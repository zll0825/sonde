package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/pluginrunner"
)

type stubWindowedProvider struct {
	latest      []pluginrunner.Snapshot
	latestErr   error
	history     []pluginrunner.Snapshot
	historyErr  error
	windowCalls int
}

func (s *stubWindowedProvider) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	return s.latest, s.latestErr
}

func (s *stubWindowedProvider) GetSnapshotsForWindow(context.Context, time.Time, time.Time) ([]pluginrunner.Snapshot, error) {
	s.windowCalls++
	return s.history, s.historyErr
}

func TestRealCollector_CombinesFREDAndGoldSnapshots(t *testing.T) {
	collector := &RealCollector{
		fred: &stubWindowedProvider{latest: []pluginrunner.Snapshot{
			{MetricID: "oil.energy.wti"},
			{MetricID: "metal.industrial.copper"},
		}},
		gold: &stubWindowedProvider{latest: []pluginrunner.Snapshot{{MetricID: goldMetricID}}},
	}
	snapshots, err := collector.GetSnapshots(context.Background())
	if err != nil || len(snapshots) != 3 {
		t.Fatalf("GetSnapshots() = %+v, %v", snapshots, err)
	}
}

func TestRealCollector_PreservesFREDSnapshotsWhenGoldFails(t *testing.T) {
	collector := &RealCollector{
		fred: &stubWindowedProvider{latest: []pluginrunner.Snapshot{{MetricID: "oil.energy.wti"}, {MetricID: "metal.industrial.copper"}}},
		gold: &stubWindowedProvider{latestErr: errors.New("spot unavailable")},
	}
	snapshots, err := collector.GetSnapshots(context.Background())
	if len(snapshots) != 2 || err == nil {
		t.Fatalf("GetSnapshots() = %+v, %v", snapshots, err)
	}
	if !strings.Contains(err.Error(), "alpha_vantage/metal.precious.gold") || !strings.Contains(err.Error(), "spot unavailable") {
		t.Fatalf("partial error = %q", err)
	}
}

func TestRealCollector_PreservesGoldWhenFREDFails(t *testing.T) {
	collector := &RealCollector{
		fred: &stubWindowedProvider{historyErr: errors.New("fred unavailable")},
		gold: &stubWindowedProvider{history: []pluginrunner.Snapshot{{MetricID: goldMetricID}}},
	}
	end := time.Now()
	snapshots, err := collector.GetSnapshotsForWindow(context.Background(), end.Add(-24*time.Hour), end)
	if len(snapshots) != 1 || err == nil || !strings.Contains(err.Error(), "fred unavailable") {
		t.Fatalf("GetSnapshotsForWindow() = %+v, %v", snapshots, err)
	}
}

func TestNewRealCollector_RequiresBothCredentials(t *testing.T) {
	t.Setenv("ALPHAVANTAGE_API_KEY", "")
	t.Setenv("FRED_API_KEY", "fred-fixture-key")
	if _, err := NewRealCollector([]byte(testFREDBindings)); err == nil || !strings.Contains(err.Error(), "ALPHAVANTAGE_API_KEY") {
		t.Fatalf("missing Alpha Vantage key error = %v", err)
	}

	t.Setenv("ALPHAVANTAGE_API_KEY", "alpha-fixture-key")
	t.Setenv("FRED_API_KEY", "")
	if _, err := NewRealCollector([]byte(testFREDBindings)); err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("missing FRED key error = %v", err)
	}
}

func TestRealCollector_RejectsInvalidWindowBeforeProviderCalls(t *testing.T) {
	stubs := []*stubWindowedProvider{{}, {}}
	collector := &RealCollector{fred: stubs[0], gold: stubs[1]}
	start := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(-time.Hour)); err == nil {
		t.Fatal("reversed window returned nil error")
	}
	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(MaxHistoricalWindow+time.Hour)); err == nil {
		t.Fatal("oversized window returned nil error")
	}
	if stubs[0].windowCalls != 0 || stubs[1].windowCalls != 0 {
		t.Fatalf("invalid windows called providers: fred=%d gold=%d", stubs[0].windowCalls, stubs[1].windowCalls)
	}
}
