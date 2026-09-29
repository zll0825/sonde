package main

import (
	"strings"
	"testing"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "positioning" || reg.GetInfo().GetVersion() != "0.1.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	if got := reg.GetCapabilities().GetRequiresSecrets(); len(got) != 0 {
		t.Fatalf("requiresSecrets=%v, want empty (CFTC_APP_TOKEN is optional)", got)
	}
	if !reg.GetCapabilities().GetMockAvailable() {
		t.Fatal("mockAvailable must be true")
	}

	wantMetrics := []struct{ id, unit, freq, entity string }{
		{"cot.es.noncomm_net", "contracts", "weekly", "COT"},
		{"cot.zn.noncomm_net", "contracts", "weekly", "COT"},
		{"cot.gc.noncomm_net", "contracts", "weekly", "COT"},
		{"cot.btc.noncomm_net", "contracts", "weekly", "COT"},
		{"us.mkt.margin_debt", "USD", "monthly", "US"},
	}
	if got := len(reg.GetMetrics()); got != len(wantMetrics) {
		t.Fatalf("metrics=%d, want %d", got, len(wantMetrics))
	}
	for i, want := range wantMetrics {
		m := reg.GetMetrics()[i]
		if m.GetId() != want.id || m.GetUnit() != want.unit || m.GetFrequency() != want.freq || m.GetEntityId() != want.entity {
			t.Errorf("metrics[%d]=%+v, want %+v", i, m, want)
		}
		if m.GetName() == "" {
			t.Errorf("metric %s missing Chinese name", m.GetId())
		}
	}
	if got := len(reg.GetRules()); got != 0 {
		t.Fatalf("rules=%d, want 0 (collect only)", got)
	}
}

// COT 描述必须写明周二持仓/周五发布与「非商业≠投机」，FINRA 描述必须署名来源。
func TestDescriptionsCarryCaveatsAndSource(t *testing.T) {
	for _, m := range buildRegistration().GetMetrics() {
		d := m.GetDescription()
		if strings.HasPrefix(m.GetId(), "cot.") {
			for _, must := range []string{"周二", "周五 15:30 ET", "不等于投机者", "CFTC", "cftc_contract_market_code"} {
				if !strings.Contains(d, must) {
					t.Errorf("%s description missing %q", m.GetId(), must)
				}
			}
		}
		if m.GetId() == "us.mkt.margin_debt" && !strings.Contains(d, "FINRA") {
			t.Errorf("margin_debt description must name FINRA: %q", d)
		}
	}
}
