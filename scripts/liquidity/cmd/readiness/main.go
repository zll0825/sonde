// Command readiness reports which metrics have enough real history to carry
// an alert rule under the liquidity plan's §5 gate. Read-only.
//
//	DATABASE_URL=postgres://... go run ./scripts/liquidity/cmd/readiness [uid-prefix ...]
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"

	"sonde/scripts/liquidity"
)

// Distinct observation periods per current metric, real data only.
const query = `
SELECT m.id, m.frequency, m.plugin_id,
       COUNT(DISTINCT date_trunc(CASE m.frequency
             WHEN 'weekly' THEN 'week' WHEN 'monthly' THEN 'month'
             WHEN 'quarterly' THEN 'quarter' ELSE 'day' END, o.time)) AS periods,
       MIN(o.time), MAX(o.time)
FROM metric_definitions m
LEFT JOIN observations o ON o.metric_uid = m.uid AND o.source_class = 'real'
WHERE m.effective_to IS NULL AND m.active
GROUP BY m.id, m.frequency, m.plugin_id
ORDER BY m.plugin_id, m.id`

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, query)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer rows.Close()

	prefixes := os.Args[1:]
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLUGIN\tMETRIC\tFREQ\tPERIODS\tMIN\tSTATUS\tFIRST\tLAST")
	for rows.Next() {
		var id, freq, plugin string
		var n int
		var first, last *time.Time
		if err := rows.Scan(&id, &freq, &plugin, &n, &first, &last); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !matches(id, prefixes) {
			continue
		}
		min, gated, ready := liquidity.Readiness(freq, n)
		status := "not gated"
		switch {
		case ready:
			status = "ready"
		case gated:
			status = fmt.Sprintf("short %d", min-n)
		}
		minCol := "-"
		if gated {
			minCol = fmt.Sprint(min)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", plugin, id, freq, n, minCol, status, day(first), day(last))
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = w.Flush()
}

func matches(id string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func day(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02")
}
