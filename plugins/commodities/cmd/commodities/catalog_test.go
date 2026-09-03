package main

import (
	"testing"

	fredprov "capital_observatory/pkg/provider/fred"
	commodities "capital_observatory/plugins/commodities"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "commodities" {
		t.Fatalf("name=%q", reg.GetInfo().GetName())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() {
		t.Fatal("commodities must declare windowedBackfill")
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
