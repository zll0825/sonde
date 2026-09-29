package provider

import (
	"testing"
	"time"
)

func TestPollInterval_KnownFrequencies(t *testing.T) {
	cases := map[string]time.Duration{
		FrequencyRealtime:  0,
		FrequencyHourly:    time.Hour,
		FrequencyDaily:     6 * time.Hour,
		FrequencyWeekly:    24 * time.Hour,
		FrequencyMonthly:   24 * time.Hour,
		FrequencyQuarterly: 7 * 24 * time.Hour,
	}
	for freq, want := range cases {
		got, known := PollInterval(freq)
		if !known || got != want {
			t.Errorf("PollInterval(%q) = %v, %v; want %v, true", freq, got, known, want)
		}
	}
	if got, known := PollInterval("fortnightly"); known || got != time.Hour {
		t.Errorf("unknown frequency = %v, %v; want the legacy 1h, false", got, known)
	}
}

func TestPollSchedule_DueByFrequency(t *testing.T) {
	var s PollSchedule
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if !s.Due("a", FrequencyDaily, t0) {
		t.Fatal("never-polled key must be due")
	}
	s.MarkPolled("a", t0)
	if s.Due("a", FrequencyDaily, t0.Add(time.Hour)) {
		t.Fatal("daily series must not be due an hour later")
	}
	if !s.Due("a", FrequencyDaily, t0.Add(6*time.Hour-time.Second)) {
		t.Fatal("tick jitter just under the interval must still count as due")
	}
	if !s.Due("a", FrequencyRealtime, t0) {
		t.Fatal("realtime is due on every tick")
	}
	if !s.Due("b", FrequencyMonthly, t0) {
		t.Fatal("keys are tracked independently")
	}
}

func TestPollSchedule_MarkPolledIsIdempotent(t *testing.T) {
	var s PollSchedule
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s.MarkPolled("a", t0)
	s.MarkPolled("a", t0)
	if s.Due("a", FrequencyWeekly, t0.Add(12*time.Hour)) {
		t.Fatal("re-marking must not change the schedule")
	}
	if !s.Due("a", FrequencyWeekly, t0.Add(24*time.Hour)) {
		t.Fatal("weekly series due after a day")
	}
}
