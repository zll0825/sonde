package main

import (
	"strings"
	"testing"

	fedops "sonde/plugins/fedops"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "fedops" || reg.GetInfo().GetVersion() != "0.2.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	if got := reg.GetCapabilities().GetRequiresSecrets(); len(got) != 0 {
		t.Fatalf("requiresSecrets=%v, want empty (no FRED key)", got)
	}
	if !reg.GetCapabilities().GetMockAvailable() {
		t.Fatal("mockAvailable must be true")
	}

	wantMetrics := []struct{ id, unit, freq, entity string }{
		{"fed.ins.tga_close", "USD", "daily", "FED"},
		{"us.mkt.treasury_settlement", "USD", "daily", "US"},
		{"fed.ins.rrp", "USD", "daily", "FED"},
		{"fed.ins.srf_usage", "USD", "daily", "FED"},
		{"us.mkt.sofr", "%", "daily", "US"},
		{"us.mkt.sofr_p99", "%", "daily", "US"},
		{"us.mkt.sofr_tail", "bp", "daily", "US"},
		{"us.mkt.ofr_fsi", "index", "daily", "US"},
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
		t.Fatalf("rules=%d, want 0", got)
	}
}

func TestCatalogOmitsDerivedMetrics(t *testing.T) {
	raw, err := fedops.CatalogFS.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, banned := range []string{"sofr_iorb_spread", "net_liquidity"} {
		if strings.Contains(text, banned) {
			t.Errorf("catalog must not register %s", banned)
		}
	}
	for _, m := range buildRegistration().GetMetrics() {
		switch m.GetId() {
		case "us.mkt.sofr_iorb_spread", "fed.ins.net_liquidity":
			t.Errorf("derived metric %s must not be registered", m.GetId())
		}
	}
}

func TestSOFRDescriptionNamesNYFed(t *testing.T) {
	for _, m := range buildRegistration().GetMetrics() {
		switch m.GetId() {
		case "us.mkt.sofr", "us.mkt.sofr_p99", "us.mkt.sofr_tail":
			if !strings.Contains(m.GetDescription(), "纽约联邦储备银行") {
				t.Errorf("%s description must name NY Fed: %q", m.GetId(), m.GetDescription())
			}
		}
	}
}

func TestDeclaredDetectorsAreRegisteredInCore(t *testing.T) {
	registered := map[string]bool{
		"threshold": true, "percentile": true, "trend": true,
		"volatility": true, "moving_average": true,
	}
	for _, r := range buildRegistration().GetRules() {
		if !registered[r.GetDetectorName()] {
			t.Errorf("rule %s declares detector %q, which Core does not register",
				r.GetName(), r.GetDetectorName())
		}
	}
}
