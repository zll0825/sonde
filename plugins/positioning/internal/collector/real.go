package collector

import (
	"context"
	"time"

	"sonde/pkg/pluginrunner"
)

type windowedProvider interface {
	pluginrunner.Provider
	pluginrunner.WindowedProvider
}

// RealCollector composes CFTC COT and FINRA margin statistics. One source
// failing must not drop the other's snapshots. No secret is required.
type RealCollector struct {
	cftc  windowedProvider
	finra windowedProvider
}

// NewRealCollector wires both public-data collectors.
func NewRealCollector() *RealCollector {
	return &RealCollector{
		cftc:  NewCFTCCollector(),
		finra: NewFINRACollector(),
	}
}

func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	return r.merge(func(p windowedProvider) ([]pluginrunner.Snapshot, error) {
		return p.GetSnapshots(ctx)
	})
}

func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	return r.merge(func(p windowedProvider) ([]pluginrunner.Snapshot, error) {
		return p.GetSnapshotsForWindow(ctx, start, end)
	})
}

func (r *RealCollector) merge(fn func(windowedProvider) ([]pluginrunner.Snapshot, error)) ([]pluginrunner.Snapshot, error) {
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure
	for _, part := range []struct {
		name string
		p    windowedProvider
	}{
		{providerCFTC, r.cftc},
		{providerFINRA, r.finra},
	} {
		got, err := fn(part.p)
		snaps = append(snaps, got...)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: part.name, Err: err})
		}
	}
	if len(snaps) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, pluginrunner.SummarizeCollectionFailures(failures)
}
