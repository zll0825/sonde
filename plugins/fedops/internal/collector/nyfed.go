package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultNYFedBase = "https://markets.newyorkfed.org/api"
	metricRRP        = "fed.ins.rrp"
	metricSRF        = "fed.ins.srf_usage"
	metricSOFR       = "us.mkt.sofr"
	metricSOFRP99    = "us.mkt.sofr_p99"
	metricSOFRTail   = "us.mkt.sofr_tail"
	providerNYFed    = "nyfed"
	nyfedLatestDays  = 14
)

// NYFedCollector reads repo operations (RRP/SRF) and SOFR from the NY Fed.
type NYFedCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
}

// NewNYFedCollector builds a production NY Fed client.
func NewNYFedCollector() *NYFedCollector {
	return NewNYFedCollectorWithClient(provider.NewSafeHTTPClient(provider.NYFedConfig()))
}

// NewNYFedCollectorWithClient injects the HTTP client (tests use httptest).
func NewNYFedCollectorWithClient(client *provider.SafeHTTPClient) *NYFedCollector {
	return &NYFedCollector{client: client, baseURL: defaultNYFedBase}
}

type repoOp struct {
	OperationDate    string  `json:"operationDate"`
	OperationType    string  `json:"operationType"`
	OperationMethod  string  `json:"operationMethod"`
	Term             string  `json:"term"`
	TotalAmtAccepted float64 `json:"totalAmtAccepted"`
}

type repoResponse struct {
	Repo struct {
		Operations []repoOp `json:"operations"`
	} `json:"repo"`
}

type sofrRate struct {
	EffectiveDate       string  `json:"effectiveDate"`
	Type                string  `json:"type"`
	PercentRate         float64 `json:"percentRate"`
	PercentPercentile99 float64 `json:"percentPercentile99"`
}

type sofrResponse struct {
	RefRates []sofrRate `json:"refRates"`
}

func isRRP(op repoOp) bool {
	return op.OperationType == "Reverse Repo"
}

func isSRF(op repoOp) bool {
	return op.OperationType == "Repo" && op.OperationMethod == "Full Allotment"
}

func pickRRPForDate(ops []repoOp, date string) (float64, bool) {
	var overnight, all []repoOp
	for _, op := range ops {
		if op.OperationDate != date || !isRRP(op) {
			continue
		}
		all = append(all, op)
		if strings.EqualFold(op.Term, "Overnight") {
			overnight = append(overnight, op)
		}
	}
	chosen := all
	if len(overnight) > 0 {
		chosen = overnight
	}
	if len(chosen) == 0 {
		return 0, false
	}
	var sum float64
	for _, op := range chosen {
		sum += op.TotalAmtAccepted
	}
	return sum, true
}

func srfSumForDate(ops []repoOp, date string) float64 {
	var sum float64
	for _, op := range ops {
		if op.OperationDate == date && isSRF(op) {
			sum += op.TotalAmtAccepted
		}
	}
	return sum
}

func latestOperationDate(ops []repoOp) string {
	latest := ""
	for _, op := range ops {
		if op.OperationDate > latest {
			latest = op.OperationDate
		}
	}
	return latest
}

func repoSnapshotsForDate(ops []repoOp, date string, fetchedAt time.Time) []pluginrunner.Snapshot {
	ts, err := parseDateUTC(date)
	if err != nil {
		return nil
	}
	out := make([]pluginrunner.Snapshot, 0, 2)
	if v, ok := pickRRPForDate(ops, date); ok {
		out = append(out, snap(metricRRP, v, ts, fetchedAt, providerNYFed))
	}
	out = append(out, snap(metricSRF, srfSumForDate(ops, date), ts, fetchedAt, providerNYFed))
	return out
}

