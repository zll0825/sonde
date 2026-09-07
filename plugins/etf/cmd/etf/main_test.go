package main

import "testing"

// TestBuildRegistrationDeclaresMetrics 锁定 etf catalog 的申报面。
// catalog 是退役的唯一真源——注册对账把「不再申报」翻译成 effective_to，
// 所以一个指标悄悄回到这里，就等于悄悄复活。
func TestBuildRegistrationDeclaresMetrics(t *testing.T) {
	registration := buildRegistration()
	metrics := registration.GetMetrics()
	if len(metrics) == 0 {
		t.Fatal("registration must declare etf metrics")
	}

	for _, metric := range metrics {
		if metric.GetFrequency() == "" {
			t.Errorf("metric %s frequency must be set", metric.GetId())
		}
	}

	expectedIDs := map[string]bool{
		"gld.ass.price":  false,
		"gld.ass.volume": false,
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

	retiredIDs := []string{"gld.ass.daily_flow", "eth.ass.daily_flow", "gld.ass.flow_proxy"}
	for _, metric := range metrics {
		for _, retired := range retiredIDs {
			if metric.GetId() == retired {
				t.Errorf("retired metric %s should not be declared", retired)
			}
		}
	}

	// 规则不得指向未申报的指标：这样的规则不会被对账退役（它仍在申报里），
	// 只会每轮找不到观测。
	declared := map[string]bool{}
	for _, metric := range metrics {
		declared[metric.GetId()] = true
	}
	for _, rule := range registration.GetRules() {
		if !declared[rule.GetMetricId()] {
			t.Errorf("rule %s targets undeclared metric %s", rule.GetName(), rule.GetMetricId())
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
