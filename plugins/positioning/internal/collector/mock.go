package collector

import (
	"context"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
)

// Mock returns deterministic snapshots for every registered positioning metric.
type Mock struct{}

var mockMetrics = []struct {
	id    string
	value float64
}{
	{"cot.es.noncomm_net", -133_228},
	{"cot.zn.noncomm_net", -811_752},
	{"cot.gc.noncomm_net", 225_853},
	{"cot.btc.noncomm_net", 2_756},
	{metricMarginDebt, 1_453_832_000_000},
}

// lastTuesday returns the most recent Tuesday on or before t (UTC midnight).
func lastTuesday(t time.Time) time.Time {
	d := dateUTC(t.Year(), t.Month(), t.Day())
	for d.Weekday() != time.Tuesday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

func mockTimestamp(id string, t time.Time) time.Time {
	if id == metricMarginDebt {
		return dateUTC(t.Year(), t.Month(), 1)
	}
	return lastTuesday(t)
}

func (Mock) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	out := make([]pluginrunner.Snapshot, 0, len(mockMetrics))
	for _, m := range mockMetrics {
		out = append(out, pluginrunner.Snapshot{
			MetricID:    m.id,
			Value:       m.value,
			Timestamp:   mockTimestamp(m.id, now.AddDate(0, 0, -7)),
			FetchedAt:   now,
			Provider:    "mock_positioning",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		})
	}
	return out, nil
}

// GetSnapshotsForWindow emits weekly (COT, Tuesdays) and monthly (margin)
// points inside [start, end], capped at 60 steps.
func (Mock) GetSnapshotsForWindow(_ context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	seen := map[string]bool{}
	out := make([]pluginrunner.Snapshot, 0, maxSamples*len(mockMetrics))
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(24*time.Hour), n+1 {
		for _, m := range mockMetrics {
			ts := mockTimestamp(m.id, d)
			key := m.id + ts.Format("2006-01-02")
			if seen[key] || ts.Before(start) || ts.After(end) {
				continue
			}
			seen[key] = true
			out = append(out, pluginrunner.Snapshot{
				MetricID:    m.id,
				Value:       m.value,
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    "mock_positioning",
				SourceClass: model.SourceClassMock,
				Grade:       "estimated",
			})
		}
	}
	return out, nil
}
