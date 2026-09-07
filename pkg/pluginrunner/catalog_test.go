package pluginrunner

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "sonde/pkg/proto/plugin/v1"
)

func TestLoadRegistration_CapabilitiesAndRules(t *testing.T) {
	reg, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
description: catalog test
capabilities:
  windowedBackfill: true
  maxBackfillDays: 10
  requiresSecrets: [FRED_API_KEY]
  mockAvailable: true
entities:
  - id: US
    name: United States
    namespace: us
    type: market
metrics:
  - id: us.mkt.extra_fixture
    name: Extra
    unit: index
    frequency: daily
    entity: US
rules:
  - name: extra_threshold
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{"operator":"gt","value":1}'
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 10 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	if len(reg.GetMetrics()) != 1 || reg.GetMetrics()[0].GetId() != "us.mkt.extra_fixture" {
		t.Fatalf("metrics=%v", reg.GetMetrics())
	}
	if string(reg.GetRules()[0].GetConfig()) != `{"operator":"gt","value":1}` {
		t.Fatalf("config=%s", reg.GetRules()[0].GetConfig())
	}
	if reg.GetRules()[0].GetDisplayName() != "" {
		t.Fatalf("old catalog without display_name must stay empty, got %q", reg.GetRules()[0].GetDisplayName())
	}
}

func TestLoadRegistration_DisplayNameOptional(t *testing.T) {
	without, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: legacy_slug
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
`))
	if err != nil {
		t.Fatal(err)
	}
	if without.GetRules()[0].GetDisplayName() != "" {
		t.Fatalf("missing display_name = %q, want empty so frontend falls back to name", without.GetRules()[0].GetDisplayName())
	}

	with, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: legacy_slug
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
    display_name: 美联储资产负债表连续 4 周收缩
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := with.GetRules()[0].GetDisplayName(); got != "美联储资产负债表连续 4 周收缩" {
		t.Fatalf("display_name = %q", got)
	}
}

func TestLoadRegistration_Mode(t *testing.T) {
	omitted, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: omitted_mode
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
`))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.GetRules()[0].GetMode() != "" {
		t.Fatalf("omitted mode = %q, want empty so Core treats it as live", omitted.GetRules()[0].GetMode())
	}

	explicitLive, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: live_mode
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
    mode: live
`))
	if err != nil {
		t.Fatal(err)
	}
	if explicitLive.GetRules()[0].GetMode() != "live" {
		t.Fatalf("mode: live = %q, want live", explicitLive.GetRules()[0].GetMode())
	}

	observe, err := LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: observe_mode
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
    mode: observe
`))
	if err != nil {
		t.Fatal(err)
	}
	if observe.GetRules()[0].GetMode() != "observe" {
		t.Fatalf("mode: observe = %q, want observe", observe.GetRules()[0].GetMode())
	}

	_, err = LoadRegistration([]byte(`
name: fixture
version: "1.0.0"
rules:
  - name: bad_mode
    metric: us.mkt.extra_fixture
    detector: threshold
    severity: info
    config: '{}'
    mode: shadow
`))
	if err == nil {
		t.Fatal("invalid mode must fail catalog load")
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("invalid mode error = %v, want unknown mode", err)
	}
}

func TestValidateCapabilities_RejectsMismatch(t *testing.T) {
	req := &pb.RegisterPluginRequest{
		Capabilities: &pb.PluginCapabilities{WindowedBackfill: true},
	}
	if err := ValidateCapabilities(req, stubProvider{}); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestValidateCapabilities_AcceptsWindowed(t *testing.T) {
	req := &pb.RegisterPluginRequest{
		Capabilities: &pb.PluginCapabilities{WindowedBackfill: true},
	}
	if err := ValidateCapabilities(req, stubWindowed{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCapabilities_OldPluginZeroValue(t *testing.T) {
	if err := ValidateCapabilities(&pb.RegisterPluginRequest{}, stubProvider{}); err != nil {
		t.Fatal(err)
	}
}

type stubProvider struct{}

func (stubProvider) GetSnapshots(context.Context) ([]Snapshot, error) { return nil, nil }

type stubWindowed struct{ stubProvider }

func (stubWindowed) GetSnapshotsForWindow(context.Context, time.Time, time.Time) ([]Snapshot, error) {
	return nil, nil
}
