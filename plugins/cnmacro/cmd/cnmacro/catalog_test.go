package main

import (
	"strings"
	"testing"

	"sonde/plugins/cnmacro/internal/collector"
)

func TestBuildRegistrationFromYAML(t *testing.T) {
	reg := buildRegistration()
	if reg.GetInfo().GetName() != "cnmacro" || reg.GetInfo().GetVersion() != "0.1.0" {
		t.Fatalf("info=%+v", reg.GetInfo())
	}
	if !reg.GetCapabilities().GetWindowedBackfill() || reg.GetCapabilities().GetMaxBackfillDays() != 3650 {
		t.Fatalf("capabilities=%+v", reg.GetCapabilities())
	}
	if got := reg.GetCapabilities().GetRequiresSecrets(); len(got) != 0 {
		t.Fatalf("requiresSecrets=%v, want empty", got)
	}
	if !reg.GetCapabilities().GetMockAvailable() {
		t.Fatal("mockAvailable must be true")
	}
	if len(reg.GetEntities()) != 1 || reg.GetEntities()[0].GetId() != "CN" || reg.GetEntities()[0].GetNamespace() != "cn" {
		t.Fatalf("entities=%+v", reg.GetEntities())
	}

	wantMetrics := []struct{ id, unit, freq string }{
		{"cn.mkt.m2_yoy", "%", "monthly"},
		{"cn.mkt.m1_yoy", "%", "monthly"},
		{"cn.mkt.m1_m2_gap", "pp", "monthly"},
		{"cn.mkt.social_financing", "CNY", "monthly"},
		{"cn.mkt.dr007", "%", "daily"},
		{"cn.mkt.omo_7d_rate", "%", "daily"},
		{"cn.mkt.dr007_omo_spread", "bp", "daily"},
		{"cn.mkt.lpr_1y", "%", "monthly"},
		{"cn.mkt.lpr_5y", "%", "monthly"},
	}
	if got := len(reg.GetMetrics()); got != len(wantMetrics) {
		t.Fatalf("metrics=%d, want %d", got, len(wantMetrics))
	}
	for i, want := range wantMetrics {
		m := reg.GetMetrics()[i]
		if m.GetId() != want.id || m.GetUnit() != want.unit || m.GetFrequency() != want.freq || m.GetEntityId() != "CN" {
			t.Errorf("metrics[%d]=%+v, want %+v", i, m, want)
		}
		if m.GetName() == "" || m.GetDescription() == "" {
			t.Errorf("metric %s missing Chinese name/description", m.GetId())
		}
		if !strings.Contains(m.GetDescription(), "数据来源") {
			t.Errorf("metric %s description must name its data source", m.GetId())
		}
	}
	if got := len(reg.GetRules()); got != 0 {
		t.Fatalf("rules=%d, want 0 (observe/collect only)", got)
	}
}

// 口径陷阱必须写进说明，防止研究侧把新旧口径拼接或把报价当成交。
func TestDescriptionsCarryCaveats(t *testing.T) {
	want := map[string][]string{
		"cn.mkt.m1_yoy":           {"2025 年 1 月", "不可"},
		"cn.mkt.m1_m2_gap":        {"百分点", "不可比"},
		"cn.mkt.dr007":            {"市场利率", "中国外汇交易中心"},
		"cn.mkt.lpr_1y":           {"报价利率"},
		"cn.mkt.lpr_5y":           {"报价利率"},
		"cn.mkt.social_financing": {"中国人民银行", "亿元"},
		"cn.mkt.omo_7d_rate":      {"公开市场业务交易公告"},
	}
	for _, m := range buildRegistration().GetMetrics() {
		for _, frag := range want[m.GetId()] {
			if !strings.Contains(m.GetDescription(), frag) {
				t.Errorf("%s description missing %q: %q", m.GetId(), frag, m.GetDescription())
			}
		}
	}
}

// Mock 与 catalog 必须覆盖同一组指标。
func TestMockCoversCatalog(t *testing.T) {
	declared := map[string]bool{}
	for _, m := range buildRegistration().GetMetrics() {
		declared[m.GetId()] = true
	}
	mocked := map[string]bool{}
	for _, id := range collector.MockMetricIDs() {
		mocked[id] = true
		if !declared[id] {
			t.Errorf("mock emits undeclared metric %s", id)
		}
	}
	for id := range declared {
		if !mocked[id] {
			t.Errorf("mock missing declared metric %s", id)
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
