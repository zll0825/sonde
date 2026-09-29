package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
		"btc.ass.price":             false,
		"btc.ass.hash_rate":         false,
		"btc.ass.tx_count":          false,
		"stable.ass.total_supply":   false,
		"btc.ass.etf_net_flow":      false,
		"btc.ass.okx_open_interest": false,
		"btc.ass.okx_funding_rate":  false,
		"btc.ass.dvol":              false,
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

// TestNoTrendRuleFiresOnBareNoise 钉住本插件 trend 规则的抗噪下限。
//
// 背景：btc_hashrate_drop 曾是 trend{down, consecutive 3, tolerance 0}。在
// btc.ass.hash_rate 这条序列上，下跌步占 55%，3 连跌的自然发生率约 16.6%，
// 实测 13.74% 的评估触发、覆盖 36 天里的 30 天——规则测的是采样噪声。
// tolerance 0 意味着「严格小于前值」即算一步，consecutive 太小时等价于抛硬币；
// 二者必须至少有一个够紧。这里只拦明确已知会常亮的组合，不替未来的新规则
// 猜阈值：tolerance 为 0 时要求 consecutive ≥ 4。
func TestNoTrendRuleFiresOnBareNoise(t *testing.T) {
	for _, r := range buildRegistration().GetRules() {
		if r.GetDetectorName() != "trend" {
			continue
		}
		var cfg struct {
			Consecutive int     `json:"consecutive"`
			Tolerance   float64 `json:"tolerance"`
		}
		if err := json.Unmarshal(r.GetConfig(), &cfg); err != nil {
			t.Fatalf("rule %s has unparseable config %s: %v", r.GetName(), r.GetConfig(), err)
		}
		// consecutive 缺省时 detector 用 3，等同于最松的一档。
		if cfg.Consecutive == 0 {
			cfg.Consecutive = 3
		}
		if cfg.Tolerance == 0 && cfg.Consecutive < 4 {
			t.Errorf("rule %s is back on a bare-noise trend (tolerance 0, consecutive %d): %s",
				r.GetName(), cfg.Consecutive, r.GetConfig())
		}
	}
}

// 仓位/衍生品/ETF 流量四项只采集、不挂规则，且描述须写明来源与口径边界。
func TestPositioningMetricsAreCollectOnlyWithCaveats(t *testing.T) {
	reg := buildRegistration()
	must := map[string][]string{
		"btc.ass.etf_net_flow":      {"TFTC", "CC BY 4.0", "SoSoValue", "Farside"},
		"btc.ass.okx_open_interest": {"OKX", "BTC-USDT-SWAP", "不是全市场杠杆", "oiCcy"},
		"btc.ass.okx_funding_rate":  {"OKX", "BTC-USDT-SWAP", "不是全市场杠杆", "8 小时"},
		"btc.ass.dvol":              {"Deribit"},
	}
	found := 0
	for _, m := range reg.GetMetrics() {
		words, ok := must[m.GetId()]
		if !ok {
			continue
		}
		found++
		for _, w := range words {
			if !strings.Contains(m.GetDescription(), w) {
				t.Errorf("%s description missing %q", m.GetId(), w)
			}
		}
	}
	if found != len(must) {
		t.Fatalf("found %d of %d positioning metrics", found, len(must))
	}
	for _, r := range reg.GetRules() {
		if _, ok := must[r.GetMetricId()]; ok {
			t.Errorf("rule %s targets collect-only metric %s", r.GetName(), r.GetMetricId())
		}
	}
}
