package main

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultCollectionInterval(t *testing.T) {
	// 2h 这个值原本是为 Alpha Vantage 免费档 25 次/天定的（12 次/天留有余量）。
	// 黄金退役后只剩 FRED，配额压力消失，但间隔保持不变——改采集节奏不属于
	// 本次退役的范围，也没有触发它的理由。
	if defaultCollectionInterval != 2*time.Hour {
		t.Fatalf("default interval = %s, want 2h", defaultCollectionInterval)
	}
	if scheduled := (24 * time.Hour) / defaultCollectionInterval; scheduled != 12 {
		t.Fatalf("scheduled calls/day = %d, want 12", scheduled)
	}
}

func TestRegistrationPreservesFREDLegs(t *testing.T) {
	registration := buildRegistration()
	if registration.GetInfo().GetVersion() != "0.2.0" {
		t.Fatalf("plugin info = %+v", registration.GetInfo())
	}
	if strings.Contains(registration.GetInfo().GetDescription(), "XAUUSD") {
		t.Fatalf("description still advertises retired gold: %q", registration.GetInfo().GetDescription())
	}

	metrics := make(map[string]struct {
		name, description, unit, frequency string
	}, len(registration.GetMetrics()))
	for _, metric := range registration.GetMetrics() {
		metrics[metric.GetId()] = struct {
			name, description, unit, frequency string
		}{metric.GetName(), metric.GetDescription(), metric.GetUnit(), metric.GetFrequency()}
	}

	// 退役黄金不得动到另外两条腿的测量对象、单位和频率。显示名中文化不改这些。
	wti := metrics["oil.energy.wti"]
	if wti.unit != "USD/bbl" || wti.frequency != "daily" || !strings.Contains(wti.name, "WTI") {
		t.Fatalf("WTI registration changed: %+v", wti)
	}
	copper := metrics["metal.industrial.copper"]
	if copper.unit != "USD/mt" || copper.frequency != "monthly" || !strings.Contains(copper.description, "PCOPPUSDM") {
		t.Fatalf("copper registration changed: %+v", copper)
	}

	if !strings.Contains(registration.GetChangeLog(), "metal.precious.gold") {
		t.Fatalf("change log must record the retirement: %q", registration.GetChangeLog())
	}
}
