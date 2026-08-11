package collector

import (
	"context"
	"math"
	"math/rand"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// Mock returns deterministic-looking but jittered commodity series for all
// real-source metrics: oil.energy.wti, metal.industrial.copper, metal.precious.gold.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:    "oil.energy.wti",
			Value:       75.0 + rand.Float64()*2.0, // ~75 $/bbl
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_commodities",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "metal.industrial.copper",
			Value:       420.0 + rand.Float64()*5.0, // ~420 ¢/lb
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_commodities",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "metal.precious.gold",
			Value:       2350.0 + rand.Float64()*20.0, // ~2350 $/oz
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_commodities",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily commodity series across [start, end]
// using bounded random walk drift for all real-source metrics.
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*3)
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		seeded := rand.New(rand.NewSource(d.Unix() / 86400))
		drift := 1.0 + (seeded.Float64()-0.5)*0.008
		drift = math.Max(0.92, math.Min(1.08, drift))
		out = append(out,
			pluginrunner.Snapshot{MetricID: "oil.energy.wti", Value: 75.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_commodities", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "metal.industrial.copper", Value: 420.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_commodities", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "metal.precious.gold", Value: 2350.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_commodities", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