func sofrSnapshots(rates []sofrRate, fetchedAt time.Time) []pluginrunner.Snapshot {
	out := make([]pluginrunner.Snapshot, 0, len(rates)*3)
	for _, r := range rates {
		if r.Type != "" && r.Type != "SOFR" {
			continue
		}
		ts, err := parseDateUTC(r.EffectiveDate)
		if err != nil {
			continue
		}
		tail := (r.PercentPercentile99 - r.PercentRate) * 100
		out = append(out,
			snap(metricSOFR, r.PercentRate, ts, fetchedAt, providerNYFed),
			snap(metricSOFRP99, r.PercentPercentile99, ts, fetchedAt, providerNYFed),
			snap(metricSOFRTail, tail, ts, fetchedAt, providerNYFed),
		)
	}
	return out
}

func (c *NYFedCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	fetchedAt := time.Now()
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure

	end := fetchedAt.UTC()
	start := end.Add(-nyfedLatestDays * 24 * time.Hour)
	ops, err := c.fetchRepo(ctx, start, end)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerNYFed, MetricID: metricRRP, Err: err})
	} else if date := latestOperationDate(ops); date != "" {
		snaps = append(snaps, repoSnapshotsForDate(ops, date, fetchedAt)...)
	}

	rates, err := c.fetchSOFRLatest(ctx)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerNYFed, MetricID: metricSOFR, Err: err})
	} else {
		snaps = append(snaps, sofrSnapshots(rates, fetchedAt)...)
	}

	if len(snaps) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	if len(failures) > 0 {
		return snaps, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, nil
}

func (c *NYFedCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("NY Fed history end precedes start")
	}
	fetchedAt := time.Now()
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure

	ops, err := c.fetchRepo(ctx, start, end)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerNYFed, MetricID: metricRRP, Err: err})
	} else {
		seen := map[string]struct{}{}
		for _, op := range ops {
			if _, ok := seen[op.OperationDate]; ok {
				continue
			}
			seen[op.OperationDate] = struct{}{}
			snaps = append(snaps, repoSnapshotsForDate(ops, op.OperationDate, fetchedAt)...)
		}
	}

	rates, err := c.fetchSOFRSearch(ctx, start, end)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerNYFed, MetricID: metricSOFR, Err: err})
	} else {
		snaps = append(snaps, sofrSnapshots(rates, fetchedAt)...)
	}

	if len(snaps) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	if len(failures) > 0 {
		return snaps, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, nil
}

func (c *NYFedCollector) fetchRepo(ctx context.Context, start, end time.Time) ([]repoOp, error) {
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/rp/results/search.json")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("startDate", start.UTC().Format("2006-01-02"))
	q.Set("endDate", end.UTC().Format("2006-01-02"))
	u.RawQuery = q.Encode()
	body, err := doGET(ctx, c.client, u.String())
	if err != nil {
		return nil, err
	}
	var resp repoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode NY Fed repo results: %w", err)
	}
	return resp.Repo.Operations, nil
}

func (c *NYFedCollector) fetchSOFRLatest(ctx context.Context) ([]sofrRate, error) {
	rawURL := strings.TrimRight(c.baseURL, "/") + "/rates/secured/sofr/last/1.json"
	return c.decodeSOFR(ctx, rawURL)
}

func (c *NYFedCollector) fetchSOFRSearch(ctx context.Context, start, end time.Time) ([]sofrRate, error) {
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/rates/secured/sofr/search.json")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("startDate", start.UTC().Format("2006-01-02"))
	q.Set("endDate", end.UTC().Format("2006-01-02"))
	u.RawQuery = q.Encode()
	return c.decodeSOFR(ctx, u.String())
}

func (c *NYFedCollector) decodeSOFR(ctx context.Context, rawURL string) ([]sofrRate, error) {
	body, err := doGET(ctx, c.client, rawURL)
	if err != nil {
		return nil, err
	}
	var resp sofrResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode NY Fed SOFR: %w", err)
	}
	if len(resp.RefRates) == 0 {
		return nil, fmt.Errorf("NY Fed SOFR: empty refRates")
	}
	return resp.RefRates, nil
}
