package main

import (
	"testing"

	"capital_observatory/pkg/pluginrunner"
	fredprov "capital_observatory/pkg/provider/fred"
	macro "capital_observatory/plugins/macro"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "macro" {
		t.Fatalf("name=%q", reg.GetInfo().GetName())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() {
		t.Fatal("macro must declare windowedBackfill")
	}
	if got := len(reg.GetMetrics()); got != 6 {
		t.Fatalf("metrics=%d, want 6", got)
	}
	want := []string{
		"fed.ins.balance_sheet",
		"us.mkt.ten_year_yield",
		"us.mkt.dollar_index",
		"us.mkt.usd_cny",
		"us.mkt.cpi",
		"us.mkt.inflation_yoy",
	}
	for i, id := range want {
		if reg.GetMetrics()[i].GetId() != id {
			t.Errorf("metrics[%d]=%q, want %q", i, reg.GetMetrics()[i].GetId(), id)
		}
	}
	for _, relation := range reg.GetRelations() {
		if relation.GetRelationType() != "causes" {
			t.Errorf("relation type = %q, want causes", relation.GetRelationType())
		}
	}
}

func TestBindingsMatchRegistrationMetrics(t *testing.T) {
	raw, err := macro.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := fredprov.LoadBindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	reg := buildRegistration()
	if len(bindings) != len(reg.GetMetrics()) {
		t.Fatalf("bindings=%d metrics=%d", len(bindings), len(reg.GetMetrics()))
	}
	for i := range bindings {
		if bindings[i].MetricID != reg.GetMetrics()[i].GetId() {
			t.Errorf("binding[%d]=%q metric=%q", i, bindings[i].MetricID, reg.GetMetrics()[i].GetId())
		}
	}
}

func TestExtraManifestMetricNeedsNoGo(t *testing.T) {
	reg, err := pluginrunner.LoadRegistration([]byte(`
name: fixture
version: "0.0.1"
metrics:
  - id: us.mkt.extra_fixture
    name: Extra Fixture
    unit: index
    frequency: daily
    entity: US
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.GetMetrics()) != 1 || reg.GetMetrics()[0].GetId() != "us.mkt.extra_fixture" {
		t.Fatalf("metrics=%v", reg.GetMetrics())
	}
}
