package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultCFETSBase = "https://www.chinamoney.com.cn"
	cfetsLatestPath  = "/r/cms/www/chinamoney/data/currency/prr-md.json"
	cfetsHistoryPath = "/r/cms/www/chinamoney/data/currency/prr-chrt.csv"
	providerCFETS    = "cfets"
	metricDR007      = "cn.mkt.dr007"

	// prr-chrt.csv has no header row. CFETS' own chart script
	// (/r/cms/www/chinamoney/html/currency/prr-h-chart.html, read 2026-09-29)
	// plots legend ['DR001','DR007','DR014'] from vArr[6], vArr[7], vArr[8],
	// i.e. 0-based index 7 is DR007. Whether this column is the weighted
	// rate (prr-md weightedRate) or the latest/closing rate is NOT stated;
	// crossCheckDR007 compares it against weightedRate whenever both carry
	// the same date and warns on mismatch.
	cfetsCSVDR007Col = 7
	dr007CrossTol    = 0.0001
)

// CFETSCollector reads DR007 from chinamoney.com.cn: today's intraday
// weighted rate (prr-md.json, refreshed ~15 min) plus ~3 months of history
// (prr-chrt.csv).
//
// Finality: a same-CST-day prr-md value may still move, so it is reported
// with Grade "preliminary". The next day's CSV row for that date carries the
// same (metric, timestamp, provider) key and overwrites it with "delayed".
// prr-md values dated before today (CST) are treated as final.
type CFETSCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
	now     func() time.Time
}

// NewCFETSCollector builds the production client.
func NewCFETSCollector() *CFETSCollector {
	return NewCFETSCollectorWithClient(provider.NewSafeHTTPClient(provider.ChinaMoneyConfig()))
}

// NewCFETSCollectorWithClient injects the HTTP client (tests use httptest).
func NewCFETSCollectorWithClient(client *provider.SafeHTTPClient) *CFETSCollector {
	return &CFETSCollector{client: client, baseURL: defaultCFETSBase, now: time.Now}
}

type prrRecord struct {
	Date         string `json:"date"`
	ProductCode  string `json:"productCode"`
	WeightedRate string `json:"weightedRate"`
	LatestRate   string `json:"latestRate"`
}

type prrLatest struct {
	Data struct {
		ShowDateCN string `json:"showDateCN"`
	} `json:"data"`
	Records []prrRecord `json:"records"`
}

type datedRate struct {
	date time.Time
	rate float64
}

// parsePRRLatest extracts DR007's weighted rate. Record dates are "yy-mm-dd".
func parsePRRLatest(body []byte) (datedRate, error) {
	var resp prrLatest
	if err := json.Unmarshal(body, &resp); err != nil {
		return datedRate{}, fmt.Errorf("decode prr-md: %w", err)
	}
	for _, r := range resp.Records {
		if r.ProductCode != "DR007" {
			continue
		}
		d, err := time.ParseInLocation("06-01-02", strings.TrimSpace(r.Date), time.UTC)
		if err != nil {
			return datedRate{}, fmt.Errorf("prr-md DR007 date %q: %w", r.Date, err)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r.WeightedRate), 64)
		if err != nil {
			return datedRate{}, fmt.Errorf("prr-md DR007 weightedRate %q: %w", r.WeightedRate, err)
		}
		return datedRate{date: d, rate: v}, nil
	}
	return datedRate{}, fmt.Errorf("prr-md: no DR007 record")
}

// parsePRRHistory parses rows like "2026-09-28,,,,,,1.3569,1.3911,1.399".
func parsePRRHistory(body []byte) ([]datedRate, error) {
	var out []datedRate
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) <= cfetsCSVDR007Col {
			continue
		}
		d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(cols[0]), time.UTC)
		if err != nil {
			continue
		}
		raw := strings.TrimSpace(cols[cfetsCSVDR007Col])
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out = append(out, datedRate{date: d, rate: v})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("prr-chrt.csv: no DR007 rows")
	}
	return out, nil
}

