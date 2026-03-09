package activities

import (
	"context"
	"database/sql"
	"os"
	"sync"

	_ "github.com/lib/pq"
)

type PartnerStats struct {
	PartnerID           int
	PartnerCode         string
	MerchantCount       int
	ActiveLoans         int
	PendingTransactions int
}

var (
	db     *sql.DB
	dbOnce sync.Once
)

func getDB() *sql.DB {
	dbOnce.Do(func() {
		var err error
		db, err = sql.Open("postgres", os.Getenv("DATABASE_URL"))
		if err != nil {
			panic("failed to open db: " + err.Error())
		}
		db.SetMaxOpenConns(10)
	})
	return db
}

func GetPartnerStats(ctx context.Context, partnerCode string) (*PartnerStats, error) {
	d := getDB()

	stats := &PartnerStats{PartnerCode: partnerCode}
	err := d.QueryRowContext(ctx, `
		SELECT
			p.id,
			COUNT(DISTINCT m.id),
			COUNT(DISTINCT l.id),
			COUNT(t.id)
		FROM partners p
		JOIN merchants m ON m.partner_id = p.id
		LEFT JOIN loans l ON l.merchant_id = m.id AND l.status = 'active'
		LEFT JOIN transactions t ON t.merchant_id = m.id AND t.status = 'pending'
		WHERE p.code = $1
		GROUP BY p.id
	`, partnerCode).Scan(
		&stats.PartnerID,
		&stats.MerchantCount,
		&stats.ActiveLoans,
		&stats.PendingTransactions,
	)
	if err != nil {
		return nil, err
	}
	return stats, nil
}
