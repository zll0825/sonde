package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultFiscalBase = "https://api.fiscaldata.treasury.gov/services/api/fiscal_service"
	tgaPath           = "/v1/accounting/dts/operating_cash_balance"
	tgaScale          = 1e6
	metricTGA         = "fed.ins.tga_close"
	providerFiscal    = "fiscaldata"
	tgaLatestDays     = 21
)

// FiscalDataCollector reads Treasury operating cash balance (TGA close).
type FiscalDataCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
}

// NewFiscalDataCollector builds a production FiscalData client.
func NewFiscalDataCollector() *FiscalDataCollector {
	return NewFiscalDataCollectorWithClient(provider.NewSafeHTTPClient(provider.FiscalDataConfig()))
}

// NewFiscalDataCollectorWithClient injects the HTTP client (tests use httptest).
func NewFiscalDataCollectorWithClient(client *provider.SafeHTTPClient) *FiscalDataCollector {
	return &FiscalDataCollector{client: client, baseURL: defaultFiscalBase}
}

type tgaGeneration struct {
	start       time.Time
	end         time.Time // zero = open-ended
	accountType string
	field       string // "close" or "open"
}

func tgaGenerationFor(date time.Time) (tgaGeneration, error) {
	d := date.UTC().Truncate(24 * time.Hour)
	gens := []tgaGeneration{
		{start: dateUTC(2005, 10, 3), end: dateUTC(2021, 9, 30), accountType: "Federal Reserve Account", field: "close"},
		{start: dateUTC(2021, 10, 1), end: dateUTC(2022, 4, 15), accountType: "Treasury General Account (TGA)", field: "close"},
		{start: dateUTC(2022, 4, 18), accountType: "Treasury General Account (TGA) Closing Balance", field: "open"},
	}
	for _, g := range gens {
		if d.Before(g.start) {
			continue
		}
		if !g.end.IsZero() && d.After(g.end) {
			continue
		}
		return g, nil
	}
	return tgaGeneration{}, fmt.Errorf("no TGA account_type generation for %s", d.Format("2006-01-02"))
}

type tgaRow struct {
	RecordDate    string `json:"record_date"`
	AccountType   string `json:"account_type"`
	CloseTodayBal string `json:"close_today_bal"`
	OpenTodayBal  string `json:"open_today_bal"`
}

type tgaResponse struct {
	Data []tgaRow `json:"data"`
	Meta struct {
		TotalPages int `json:"total_pages"`
	} `json:"meta"`
}

func parseBalance(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return 0, fmt.Errorf("balance is %q", s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse balance %q: %w", s, err)
	}
	return v, nil
}

// pickTGA selects the generation-matching row for date. It does not fall
// back to the first row when the current generation is missing.
func pickTGA(date time.Time, rows []tgaRow) (float64, error) {
	gen, err := tgaGenerationFor(date)
	if err != nil {
		return 0, err
	}
	wantDate := date.UTC().Format("2006-01-02")
	for _, row := range rows {
		if row.RecordDate != wantDate || row.AccountType != gen.accountType {
			continue
		}
		raw := row.CloseTodayBal
		if gen.field == "open" {
			raw = row.OpenTodayBal
		}
		v, err := parseBalance(raw)
		if err != nil {
			return 0, fmt.Errorf("TGA %s %s: %w", wantDate, gen.accountType, err)
		}
		return v * tgaScale, nil
	}
	return 0, fmt.Errorf("TGA %s: no row matching account_type %q", wantDate, gen.accountType)
}

func (c *FiscalDataCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	end := time.Now().UTC()
	start := end.Add(-tgaLatestDays * 24 * time.Hour)
	rows, err := c.fetchRows(ctx, start, end)
	if err != nil {
		return nil, err
	}
	latest := ""
	for _, row := range rows {
		if row.RecordDate > latest {
			latest = row.RecordDate
		}
	}
	if latest == "" {
		return nil, fmt.Errorf("TGA: no rows in latest window")
	}
	ts, err := parseDateUTC(latest)
	if err != nil {
		return nil, fmt.Errorf("TGA latest date: %w", err)
	}
	v, err := pickTGA(ts, rows)
	if err != nil {
		return nil, err
	}
	return []pluginrunner.Snapshot{snap(metricTGA, v, ts, time.Now(), providerFiscal)}, nil
}

func (c *FiscalDataCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("TGA history end precedes start")
	}
	rows, err := c.fetchRows(ctx, start, end)
	if err != nil {
		return nil, err
	}
	dates := map[string]struct{}{}
	for _, row := range rows {
		dates[row.RecordDate] = struct{}{}
	}
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, len(dates))
	for d := range dates {
		ts, err := parseDateUTC(d)
		if err != nil {
			return nil, fmt.Errorf("TGA date %s: %w", d, err)
		}
		if ts.Before(start.UTC().Truncate(24*time.Hour)) || ts.After(end.UTC()) {
			continue
		}
		v, err := pickTGA(ts, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap(metricTGA, v, ts, fetchedAt, providerFiscal))
	}
	return out, nil
}

func (c *FiscalDataCollector) fetchRows(ctx context.Context, start, end time.Time) ([]tgaRow, error) {
	var all []tgaRow
	for page := 1; page <= 50; page++ {
		u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + tgaPath)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("filter", fmt.Sprintf("record_date:gte:%s,record_date:lte:%s",
			start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02")))
		q.Set("sort", "record_date")
		q.Set("page[size]", "10000")
		q.Set("page[number]", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		body, err := doGET(ctx, c.client, u.String())
		if err != nil {
			return nil, err
		}
		var resp tgaResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode FiscalData TGA: %w", err)
		}
		all = append(all, resp.Data...)
		if len(resp.Data) == 0 {
			break
		}
		if resp.Meta.TotalPages > 0 && page >= resp.Meta.TotalPages {
			break
		}
		if resp.Meta.TotalPages == 0 && len(resp.Data) < 10000 {
			break
		}
	}
	return all, nil
}
