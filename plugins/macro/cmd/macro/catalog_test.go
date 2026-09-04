package main

import (
	"testing"

	"sonde/pkg/pluginrunner"
	fredprov "sonde/pkg/provider/fred"
	macro "sonde/plugins/macro"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "macro" || reg.GetInfo().GetVersion() != "0.2.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	if got := len(reg.GetEntities()); got != 2 {
		t.Fatalf("entities=%d, want 2", got)
	}
	if reg.GetEntities()[0].GetId() != "FED" || reg.GetEntities()[1].GetId() != "US" {
		t.Fatalf("entities=%v", reg.GetEntities())
	}
	wantMetrics := []struct{ id, unit, freq, entity string }{
		{"fed.ins.balance_sheet", "USD", "weekly", "FED"},
		{"us.mkt.ten_year_yield", "%", "daily", "US"},
		{"us.mkt.dollar_index", "index", "daily", "US"},
		{"us.mkt.usd_cny", "CNY per USD", "daily", "US"},
		{"us.mkt.cpi", "index", "monthly", "US"},
		{"us.mkt.inflation_yoy", "%", "monthly", "US"},
	}
	if got := len(reg.GetMetrics()); got != len(wantMetrics) {
		t.Fatalf("metrics=%d, want %d", got, len(wantMetrics))
	}
	for i, want := range wantMetrics {
		m := reg.GetMetrics()[i]
		if m.GetId() != want.id || m.GetUnit() != want.unit || m.GetFrequency() != want.freq || m.GetEntityId() != want.entity {
			t.Errorf("metrics[%d]=%+v, want %+v", i, m, want)
		}
	}
	if len(reg.GetRelations()) != 1 || reg.GetRelations()[0].GetRelationType() != "causes" {
		t.Fatalf("relations=%v", reg.GetRelations())
	}
	wantRules := []struct{ name, metric, detector, config string }{
		{"fed_balance_drop", "fed.ins.balance_sheet", "trend", `{"direction":"down","consecutive":4}`},
		{"yield_spike_percentile", "us.mkt.ten_year_yield", "percentile", `{"percentile":90,"consecutive":2}`},
		{"usd_index_extreme", "us.mkt.dollar_index", "threshold", `{"operator":"gt","value":105}`},
		{"inflation_above_target", "us.mkt.inflation_yoy", "threshold", `{"operator":"gt","value":3.0,"consecutive":2}`},
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
	if bindings[0].SeriesID != "WALCL" || bindings[0].UnitScale != 1e6 {
		t.Errorf("WALCL binding = %+v, want scale 1e6", bindings[0])
	}
	if bindings[5].SeriesID != "CPIAUCSL" || bindings[5].Units != "pc1" {
		t.Errorf("inflation_yoy binding = %+v, want units=pc1", bindings[5])
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
