package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	auctionsPath         = "/v1/accounting/od/auctions_query"
	metricAuctionSettle  = "us.mkt.treasury_settlement"
	auctionLatestDays    = 21
	auctionsFields       = "issue_date,security_type,security_term,total_accepted,soma_accepted"
	auctionsPageSize     = 10000
	auctionsMaxPages     = 50
	auctionsNullSentinel = "null"
)

// AuctionsCollector sums, per settlement (issue) date, what the public paid
// for Treasury auctions: total_accepted − soma_accepted. SOMA add-ons are
// rollovers of the Fed's own holdings and move no cash into the TGA.
//
// Gross only — maturing securities are not netted out, so this explains TGA
// inflows on settlement days, not net issuance.
type AuctionsCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
	now     func() time.Time
}

// NewAuctionsCollector builds a production FiscalData auctions client.
func NewAuctionsCollector() *AuctionsCollector {
	return NewAuctionsCollectorWithClient(provider.NewSafeHTTPClient(provider.FiscalDataConfig()))
}

// NewAuctionsCollectorWithClient injects the HTTP client (tests use httptest).
func NewAuctionsCollectorWithClient(client *provider.SafeHTTPClient) *AuctionsCollector {
	return &AuctionsCollector{client: client, baseURL: defaultFiscalBase, now: time.Now}
}

type auctionRow struct {
	IssueDate     string `json:"issue_date"`
	SecurityType  string `json:"security_type"`
	SecurityTerm  string `json:"security_term"`
	TotalAccepted string `json:"total_accepted"`
	SOMAAccepted  string `json:"soma_accepted"`
}

type auctionsResponse struct {
	Data []auctionRow `json:"data"`
	Meta struct {
		TotalPages int `json:"total_pages"`
	} `json:"meta"`
}

type settlement struct {
	date  time.Time
	value float64
}

// settlementsByDate aggregates rows by issue_date. A date is dropped when it
// is after today (not yet settled) or when any of its auctions has no result
// yet — a partial sum would understate that day's inflow.
func settlementsByDate(rows []auctionRow, today time.Time) ([]settlement, error) {
	type acc struct {
		sum        float64
		incomplete bool
	}
	byDate := map[string]*acc{}
	for _, r := range rows {
		a := byDate[r.IssueDate]
		if a == nil {
			a = &acc{}
			byDate[r.IssueDate] = a
		}
		total := strings.TrimSpace(r.TotalAccepted)
		if total == "" || total == auctionsNullSentinel {
			a.incomplete = true
			continue
		}
		t, err := strconv.ParseFloat(total, 64)
		if err != nil {
			return nil, fmt.Errorf("auction %s %s %s total_accepted %q: %w", r.IssueDate, r.SecurityType, r.SecurityTerm, total, err)
		}
		soma := 0.0
		if s := strings.TrimSpace(r.SOMAAccepted); s != "" && s != auctionsNullSentinel {
			soma, err = strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("auction %s %s %s soma_accepted %q: %w", r.IssueDate, r.SecurityType, r.SecurityTerm, s, err)
			}
		}
		a.sum += t - soma
	}
	todayUTC := today.UTC().Truncate(24 * time.Hour)
	out := make([]settlement, 0, len(byDate))
	for d, a := range byDate {
		if a.incomplete {
			continue
		}
		ts, err := parseDateUTC(d)
		if err != nil {
			return nil, fmt.Errorf("auction issue_date %q: %w", d, err)
		}
		if ts.After(todayUTC) {
			continue
		}
		out = append(out, settlement{date: ts, value: a.sum})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out, nil
}

func (c *AuctionsCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := c.now()
	rows, err := c.fetchRows(ctx, now.Add(-auctionLatestDays*24*time.Hour), now)
	if err != nil {
		return nil, err
	}
	settles, err := settlementsByDate(rows, now)
	if err != nil {
		return nil, err
	}
	if len(settles) == 0 {
		return nil, fmt.Errorf("treasury auctions: no settled issue date in latest %d days", auctionLatestDays)
	}
	last := settles[len(settles)-1]
	return []pluginrunner.Snapshot{snap(metricAuctionSettle, last.value, last.date, now, providerFiscal)}, nil
}

func (c *AuctionsCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("treasury auctions history end precedes start")
	}
	now := c.now()
	rows, err := c.fetchRows(ctx, start, end)
	if err != nil {
		return nil, err
	}
	settles, err := settlementsByDate(rows, now)
	if err != nil {
		return nil, err
	}
	out := make([]pluginrunner.Snapshot, 0, len(settles))
	for _, s := range settles {
		out = append(out, snap(metricAuctionSettle, s.value, s.date, now, providerFiscal))
	}
	return out, nil
}

func (c *AuctionsCollector) fetchRows(ctx context.Context, start, end time.Time) ([]auctionRow, error) {
	var all []auctionRow
	for page := 1; page <= auctionsMaxPages; page++ {
		u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + auctionsPath)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("fields", auctionsFields)
		q.Set("filter", fmt.Sprintf("issue_date:gte:%s,issue_date:lte:%s",
			start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02")))
		q.Set("sort", "issue_date")
		q.Set("page[size]", strconv.Itoa(auctionsPageSize))
		q.Set("page[number]", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		body, err := doGET(ctx, c.client, u.String())
		if err != nil {
			return nil, err
		}
		var resp auctionsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode FiscalData auctions: %w", err)
		}
		all = append(all, resp.Data...)
		if len(resp.Data) == 0 || (resp.Meta.TotalPages > 0 && page >= resp.Meta.TotalPages) ||
			(resp.Meta.TotalPages == 0 && len(resp.Data) < auctionsPageSize) {
			break
		}
	}
	return all, nil
}
