package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sonde/pkg/pluginrunner"
)

type stubProvider struct {
	snaps []pluginrunner.Snapshot
	err   error
}

func (s stubProvider) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	return s.snaps, s.err
}

func (s stubProvider) GetSnapshotsForWindow(context.Context, time.Time, time.Time) ([]pluginrunner.Snapshot, error) {
	return s.snaps, s.err
}

func TestNewRealCollectorNeedsNoSecret(t *testing.T) {
	t.Setenv(envCFTCAppToken, "")
	c := NewRealCollector()
	if c == nil || c.cftc == nil || c.finra == nil {
		t.Fatal("NewRealCollector must compose CFTC and FINRA without any secret")
	}
}

func TestRealCollectorKeepsOtherSourceWhenOneFails(t *testing.T) {
	ok := snap(metricMarginDebt, 1, dateUTC(2026, 8, 1), time.Now(), providerFINRA)
	r := &RealCollector{
		cftc:  stubProvider{err: errors.New("403 throttled")},
		finra: stubProvider{snaps: []pluginrunner.Snapshot{ok}},
	}
	snaps, err := r.GetSnapshots(context.Background())
	if len(snaps) != 1 || snaps[0].MetricID != metricMarginDebt {
		t.Fatalf("snaps = %+v, want FINRA snapshot kept", snaps)
	}
	if err == nil || !strings.Contains(err.Error(), "cftc") {
		t.Fatalf("err = %v, want cftc failure reported", err)
	}

	r.finra = stubProvider{err: errors.New("down")}
	if snaps, err := r.GetSnapshotsForWindow(context.Background(), time.Now(), time.Now()); err == nil || snaps != nil {
		t.Fatalf("all failed: snaps=%v err=%v", snaps, err)
	}
}
