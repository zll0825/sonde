package collector

import (
	"context"
	"testing"

	"sonde/pkg/model"
)

func TestMock_GetSnapshots_CoversAllDimensions(t *testing.T) {
	snaps, err := Mock{}.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range snaps {
		if s.SourceClass != model.SourceClassMock {
			t.Errorf("%s source class = %v", s.MetricID, s.SourceClass)
		}
		seen[s.MetricID] = true
	}
	for _, id := range MockMetricIDs() {
		if !seen[id] {
			t.Errorf("mock missing %s", id)
		}
	}
}

func TestMock_WindowBounded(t *testing.T) {
	snaps, _ := Mock{}.GetSnapshotsForWindow(context.Background(), dateUTC(2020, 1, 1), dateUTC(2026, 1, 1))
	if len(snaps) != 60*len(mockMetrics) {
		t.Fatalf("window snaps = %d", len(snaps))
	}
}
