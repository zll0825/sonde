// Command cli is a read-only operator tool for soak SQL that used to live
// only in reports/*.sql. It shares DATABASE_URL with the API process.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"capital_observatory/pkg/model"
)

const usageText = `capital-observatory cli — read-only soak queries

Usage:
  cli alerts
  cli budget [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--limit N]

DATABASE_URL defaults to the local compose DSN.
`

// alertsActiveSQL matches GET /api/alerts. Keep the two queries in lockstep.
const alertsActiveSQL = `
		SELECT id, title, summary, severity, metric_id, triggered_at, status,
		       source_provider, source_class, dedup_count, last_deduplicated_at
		FROM alerts WHERE status = 'active'
		ORDER BY triggered_at DESC LIMIT 100
	`

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "help", "-h", "--help":
		usage()
		return
	case "alerts", "budget":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	ctx := context.Background()
	db, err := openDB(ctx)
	if err != nil {
		fatal(err)
	}
	defer db.Close()

	switch os.Args[1] {
	case "alerts":
		if err := cmdAlerts(ctx, db); err != nil {
			fatal(err)
		}
	case "budget":
		fs := flag.NewFlagSet("budget", flag.ExitOnError)
		from := fs.String("from", "", "start UTC date YYYY-MM-DD (inclusive), default today")
		to := fs.String("to", "", "end UTC date YYYY-MM-DD (inclusive), default today")
		limit := fs.Int("limit", model.DefaultAlertBudgetPerDay, "real alert daily limit")
		_ = fs.Parse(os.Args[2:])
		if err := cmdBudget(ctx, db, *from, *to, *limit); err != nil {
			fatal(err)
		}
	}
}

func usage() {
	fmt.Fprint(os.Stderr, usageText)
}

func openDB(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
	}
	return pgxpool.New(ctx, dsn)
}

func cmdAlerts(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, alertsActiveSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	alerts := []map[string]any{}
	for rows.Next() {
		var (
			id, title, summary, severity, metricID, status string
			sourceProvider, sourceClass                    string
			triggeredAt                                    time.Time
			dedupCount                                     int
			lastDeduplicatedAt                             *time.Time
		)
		if err := rows.Scan(&id, &title, &summary, &severity, &metricID, &triggeredAt, &status,
			&sourceProvider, &sourceClass, &dedupCount, &lastDeduplicatedAt); err != nil {
			return err
		}
		alerts = append(alerts, map[string]any{
			"id": id, "title": title, "summary": summary, "severity": severity,
			"metric_id": metricID, "triggered_at": triggeredAt, "status": status,
			"source_provider": sourceProvider, "source_class": sourceClass,
			"dedup_count": dedupCount, "last_deduplicated_at": lastDeduplicatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writeJSON(alerts)
}

func cmdBudget(ctx context.Context, db *pgxpool.Pool, from, to string, limit int) error {
	today := time.Now().UTC().Format("2006-01-02")
	if from == "" {
		from = today
	}
	if to == "" {
		to = today
	}
	sqlBytes, err := readBudgetSQL()
	if err != nil {
		return err
	}
	rows, err := db.Query(ctx, string(sqlBytes), from, to, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			day                                      time.Time
			realCount, mockCount, testCount, unknown int
			realLimit                                int
			within                                   bool
		)
		if err := rows.Scan(&day, &realCount, &mockCount, &testCount, &unknown, &realLimit, &within); err != nil {
			return err
		}
		out = append(out, map[string]any{
			"utc_day":       day.Format("2006-01-02"),
			"real_count":    realCount,
			"mock_count":    mockCount,
			"test_count":    testCount,
			"unknown_count": unknown,
			"real_limit":    realLimit,
			"within_budget": within,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writeJSON(out)
}

func readBudgetSQL() ([]byte, error) {
	candidates := []string{
		"reports/daily_alert_budget.sql",
		filepath.Join("..", "..", "reports", "daily_alert_budget.sql"),
	}
	var last error
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil {
			return b, nil
		}
		last = err
	}
	return nil, fmt.Errorf("read budget SQL: %w", last)
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "cli: %v\n", err)
	os.Exit(1)
}
