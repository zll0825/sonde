package main

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultCollectionIntervalFitsAlphaVantageFreeTier(t *testing.T) {
	if defaultCollectionInterval != 2*time.Hour {
		t.Fatalf("default interval = %s, want 2h", defaultCollectionInterval)
	}
	if scheduled := (24 * time.Hour) / defaultCollectionInterval; scheduled != 12 {
		t.Fatalf("scheduled spot calls/day = %d, want 12", scheduled)
	}
}

func TestRegistrationDescribesXAUUSDSpotAndPreservesFREDLegs(t *testing.T) {
	registration := buildRegistration()
	if registration.GetInfo().GetVersion() != "0.2.0" || !strings.Contains(registration.GetInfo().GetDescription(), "XAUUSD") {
		t.Fatalf("plugin info = %+v", registration.GetInfo())
	}

	metrics := make(map[string]struct {
		name, description, unit, frequency string
	}, len(registration.GetMetrics()))
	for _, metric := range registration.GetMetrics() {
		metrics[metric.GetId()] = struct {
			name, description, unit, frequency string
		}{metric.GetName(), metric.GetDescription(), metric.GetUnit(), metric.GetFrequency()}
	}
	gold := metrics["metal.precious.gold"]
	if gold.unit != "USD/troy oz" || gold.frequency != "daily" ||
		!strings.Contains(gold.name, "XAUUSD") ||
		!strings.Contains(gold.description, "Alpha Vantage") {
		t.Fatalf("gold registration = %+v", gold)
	}
	wti := metrics["oil.energy.wti"]
	if wti.unit != "USD/bbl" || wti.frequency != "daily" || !strings.Contains(wti.description, "West Texas Intermediate") {
		t.Fatalf("WTI registration changed: %+v", wti)
	}
	copper := metrics["metal.industrial.copper"]
	if copper.unit != "USD/mt" || copper.frequency != "monthly" || !strings.Contains(copper.description, "PCOPPUSDM") {
		t.Fatalf("copper registration changed: %+v", copper)
	}
	if !strings.Contains(registration.GetChangeLog(), "Alpha Vantage XAUUSD") {
		t.Fatalf("change log = %q", registration.GetChangeLog())
	}
}
