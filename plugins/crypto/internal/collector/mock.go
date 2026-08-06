package collector

import (
	"context"
	"math"
	"math/rand"
	"time"

	"capital_observatory/pkg/pluginrunner"
)

// Mock returns deterministic-looking but jittered crypto data for development
// and CI. Real data sources (Glassnode, exchange APIs) are wired as separate
// Provider implementations when their credentials are configured via env.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:  "btc.ass.exchange_balance",
			Value:     1_900_000 + rand.Float64()*50_000, // ~1.9M BTC on exchanges
			Timestamp: now,
			FetchedAt: now,
			Provider:  "mock_crypto",
			Grade:     "estimated",
		},
		{
			MetricID:  "btc.ass.hash_rate",
			Value:     620 + rand.Float64()*20, // ~620 EH/s
			Timestamp: now,
			FetchedAt: now,
			Provider:  "mock_crypto",
			Grade:     "estimated",
		},
		{
			MetricID:  "btc.ass.price",
			Value:     67_000 + rand.Float64()*500,
			Timestamp: now,
			FetchedAt: now,
			Provider:  "mock_crypto",
			Grade:     "realtime",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily series across [start, end] with
// bounded random walk back from the current value. All samples are tagged
// "estimated" so real-time data arriving later outranks them in the quality
// coverage matrix.
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
		// Deterministic-ish drift using a seed from the day.
		seeded := rand.New(rand.NewSource(d.Unix() / 86400))
		drift := 1.0 + (seeded.Float64()-0.5)*0.02*math.Min(float64(daysAgo), 7)
		drift = math.Max(0.85, math.Min(1.15, drift))
		out = append(out,
			pluginrunner.Snapshot{MetricID: "btc.ass.exchange_balance", Value: 1_900_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "btc.ass.hash_rate", Value: 620 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "btc.ass.price", Value: 67_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_crypto", Grade: "estimated"},
		)
	}
	return out, nil
}
