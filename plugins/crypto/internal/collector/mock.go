package collector

import (
	"context"
	"math"
	"math/rand"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// Mock returns deterministic-looking but jittered crypto data for development
// and CI. Only provides data for real-source metrics (price, hash_rate, tx_count).
//
// Synthetic metrics (btc.ass.exchange_balance) have been retired; no free
// trustworthy public source exists for exchange balance data.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:    "btc.ass.price",
			Value:       67_000 + rand.Float64()*500,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_crypto",
			SourceClass: model.SourceClassMock,
			Grade:       "realtime",
		},
		{
			MetricID:    "btc.ass.hash_rate",
			Value:       620 + rand.Float64()*20,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_crypto",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
		{
			MetricID:    "btc.ass.tx_count",
			Value:       350_000 + rand.Float64()*50_000,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_crypto",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily series across [start, end] with
// bounded random walk back from the current value. Only for real-source metrics.
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*3)
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		daysAgo := int(time.Since(d).Hours() / 24)
		seeded := rand.New(rand.NewSource(d.Unix() / 86400))
		drift := 1.0 + (seeded.Float64()-0.5)*0.02*math.Min(float64(daysAgo), 7)
		drift = math.Max(0.85, math.Min(1.15, drift))
		out = append(out,
			pluginrunner.Snapshot{MetricID: "btc.ass.price", Value: 67_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "btc.ass.hash_rate", Value: 620 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "btc.ass.tx_count", Value: 350_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
