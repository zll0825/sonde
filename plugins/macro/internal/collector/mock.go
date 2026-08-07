package collector

import (
	"context"
	"math"
	"math/rand"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// Mock returns deterministic-looking but jittered macro series. Real sources
// (FRED, Treasury, BIS) are wired as separate Provider implementations when
// credentials are configured via env.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:    "fed.ins.balance_sheet",
			Value:       7_200_000_000_000 + rand.Float64()*100_000_000_000, // ~$7.2T
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.ten_year_yield",
			Value:       4.2 + rand.Float64()*0.1, // ~4.2%
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.dollar_index",
			Value:       103.0 + rand.Float64()*0.5,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.usd_cny",
			Value:       7.2 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily macro series across [start, end]
// using bounded random walk drift, tagging every sample as "estimated".
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*4)
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		seeded := rand.New(rand.NewSource(d.Unix() / 86400))
		drift := 1.0 + (seeded.Float64()-0.5)*0.005 // tighter drift — macro moves slower
		drift = math.Max(0.95, math.Min(1.05, drift))
		out = append(out,
			pluginrunner.Snapshot{MetricID: "fed.ins.balance_sheet", Value: 7_200_000_000_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.ten_year_yield", Value: 4.2 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.dollar_index", Value: 103.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.usd_cny", Value: 7.2 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
