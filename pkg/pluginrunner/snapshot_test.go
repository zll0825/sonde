package pluginrunner

import (
	"testing"
	"time"
)

func TestSnapshotsToProtoUsesProviderFetchTime(t *testing.T) {
	observedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	fetchedAt := time.Date(2026, 8, 6, 8, 30, 0, 0, time.UTC)

	got := SnapshotsToProto([]Snapshot{{
		MetricID:  "macro.test",
		Timestamp: observedAt,
		FetchedAt: fetchedAt,
	}}, "test")

	if got[0].GetTimestamp() != observedAt.Unix() {
		t.Fatalf("timestamp = %d, want %d", got[0].GetTimestamp(), observedAt.Unix())
	}
	if got[0].GetSourceFetchedAt() != fetchedAt.Unix() {
		t.Fatalf("source_fetched_at = %d, want %d", got[0].GetSourceFetchedAt(), fetchedAt.Unix())
	}
}

func TestSnapshotsToProtoDefaultsMissingFetchTimeToConversionTime(t *testing.T) {
	observedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Now().Unix()

	got := SnapshotsToProto([]Snapshot{{MetricID: "legacy.test", Timestamp: observedAt}}, "test")

	after := time.Now().Unix()
	fetchedAt := got[0].GetSourceFetchedAt()
	if fetchedAt < before || fetchedAt > after {
		t.Fatalf("source_fetched_at = %d, want conversion time in [%d, %d]", fetchedAt, before, after)
	}
	if fetchedAt == observedAt.Unix() {
		t.Fatal("source_fetched_at must not fall back to the observation timestamp")
	}
}
