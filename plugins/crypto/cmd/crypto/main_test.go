package main

import "testing"

func TestBuildRegistrationDeclaresHourlyMetrics(t *testing.T) {
	registration := buildRegistration()
	if len(registration.GetMetrics()) == 0 {
		t.Fatal("registration must declare crypto metrics")
	}
	for _, metric := range registration.GetMetrics() {
		if got := metric.GetFrequency(); got != "hourly" {
			t.Errorf("metric %s frequency = %q, want hourly", metric.GetId(), got)
		}
	}
}