// crossCheckDR007 returns a non-empty message when the CSV row for the
// prr-md date disagrees with prr-md's weightedRate.
func crossCheckDR007(latest datedRate, history []datedRate) (checked bool, msg string) {
	for _, h := range history {
		if !h.date.Equal(latest.date) {
			continue
		}
		if math.Abs(h.rate-latest.rate) > dr007CrossTol {
			return true, fmt.Sprintf("prr-chrt.csv column %d (%.4f) != prr-md DR007 weightedRate (%.4f) on %s",
				cfetsCSVDR007Col+1, h.rate, latest.rate, h.date.Format("2006-01-02"))
		}
		return true, ""
	}
	return false, ""
}

func (c *CFETSCollector) fetch(ctx context.Context) (latest *datedRate, history []datedRate, failures []pluginrunner.CollectionFailure) {
	base := strings.TrimRight(c.baseURL, "/")
	if f, err := doGET(ctx, c.client, base+cfetsLatestPath, "application/json"); err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerCFETS, MetricID: metricDR007, Err: fmt.Errorf("prr-md: %w", err)})
	} else if l, err := parsePRRLatest(f.body); err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerCFETS, MetricID: metricDR007, Err: err})
	} else {
		latest = &l
	}
	if f, err := doGET(ctx, c.client, base+cfetsHistoryPath, "text/csv,*/*"); err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerCFETS, MetricID: metricDR007, Err: fmt.Errorf("prr-chrt: %w", err)})
	} else if h, err := parsePRRHistory(f.body); err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerCFETS, MetricID: metricDR007, Err: err})
	} else {
		history = h
	}
	if latest != nil && history != nil {
		if checked, msg := crossCheckDR007(*latest, history); checked && msg != "" {
			log.Warn().Str("provider", providerCFETS).Str("metric_id", metricDR007).Msg(msg)
		} else if checked {
			log.Debug().Str("provider", providerCFETS).Msg("DR007 CSV/prr-md cross-check ok")
		}
	}
	return latest, history, failures
}

// dr007Snapshots merges history and latest into one observation per date:
// CSV rows are final ("delayed"); a prr-md value adds a date the CSV lacks,
// graded "preliminary" when it is today's (CST) still-moving value.
func dr007Snapshots(latest *datedRate, history []datedRate, now, fetchedAt time.Time) []pluginrunner.Snapshot {
	seen := map[time.Time]bool{}
	out := make([]pluginrunner.Snapshot, 0, len(history)+1)
	for _, h := range history {
		if seen[h.date] {
			continue
		}
		seen[h.date] = true
		out = append(out, snap(metricDR007, h.rate, h.date, fetchedAt, providerCFETS, "delayed"))
	}
	if latest != nil && !seen[latest.date] {
		grade := "delayed"
		if !latest.date.Before(todayCST(now)) {
			grade = "preliminary"
		}
		out = append(out, snap(metricDR007, latest.rate, latest.date, fetchedAt, providerCFETS, grade))
	}
	return out
}

func (c *CFETSCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	fetchedAt := time.Now()
	latest, history, failures := c.fetch(ctx)
	// Latest mode: newest final CSV row plus the prr-md value.
	var newest []datedRate
	if len(history) > 0 {
		best := history[0]
		for _, h := range history[1:] {
			if h.date.After(best.date) {
				best = h
			}
		}
		newest = []datedRate{best}
	}
	return finish(dr007Snapshots(latest, newest, c.now(), fetchedAt), failures, true)
}

func (c *CFETSCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("cfets window end precedes start")
	}
	fetchedAt := time.Now()
	latest, history, failures := c.fetch(ctx)
	var snaps []pluginrunner.Snapshot
	for _, s := range dr007Snapshots(latest, history, c.now(), fetchedAt) {
		if inWindow(s.Timestamp, start, end) {
			snaps = append(snaps, s)
		}
	}
	return finish(snaps, failures, false)
}
