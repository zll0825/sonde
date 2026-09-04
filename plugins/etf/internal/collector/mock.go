package collector

import (
	"context"
	"math/rand"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
)

// Mock returns deterministic-looking but slightly jittered ETF data for
// development and CI environments where Yahoo Finance may be unavailable.
// Only provides data for real-source metrics (price, volume).
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
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
			MetricID:    "gld.ass.volume",
			Value:       5_000_000 + rand.Float64()*1_000_000,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_etf",
			SourceClass: model.SourceClassMock,
			Grade:       "estimated",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily sample series spanning [start, end] for real-source metrics only.
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*2)
	basePrice := 2150.50 + rand.Float64()*10
	baseVolume := 5_000_000 + rand.Float64()*1_000_000
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		daysAgo := int(time.Since(d).Hours() / 24)
		drift := 1.0 - float64(daysAgo)*0.003
		out = append(out,
			pluginrunner.Snapshot{MetricID: "gld.ass.price", Value: basePrice * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_etf", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "gld.ass.volume", Value: baseVolume * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_etf", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
