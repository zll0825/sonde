package main

import (
	"strings"
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
		{"usd_index_extreme", "us.mkt.dollar_index", "moving_average", `{"window":20,"margin":1.5,"above":true,"min_observations":25}`},
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
		if r.GetDisplayName() == "" {
			t.Errorf("rule %s missing display_name", r.GetName())
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

// TestDollarIndexIsNotMislabelledAsDXY 锁住一次代价很高的错误：
// us.mkt.dollar_index 绑的是 FRED DTWEXBGS（美联储广义贸易加权，Jan 2006=100，
// 实测 118.7），catalog 却把它写成 "DXY US Dollar Index"。名字一错，后续规则
// 作者照 DXY 的区间（97–110）写了 threshold gt 105，于是永久为真。
func TestDollarIndexIsNotMislabelledAsDXY(t *testing.T) {
	reg := buildRegistration()
	for _, m := range reg.GetMetrics() {
		if m.GetId() != "us.mkt.dollar_index" {
			continue
		}
		if strings.Contains(m.GetName(), "DXY") {
			t.Errorf("name %q claims DXY; the series is DTWEXBGS", m.GetName())
		}
		if !strings.Contains(m.GetName(), "广义贸易加权") {
			t.Errorf("name %q must say 广义贸易加权, not a generic 美元指数", m.GetName())
		}
		if !strings.Contains(m.GetDescription(), "DTWEXBGS") {
			t.Errorf("description must name the actual series: %q", m.GetDescription())
		}
		return
	}
	t.Fatal("us.mkt.dollar_index must stay declared; renaming its id would break uid inheritance")
}

// TestNoRuleUsesAnAbsolutePriceThreshold 锁住常亮规则的成因：对价格水平做
// 静态阈值，在趋势市中一旦越过就永久为真。相对自身分布的形态才有资格留下。
func TestNoRuleUsesAnAbsolutePriceThreshold(t *testing.T) {
	reg := buildRegistration()
	for _, r := range reg.GetRules() {
		if r.GetMetricId() == "us.mkt.dollar_index" && r.GetDetectorName() == "threshold" {
			t.Errorf("rule %s is back on a static threshold: %s", r.GetName(), r.GetConfig())
		}
	}
}

// TestDeclaredDetectorsAreRegisteredInCore 钉住插件侧的一半约束。
//
// Core 在 cmd/core/main.go 的 newDetectorEngine 注册探测器；申报一个不在其中
// 的名字，规则会每轮打 WARN 且永不触发——静默失效，不报错。插件是独立模块，
// 不能导入 internal/core，所以这里复制一份名单；Core 侧有
// TestDetectorRegistrationCoversEveryPluginDetector 钉着同一份，任一侧漂移都会红。
func TestDeclaredDetectorsAreRegisteredInCore(t *testing.T) {
	registered := map[string]bool{
		"threshold": true, "percentile": true, "trend": true,
		"volatility": true, "moving_average": true,
	}
	for _, r := range buildRegistration().GetRules() {
		if !registered[r.GetDetectorName()] {
			t.Errorf("rule %s declares detector %q, which Core does not register; the rule would be inert",
				r.GetName(), r.GetDetectorName())
		}
	}
}
