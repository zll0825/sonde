package ontology

import (
	"encoding/json"
	"testing"

	pb "sonde/pkg/proto/plugin/v1"
)

func TestMarshalPluginCapabilities(t *testing.T) {
	got, err := marshalPluginCapabilities(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("nil caps = %s, want {}", got)
	}

	raw, err := marshalPluginCapabilities(&pb.PluginCapabilities{
		WindowedBackfill: true,
		MaxBackfillDays:  3650,
		RequiresSecrets:  []string{"FRED_API_KEY"},
		MockAvailable:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["windowed_backfill"] != true {
		t.Fatalf("windowed_backfill = %v", parsed["windowed_backfill"])
	}
	if parsed["max_backfill_days"] != float64(3650) {
		t.Fatalf("max_backfill_days = %v", parsed["max_backfill_days"])
	}
	if parsed["mock_available"] != true {
		t.Fatalf("mock_available = %v", parsed["mock_available"])
	}
}
