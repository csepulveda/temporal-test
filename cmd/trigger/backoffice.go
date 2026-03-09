package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ============================================================
// Backoffice API handlers
// ============================================================

type PartnerSummary struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Code             string `json:"code"`
	Tier             string `json:"tier"`
	MerchantCount    int    `json:"merchant_count"`
	ActiveLoans      int    `json:"active_loans"`
	PaidLoans        int    `json:"paid_loans"`
	PendingTxns      int    `json:"pending_txns"`
	ProcessedTxns    int    `json:"processed_txns"`
	SkippedTxns      int    `json:"skipped_txns"`
	TotalCollections int     `json:"total_collections"`
	CollectedAmount  float64 `json:"collected_amount"`
	TotalLoanAmount  float64 `json:"total_loan_amount"`
}

type MerchantDetail struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	ExternalID  string  `json:"external_id"`
	HasLoan     bool    `json:"has_loan"`
	LoanID      *int    `json:"loan_id,omitempty"`
	OrigAmount  *float64 `json:"original_amount,omitempty"`
	Remaining   *float64 `json:"remaining_amount,omitempty"`
	LoanStatus  *string `json:"loan_status,omitempty"`
	PendingTxns int     `json:"pending_txns"`
	ProcessedTxns int   `json:"processed_txns"`
	SkippedTxns int     `json:"skipped_txns"`
	Collections int     `json:"collections"`
	Collected   float64 `json:"collected_amount"`
}

func registerBackofficeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/partners", handlePartners)
	mux.HandleFunc("/api/v1/partners/", handlePartnerMerchants)
	mux.HandleFunc("/api/v1/loans/", handleLoanUpdate)
	mux.HandleFunc("/api/v1/transactions/create", handleCreateTransactions)
	mux.HandleFunc("/", handleBackofficeUI)
}

func getDB() (*sql.DB, error) {
	dbURL := envOr("DATABASE_URL", "")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}
	return sql.Open("postgres", dbURL)
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// GET /api/v1/partners
func handlePartners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	db, err := getDB()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(r.Context(), `
		SELECT
			p.id, p.name, p.code, p.tier,
			COUNT(DISTINCT m.id) AS merchant_count,
			COUNT(DISTINCT CASE WHEN l.status = 'active' THEN l.id END) AS active_loans,
			COUNT(DISTINCT CASE WHEN l.status = 'paid' THEN l.id END) AS paid_loans,
			COALESCE(SUM(CASE WHEN t.status = 'pending' THEN 1 ELSE 0 END), 0) AS pending_txns,
			COALESCE(SUM(CASE WHEN t.status = 'processed' THEN 1 ELSE 0 END), 0) AS processed_txns,
			COALESCE(SUM(CASE WHEN t.status = 'skipped' THEN 1 ELSE 0 END), 0) AS skipped_txns,
			(SELECT COUNT(*) FROM collections c JOIN loans ll ON c.loan_id = ll.id JOIN merchants mm ON ll.merchant_id = mm.id WHERE mm.partner_id = p.id) AS total_collections,
			(SELECT COALESCE(SUM(c.amount), 0) FROM collections c JOIN loans ll ON c.loan_id = ll.id JOIN merchants mm ON ll.merchant_id = mm.id WHERE mm.partner_id = p.id) AS collected_amount,
			(SELECT COALESCE(SUM(ll.original_amount), 0) FROM loans ll JOIN merchants mm ON ll.merchant_id = mm.id WHERE mm.partner_id = p.id) AS total_loan_amount
		FROM partners p
		LEFT JOIN merchants m ON m.partner_id = p.id
		LEFT JOIN transactions t ON t.merchant_id = m.id
		LEFT JOIN loans l ON l.merchant_id = m.id
		GROUP BY p.id
		ORDER BY p.id
	`)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var partners []PartnerSummary
	for rows.Next() {
		var p PartnerSummary
		if err := rows.Scan(&p.ID, &p.Name, &p.Code, &p.Tier,
			&p.MerchantCount, &p.ActiveLoans, &p.PaidLoans,
			&p.PendingTxns, &p.ProcessedTxns, &p.SkippedTxns,
			&p.TotalCollections, &p.CollectedAmount, &p.TotalLoanAmount); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		partners = append(partners, p)
	}

	jsonResponse(w, partners)
}

