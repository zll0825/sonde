package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonde/pkg/pluginrunner"
)

type countingProvider struct {
	calls int
	err   error
}

func (p *countingProvider) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return []pluginrunner.Snapshot{{MetricID: "m", Value: 1, Timestamp: time.Now()}}, nil
}

func (p *countingProvider) GetSnapshotsForWindow(ctx context.Context, _, _ time.Time) ([]pluginrunner.Snapshot, error) {
	return p.GetSnapshots(ctx)
}

func TestRealCollectorPollsDailySourcesByFrequency(t *testing.T) {
	tga, auctions, nyfed, ofr := &countingProvider{}, &countingProvider{}, &countingProvider{}, &countingProvider{err: errors.New("down")}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	offset := time.Duration(0)
	r := &RealCollector{tga: tga, auctions: auctions, nyfed: nyfed, ofr: ofr, now: func() time.Time { return base.Add(offset) }}

	for h := 0; h <= 12; h++ {
		offset = time.Duration(h) * time.Hour
		_, _ = r.GetSnapshots(context.Background())
	}
	// Daily sources: polled at 0h, 6h and 12h over thirteen hourly ticks.
	if tga.calls != 3 || auctions.calls != 3 || nyfed.calls != 3 {
		t.Fatalf("tga=%d auctions=%d nyfed=%d fetches, want 3 each", tga.calls, auctions.calls, nyfed.calls)
	}
	// A failing source is retried on every tick instead of waiting 6h.
	if ofr.calls != 13 {
		t.Fatalf("ofr=%d fetches, want 13 (retry every tick while failing)", ofr.calls)
	}

	if _, err := r.GetSnapshots(pluginrunner.WithForcedCollection(context.Background())); err == nil {
		t.Fatal("forced collect should still surface the failing source")
	}
	if tga.calls != 4 || auctions.calls != 4 || nyfed.calls != 4 {
		t.Fatalf("forced collect must poll every source: tga=%d auctions=%d nyfed=%d", tga.calls, auctions.calls, nyfed.calls)
	}
}
