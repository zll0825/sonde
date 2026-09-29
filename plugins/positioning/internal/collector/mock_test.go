package collector

import (
	"context"
	"testing"

	"sonde/pkg/model"
)

func TestMockCoversAllRegisteredMetrics(t *testing.T) {
	want := []string{metricMarginDebt}
	for _, k := range cotContracts {
		want = append(want, k.metricID)
	}
	snaps, err := Mock{}.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range snaps {
		got[s.MetricID] = true
		if s.SourceClass != model.SourceClassMock {
			t.Errorf("%s source class = %q, want mock", s.MetricID, s.SourceClass)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("mock metrics = %d, want %d (%v)", len(got), len(want), got)
	}
	for _, id := range want {
		if !got[id] {
			t.Errorf("mock missing %s", id)
		}
	}

	start, end := dateUTC(2026, 8, 1), dateUTC(2026, 9, 10)
	win, err := Mock{}.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range win {
		ids[s.MetricID] = true
		if s.Timestamp.Before(start) || s.Timestamp.After(end) {
			t.Errorf("window timestamp out of range: %s", s.Timestamp)
		}
	}
	for _, id := range want {
		if !ids[id] {
			t.Errorf("mock window missing %s", id)
		}
	}
}
