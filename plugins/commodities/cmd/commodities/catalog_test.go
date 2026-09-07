package main

import (
	"testing"

	fredprov "sonde/pkg/provider/fred"
	commodities "sonde/plugins/commodities"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "commodities" || reg.GetInfo().GetVersion() != "0.2.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}

	// metal.precious.gold 退役后 ALPHAVANTAGE_API_KEY 没有使用者了。它若还留在
	// requiresSecrets，首页就会一直报「密钥未就绪」——这正是本次退役要消掉的红字。
	secrets := reg.GetCapabilities().GetRequiresSecrets()
	foundFRED := false
	for _, s := range secrets {
		if s == "FRED_API_KEY" {
			foundFRED = true
		}
		if s == "ALPHAVANTAGE_API_KEY" {
			t.Errorf("ALPHAVANTAGE_API_KEY has no consumer; it must not be required: %v", secrets)
		}
	}
	if !foundFRED {
		t.Fatalf("requiresSecrets=%v, want FRED_API_KEY", secrets)
	}

	if got := len(reg.GetEntities()); got != 2 {
		t.Fatalf("entities=%d, want 2 (GOLD retired with its metric)", got)
	}
	wantMetrics := []string{"oil.energy.wti", "metal.industrial.copper"}
	if got := len(reg.GetMetrics()); got != len(wantMetrics) {
		t.Fatalf("metrics=%d, want %d", got, len(wantMetrics))
	}
	for i, id := range wantMetrics {
		if reg.GetMetrics()[i].GetId() != id {
			t.Errorf("metrics[%d]=%q, want %q", i, reg.GetMetrics()[i].GetId(), id)
		}
	}
	wantRules := []struct{ name, metric, detector string }{
		{"wti_spike_threshold", "oil.energy.wti", "trend"},
		{"copper_downtrend", "metal.industrial.copper", "trend"},
	}
	if got := len(reg.GetRules()); got != len(wantRules) {
		t.Fatalf("rules=%d, want %d", got, len(wantRules))
	}
	for i, want := range wantRules {
		r := reg.GetRules()[i]
		if r.GetName() != want.name || r.GetMetricId() != want.metric || r.GetDetectorName() != want.detector {
			t.Errorf("rules[%d]=%+v, want %+v", i, r, want)
		}
		if r.GetDisplayName() == "" {
			t.Errorf("rule %s missing display_name", r.GetName())
		}
	}
}

// TestRegistrationHasNoDanglingReferences 锁住退役最容易漏的一环：
// 指标退役了，指向它的规则或关系还留着。这类残留不会被注册对账清掉
// （对账按本次申报做差集，仍在申报的条目不参与），只会静默失效。
func TestRegistrationHasNoDanglingReferences(t *testing.T) {
	reg := buildRegistration()

	entities := map[string]bool{}
	for _, e := range reg.GetEntities() {
		entities[e.GetId()] = true
	}
	metrics := map[string]bool{}
	for _, m := range reg.GetMetrics() {
		if !entities[m.GetEntityId()] {
			t.Errorf("metric %s references undeclared entity %s", m.GetId(), m.GetEntityId())
		}
		metrics[m.GetId()] = true
	}
	for _, r := range reg.GetRules() {
		if !metrics[r.GetMetricId()] {
			t.Errorf("rule %s references undeclared metric %s", r.GetName(), r.GetMetricId())
		}
	}
	for _, rel := range reg.GetRelations() {
		if !entities[rel.GetSourceId()] || !entities[rel.GetTargetId()] {
			t.Errorf("relation %s->%s references an undeclared entity", rel.GetSourceId(), rel.GetTargetId())
		}
	}

	retired := []string{"metal.precious.gold"}
	for _, id := range retired {
		if metrics[id] {
			t.Errorf("retired metric %s should not be declared", id)
		}
	}
	if entities["GOLD"] {
		t.Error("retired entity GOLD should not be declared")
	}
}

func TestFREDBindingsCoverEveryDeclaredMetric(t *testing.T) {
	raw, err := commodities.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := fredprov.LoadBindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 黄金曾是唯一一个不走 FRED 的指标。它退役后，commodities 的每个指标
	// 都由一条 FRED binding 供给，两边数量必须对齐。
	if len(bindings) != len(buildRegistration().GetMetrics()) {
		t.Fatalf("fred bindings=%d, declared metrics=%d; every commodities metric is now FRED-backed",
			len(bindings), len(buildRegistration().GetMetrics()))
	}
	for _, b := range bindings {
		if b.MetricID == "metal.precious.gold" {
			t.Fatal("retired gold must not be a FRED binding")
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
