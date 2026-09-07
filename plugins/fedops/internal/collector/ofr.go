package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultOFRURL = "https://www.financialresearch.gov/financial-stress-index/data/fsi.json"
	metricOFRFSI  = "us.mkt.ofr_fsi"
	providerOFR   = "ofr"
)

// OFRCollector reads the OFR Financial Stress Index.
type OFRCollector struct {
	client *provider.SafeHTTPClient
	url    string
}

// NewOFRCollector builds a production OFR client.
func NewOFRCollector() *OFRCollector {
	return NewOFRCollectorWithClient(provider.NewSafeHTTPClient(provider.OFRConfig()))
}

// NewOFRCollectorWithClient injects the HTTP client (tests use httptest).
func NewOFRCollectorWithClient(client *provider.SafeHTTPClient) *OFRCollector {
	return &OFRCollector{client: client, url: defaultOFRURL}
}

type ofrFile struct {
	OFRFSI struct {
		Data [][]float64 `json:"data"`
	} `json:"OFRFSI"`
}

type ofrPoint struct {
	t time.Time
	v float64
}

func parseOFRPoints(data [][]float64) []ofrPoint {
	out := make([]ofrPoint, 0, len(data))
	for _, row := range data {
		if len(row) < 2 {
			continue
		}
		out = append(out, ofrPoint{
			t: time.UnixMilli(int64(row[0])).UTC(),
			v: row[1],
		})
	}
	return out
}

func (c *OFRCollector) fetchPoints(ctx context.Context) ([]ofrPoint, error) {
	body, err := doGET(ctx, c.client, c.url)
	if err != nil {
		return nil, err
	}
	var file ofrFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("decode OFR FSI: %w", err)
	}
	points := parseOFRPoints(file.OFRFSI.Data)
	if len(points) == 0 {
		return nil, fmt.Errorf("OFR FSI: no data points")
	}
	return points, nil
}

func (c *OFRCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	points, err := c.fetchPoints(ctx)
	if err != nil {
		return nil, err
	}
	latest := points[0]
	for _, p := range points[1:] {
		if p.t.After(latest.t) {
			latest = p
		}
	}
	return []pluginrunner.Snapshot{snap(metricOFRFSI, latest.v, latest.t, time.Now(), providerOFR)}, nil
}

func (c *OFRCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("OFR history end precedes start")
	}
	points, err := c.fetchPoints(ctx)
	if err != nil {
		return nil, err
	}
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, len(points))
	for _, p := range points {
		if p.t.Before(start) || p.t.After(end) {
			continue
		}
		out = append(out, snap(metricOFRFSI, p.v, p.t, fetchedAt, providerOFR))
	}
	return out, nil
}
