package collector

import (
	"context"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
)

// Mock returns deterministic snapshots for every registered fedops metric.
type Mock struct{}

var mockMetrics = []struct {
	id    string
	value float64
}{
	{metricTGA, 700_000_000_000},
	{metricRRP, 675_000_000},
	{metricSRF, 0},
	{metricSOFR, 4.29},
	{metricSOFRP99, 4.35},
	{metricSOFRTail, 6.0},
	{metricOFRFSI, -0.25},
}

func (Mock) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	out := make([]pluginrunner.Snapshot, 0, len(mockMetrics))
	for _, m := range mockMetrics {
		out = append(out, pluginrunner.Snapshot{
			MetricID:    m.id,
			Value:       m.value,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_fedops",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		})
	}
	return out, nil
}

func (Mock) GetSnapshotsForWindow(_ context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*len(mockMetrics))
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		for _, m := range mockMetrics {
			out = append(out, pluginrunner.Snapshot{
				MetricID:    m.id,
				Value:       m.value,
				Timestamp:   d,
				FetchedAt:   fetchedAt,
				Provider:    "mock_fedops",
				SourceClass: model.SourceClassMock,
				Grade:       "estimated",
			})
		}
	}
	return out, nil
}
