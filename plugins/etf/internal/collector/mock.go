package collector

import (
	"context"
	"math/rand"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// Mock returns deterministic-looking but slightly jittered ETF data.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:    "gld.ass.daily_flow",
			Value:       650_000_000 + rand.Float64()*10_000_000, // ~650M–660M, fires threshold
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_etf",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
		{
			MetricID:    "gld.ass.price",
			Value:       2150.50 + rand.Float64()*10,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_etf",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
		{
			MetricID:    "eth.ass.daily_flow",
			Value:       -50_000_000 + rand.Float64()*150_000_000, // flows swing negative (outflow) to positive
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_etf",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily sample series spanning [start, end]
// from the latest mock quote with deterministic per-day drift, tagging each
// sample as Grade "estimated" so the backfill path doesn't accidentally claim
// authoritative historical data.
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*3)
	basePrice := 2150.50 + rand.Float64()*10
	baseFlow := 650_000_000 + rand.Float64()*10_000_000
	baseEthFlow := -50_000_000 + rand.Float64()*150_000_000
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		daysAgo := int(time.Since(d).Hours() / 24)
		drift := 1.0 - float64(daysAgo)*0.003
		out = append(out,
			pluginrunner.Snapshot{MetricID: "gld.ass.daily_flow", Value: baseFlow * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_etf", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "gld.ass.price", Value: basePrice * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_etf", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "eth.ass.daily_flow", Value: baseEthFlow * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_etf", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
