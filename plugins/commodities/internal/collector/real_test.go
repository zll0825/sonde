package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sonde/pkg/pluginrunner"
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

// metal.precious.gold 退役后 RealCollector 只剩 FRED 一条腿。此前的
// collectBoth / 部分失败合流逻辑随 Alpha Vantage 一起删掉了。

func TestRealCollector_ServesFREDLegs(t *testing.T) {
	collector := &RealCollector{
		fred: &stubWindowedProvider{latest: []pluginrunner.Snapshot{
			{MetricID: "oil.energy.wti"},
			{MetricID: "metal.industrial.copper"},
		}},
	}
	snapshots, err := collector.GetSnapshots(context.Background())
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("GetSnapshots() = %+v, %v", snapshots, err)
	}
}

func TestRealCollector_PropagatesFREDFailure(t *testing.T) {
	collector := &RealCollector{
		fred: &stubWindowedProvider{latestErr: errors.New("fred unavailable")},
	}
	if _, err := collector.GetSnapshots(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "fred unavailable") {
		t.Fatalf("GetSnapshots() error = %v", err)
	}
}

func TestNewRealCollector_RequiresFREDKeyOnly(t *testing.T) {
	// 退役黄金后 ALPHAVANTAGE_API_KEY 不再有使用者：即使它是空的，
	// 采集器也必须能建起来——首页的「密钥未就绪」红字就是这么消掉的。
	t.Setenv("ALPHAVANTAGE_API_KEY", "")
	t.Setenv("FRED_API_KEY", "fred-fixture-key")
	if _, err := NewRealCollector([]byte(testFREDBindings)); err != nil {
		t.Fatalf("commodities must start without ALPHAVANTAGE_API_KEY: %v", err)
	}

	t.Setenv("FRED_API_KEY", "")
	if _, err := NewRealCollector([]byte(testFREDBindings)); err == nil ||
		!strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("missing FRED key error = %v", err)
	}
}

func TestRealCollector_RejectsInvalidWindowBeforeProviderCalls(t *testing.T) {
	stub := &stubWindowedProvider{}
	collector := &RealCollector{fred: stub}
	start := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(-time.Hour)); err == nil {
		t.Fatal("reversed window returned nil error")
	}
	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(MaxHistoricalWindow+time.Hour)); err == nil {
		t.Fatal("oversized window returned nil error")
	}
	if stub.windowCalls != 0 {
		t.Fatalf("invalid windows called the provider: fred=%d", stub.windowCalls)
	}
}
