package pluginrunner

import (
	"testing"
	"time"

	"sonde/pkg/model"
	pb "sonde/pkg/proto/plugin/v1"
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

func TestSnapshotsToProtoCarriesExplicitSourceClass(t *testing.T) {
	cases := []struct {
		name  string
		class model.SourceClass
		want  pb.SourceClass
	}{
		{"real", model.SourceClassReal, pb.SourceClass_SOURCE_CLASS_REAL},
		{"mock", model.SourceClassMock, pb.SourceClass_SOURCE_CLASS_MOCK},
		{"test", model.SourceClassTest, pb.SourceClass_SOURCE_CLASS_TEST},
		{"missing", "", pb.SourceClass_SOURCE_CLASS_UNSPECIFIED},
		{"invalid", "synthetic", pb.SourceClass_SOURCE_CLASS_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SnapshotsToProto([]Snapshot{{MetricID: "test.metric", Timestamp: time.Now(), SourceClass: tc.class}}, "test")
			if got[0].GetSourceClass() != tc.want {
				t.Fatalf("source_class = %v, want %v", got[0].GetSourceClass(), tc.want)
			}
		})
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
