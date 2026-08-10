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
		"btc.ass.price":      false,
		"btc.ass.hash_rate":  false,
		"btc.ass.tx_count":   false,
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
	retiredIDs := []string{"btc.ass.exchange_balance"}
	for _, metric := range metrics {
		for _, retired := range retiredIDs {
			if metric.GetId() == retired {
				t.Errorf("retired metric %s should not be declared", retired)
			}
		}
	}
}
