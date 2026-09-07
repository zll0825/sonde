package main

import "testing"

// TestBuildRegistrationDeclaresMetrics verifies that the registration declares
// all expected real-source metrics with correct frequencies.
func TestBuildRegistrationDeclaresMetrics(t *testing.T) {
	registration := buildRegistration()
	metrics := registration.GetMetrics()
	if len(metrics) == 0 {
		t.Fatal("registration must declare crypto metrics")
	}

	// All metrics must have a frequency set
	for _, metric := range metrics {
		if metric.GetFrequency() == "" {
			t.Errorf("metric %s frequency must be set", metric.GetId())
		}
	}

	// Verify expected metrics are present
	expectedIDs := map[string]bool{
		"btc.ass.price":     false,
		"btc.ass.hash_rate": false,
		"btc.ass.tx_count":  false,
	}
	for _, metric := range metrics {
		if _, ok := expectedIDs[metric.GetId()]; ok {
			expectedIDs[metric.GetId()] = true
		}
	}
	for id, found := range expectedIDs {
		if !found {
			t.Errorf("registration missing expected metric: %s", id)
		}
	}

	// Verify retired metrics are absent
	retiredIDs := []string{"btc.ass.exchange_balance", "btc.ass.flow_proxy"}
	for _, metric := range metrics {
		for _, retired := range retiredIDs {
			if metric.GetId() == retired {
				t.Errorf("retired metric %s should not be declared", retired)
			}
		}
	}

	// 规则也必须一并退役：留下一条指向已退役指标的规则，注册对账关不掉它
	// （对账按插件申报的三元组做差集，仍在申报的规则不会被退役），
	// 它会每轮找不到观测。
	declared := map[string]bool{}
	for _, metric := range metrics {
		declared[metric.GetId()] = true
	}
	for _, rule := range registration.GetRules() {
		if !declared[rule.GetMetricId()] {
			t.Errorf("rule %s targets undeclared metric %s", rule.GetName(), rule.GetMetricId())
		}
		if rule.GetDisplayName() == "" {
			t.Errorf("rule %s missing display_name", rule.GetName())
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
