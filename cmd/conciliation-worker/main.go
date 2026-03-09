package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"
)

// Este binario corre DENTRO del K8s Job que Temporal crea.
// Recibe la config por env vars y procesa la conciliación de un partner.
func main() {
	partnerCode := os.Getenv("PARTNER_CODE")
	partnerID, _ := strconv.Atoi(os.Getenv("PARTNER_ID"))
	tier := os.Getenv("TIER")

	concurrency := concurrencyForTier(tier)
	log.Printf("Conciliation worker started: partner=%s id=%d tier=%s concurrency=%d", partnerCode, partnerID, tier, concurrency)

	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("DB connection failed: %v", err)
	}
	db.SetMaxOpenConns(concurrency + 2)
	db.SetMaxIdleConns(concurrency + 2)
	defer db.Close()

	ctx := context.Background()
	start := time.Now()

	totalCollections, err := processConciliation(ctx, db, partnerID, concurrency)
	if err != nil {
		log.Fatalf("Conciliation failed: %v", err)
	}

	elapsed := time.Since(start)
	log.Printf("Conciliation completed: partner=%s collections=%d elapsed=%s", partnerCode, totalCollections, elapsed)
}

func concurrencyForTier(tier string) int {
	switch tier {
	case "light":
		return 4
	case "medium":
		return 10
	case "heavy":
		return 20
	case "extra-heavy":
		return 40
	default:
		return 4
	}
}

func processConciliation(ctx context.Context, db *sql.DB, partnerID int, concurrency int) (int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, l.id, l.remaining_amount
		FROM merchants m
		JOIN loans l ON l.merchant_id = m.id AND l.status = 'active'
		WHERE m.partner_id = $1 AND l.remaining_amount > 0
		ORDER BY m.id
	`, partnerID)
	if err != nil {
		return 0, fmt.Errorf("query merchants: %w", err)
	}
	defer rows.Close()

	type merchantLoan struct {
		merchantID      int
		loanID          int
		remainingAmount float64
	}

	var merchants []merchantLoan
	for rows.Next() {
		var ml merchantLoan
		if err := rows.Scan(&ml.merchantID, &ml.loanID, &ml.remainingAmount); err != nil {
			return 0, err
		}
		merchants = append(merchants, ml)
	}

	log.Printf("Found %d merchants with active loans, processing with concurrency=%d", len(merchants), concurrency)

	var totalCollections atomic.Int64
	var processedMerchants atomic.Int64
	var firstErr error
	var errOnce sync.Once

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, ml := range merchants {
		wg.Add(1)
		sem <- struct{}{} // acquire slot

		go func(ml merchantLoan) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			collections, err := processMerchant(ctx, db, ml.merchantID, ml.loanID)
			if err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("merchant %d: %w", ml.merchantID, err)
				})
				return
			}

			totalCollections.Add(int64(collections))
			done := processedMerchants.Add(1)
			if done%50 == 0 {
				log.Printf("Progress: %d/%d merchants processed, %d collections created",
					done, len(merchants), totalCollections.Load())
			}
		}(ml)
	}

	wg.Wait()

	if firstErr != nil {
		return int(totalCollections.Load()), firstErr
	}

	return int(totalCollections.Load()), nil
}

func processMerchant(ctx context.Context, db *sql.DB, merchantID, loanID int) (int, error) {
	// Obtener transacciones pendientes ordenadas por fecha
	rows, err := db.QueryContext(ctx, `
		SELECT id, amount
		FROM transactions
		WHERE merchant_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, merchantID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type txn struct {
		id     int
		amount float64
	}

	var transactions []txn
	for rows.Next() {
		var t txn
		if err := rows.Scan(&t.id, &t.amount); err != nil {
			return 0, err
		}
		transactions = append(transactions, t)
	}

	collectionsCreated := 0

	for _, t := range transactions {
		created, err := processTransaction(ctx, db, loanID, t.id, t.amount)
		if err != nil {
			return collectionsCreated, err
		}
		if created {
			collectionsCreated++
		}
	}

	return collectionsCreated, nil
}

// processTransaction handles a single transaction atomically.
// Reads the loan remaining from DB (with lock), calculates retention, and updates everything in one SQL transaction.
// If the process dies mid-batch, each committed transaction is fully consistent.
func processTransaction(ctx context.Context, db *sql.DB, loanID, txnID int, txnAmount float64) (bool, error) {
	dbTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer dbTx.Rollback()

	// Read current remaining from DB with row lock
	var remaining float64
	err = dbTx.QueryRowContext(ctx, `
		SELECT remaining_amount FROM loans WHERE id = $1 FOR UPDATE
	`, loanID).Scan(&remaining)
	if err != nil {
		return false, err
	}

	// Loan already paid — skip this transaction
	if remaining <= 0 {
		_, err = dbTx.ExecContext(ctx,
			`UPDATE transactions SET status = 'skipped', processed_at = NOW() WHERE id = $1`, txnID)
		if err != nil {
			return false, err
		}
		return false, dbTx.Commit()
	}

	// Calculate retention: 10% of transaction or remaining balance (whichever is less)
	retention := math.Min(txnAmount*0.10, remaining)
	retention = math.Round(retention*100) / 100

	// Create collection
	_, err = dbTx.ExecContext(ctx,
		`INSERT INTO collections (loan_id, transaction_id, amount) VALUES ($1, $2, $3)`,
		loanID, txnID, retention)
	if err != nil {
		return false, err
	}

	// Update loan balance
	remaining -= retention
	if remaining <= 0 {
		remaining = 0
	}
	if remaining <= 0 {
		_, err = dbTx.ExecContext(ctx, `
			UPDATE loans SET remaining_amount = 0, status = 'paid', paid_at = NOW()
			WHERE id = $1`, loanID)
	} else {
		_, err = dbTx.ExecContext(ctx, `
			UPDATE loans SET remaining_amount = $1, status = 'active'
			WHERE id = $2`, remaining, loanID)
	}
	if err != nil {
		return false, err
	}

	// Mark transaction as processed
	_, err = dbTx.ExecContext(ctx,
		`UPDATE transactions SET status = 'processed', processed_at = NOW() WHERE id = $1`, txnID)
	if err != nil {
		return false, err
	}

	return true, dbTx.Commit()
}
