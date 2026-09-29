package collector

import (
	"context"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
)

// Mock returns deterministic snapshots for every registered cnmacro metric.
type Mock struct{}

var mockMetrics = []struct {
	id    string
	value float64
}{
	{metricM2Yoy, 7.5},
	{metricM1Yoy, 4.1},
	{metricM1M2, -3.4},
	{metricAFRE, 16577 * afreUnitCNY},
	{metricDR007, 1.39},
	{metricOMO7D, 1.40},
	{metricDR007OMOSpread, -1.0},
	{metricLPR1Y, 3.0},
	{metricLPR5Y, 3.5},
}

// MockMetricIDs lists the metric ids Mock emits (catalog coverage test).
func MockMetricIDs() []string {
	out := make([]string, 0, len(mockMetrics))
	for _, m := range mockMetrics {
		out = append(out, m.id)
	}
	return out
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
			Provider:    "mock_cnmacro",
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
				Provider:    "mock_cnmacro",
				SourceClass: model.SourceClassMock,
				Grade:       "estimated",
			})
		}
	}
	return out, nil
}
