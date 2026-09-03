package main

import (
	"testing"

	fredprov "capital_observatory/pkg/provider/fred"
	commodities "capital_observatory/plugins/commodities"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "commodities" || reg.GetInfo().GetVersion() != "0.2.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	secrets := reg.GetCapabilities().GetRequiresSecrets()
	foundFRED, foundAV := false, false
	for _, s := range secrets {
		if s == "FRED_API_KEY" {
			foundFRED = true
		}
		if s == "ALPHAVANTAGE_API_KEY" {
			foundAV = true
		}
	}
	if !foundFRED || !foundAV {
		t.Fatalf("requiresSecrets=%v", secrets)
	}
	if got := len(reg.GetEntities()); got != 3 {
		t.Fatalf("entities=%d, want 3", got)
	}
	wantMetrics := []string{"oil.energy.wti", "metal.industrial.copper", "metal.precious.gold"}
	if got := len(reg.GetMetrics()); got != len(wantMetrics) {
		t.Fatalf("metrics=%d, want %d", got, len(wantMetrics))
	}
	for i, id := range wantMetrics {
		if reg.GetMetrics()[i].GetId() != id {
			t.Errorf("metrics[%d]=%q, want %q", i, reg.GetMetrics()[i].GetId(), id)
		}
	}
	wantRules := []struct{ name, metric, detector, config string }{
		{"wti_spike_threshold", "oil.energy.wti", "threshold", `{"operator":"gt","value":100}`},
		{"gold_percentile_surge", "metal.precious.gold", "percentile", `{"percentile":90,"consecutive":2}`},
		{"copper_downtrend", "metal.industrial.copper", "trend", `{"direction":"down","consecutive":5}`},
	}
	if got := len(reg.GetRules()); got != len(wantRules) {
		t.Fatalf("rules=%d, want %d", got, len(wantRules))
	}
	for i, want := range wantRules {
		r := reg.GetRules()[i]
		if r.GetName() != want.name || r.GetMetricId() != want.metric || r.GetDetectorName() != want.detector || string(r.GetConfig()) != want.config {
			t.Errorf("rules[%d]=%+v, want %+v", i, r, want)
		}
	}
}

func TestFREDBindingsExcludeGold(t *testing.T) {
	raw, err := commodities.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := fredprov.LoadBindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("fred bindings=%d, want 2 (gold stays on Alpha Vantage)", len(bindings))
	}
	for _, b := range bindings {
		if b.MetricID == "metal.precious.gold" {
			t.Fatal("gold must not be a FRED binding")
		}
	}
}