// GET /api/v1/partners/{id}/merchants
func handlePartnerMerchants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract partner ID from path: /api/v1/partners/3/merchants
	var partnerID int
	_, err := fmt.Sscanf(r.URL.Path, "/api/v1/partners/%d/merchants", &partnerID)
	if err != nil {
		jsonError(w, "invalid partner id", http.StatusBadRequest)
		return
	}

	db, err := getDB()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(r.Context(), `
		SELECT
			m.id, m.name, m.external_id,
			l.id AS loan_id, l.original_amount, l.remaining_amount, l.status AS loan_status,
			COALESCE(SUM(CASE WHEN t.status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN t.status = 'processed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN t.status = 'skipped' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM collections c WHERE c.loan_id = l.id),
			(SELECT COALESCE(SUM(c.amount), 0) FROM collections c WHERE c.loan_id = l.id)
		FROM merchants m
		LEFT JOIN loans l ON l.merchant_id = m.id
		LEFT JOIN transactions t ON t.merchant_id = m.id
		WHERE m.partner_id = $1
		GROUP BY m.id, m.name, m.external_id, l.id, l.original_amount, l.remaining_amount, l.status
		ORDER BY m.id
	`, partnerID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var merchants []MerchantDetail
	for rows.Next() {
		var m MerchantDetail
		var loanID sql.NullInt64
		var origAmount, remaining sql.NullFloat64
		var loanStatus sql.NullString

		if err := rows.Scan(&m.ID, &m.Name, &m.ExternalID,
			&loanID, &origAmount, &remaining, &loanStatus,
			&m.PendingTxns, &m.ProcessedTxns, &m.SkippedTxns,
			&m.Collections, &m.Collected); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if loanID.Valid {
			m.HasLoan = true
			id := int(loanID.Int64)
			m.LoanID = &id
			m.OrigAmount = &origAmount.Float64
			m.Remaining = &remaining.Float64
			m.LoanStatus = &loanStatus.String
		}

		merchants = append(merchants, m)
	}

	jsonResponse(w, merchants)
}

// PUT /api/v1/loans/{id} — update loan remaining amount
func handleLoanUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var loanID int
	_, err := fmt.Sscanf(r.URL.Path, "/api/v1/loans/%d", &loanID)
	if err != nil {
		jsonError(w, "invalid loan id", http.StatusBadRequest)
		return
	}

	var body struct {
		RemainingAmount float64 `json:"remaining_amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	db, err := getDB()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	status := "active"
	if body.RemainingAmount <= 0 {
		status = "paid"
	}

	result, err := db.ExecContext(r.Context(), `
		UPDATE loans SET remaining_amount = $1, status = $2,
			paid_at = CASE WHEN $2 = 'paid' THEN NOW() ELSE NULL END
		WHERE id = $3`, body.RemainingAmount, status, loanID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		jsonError(w, "loan not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, map[string]string{"status": "updated"})
}

// POST /api/v1/transactions/create — bulk create transactions for a merchant
func handleCreateTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		MerchantID int     `json:"merchant_id"`
		Count      int     `json:"count"`
		Amount     float64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	if body.Count <= 0 || body.Count > 100000 {
		jsonError(w, "count must be between 1 and 100000", http.StatusBadRequest)
		return
	}
	if body.Amount <= 0 {
		jsonError(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	db, err := getDB()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Verify merchant exists
	var exists bool
	db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM merchants WHERE id = $1)`, body.MerchantID).Scan(&exists)
	if !exists {
		jsonError(w, "merchant not found", http.StatusNotFound)
		return
	}

	// Bulk insert using a single query with generate_series
	result, err := db.ExecContext(r.Context(), `
		INSERT INTO transactions (merchant_id, amount, status, created_at)
		SELECT $1, $2, 'pending', NOW() - (random() * INTERVAL '30 days')
		FROM generate_series(1, $3)
	`, body.MerchantID, body.Amount, body.Count)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	created, _ := result.RowsAffected()
	jsonResponse(w, map[string]interface{}{
		"status":  "created",
		"count":   created,
		"message": fmt.Sprintf("Created %d transactions of $%s for merchant %d",
			created, strconv.FormatFloat(body.Amount, 'f', 2, 64), body.MerchantID),
	})
}

func handleBackofficeUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(backofficeHTML))
}
