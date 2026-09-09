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

// RealCollector composes FiscalData, NY Fed, and OFR. It must not require
// FRED_API_KEY — a missing FRED key must not fail this plugin.
type RealCollector struct {
	tga   windowedProvider
	nyfed windowedProvider
	ofr   windowedProvider
}

// NewRealCollector wires the three public-data collectors.
func NewRealCollector() *RealCollector {
	return &RealCollector{
		tga:   NewFiscalDataCollector(),
		nyfed: NewNYFedCollector(),
		ofr:   NewOFRCollector(),
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
		{providerFiscal, r.tga},
		{providerNYFed, r.nyfed},
		{providerOFR, r.ofr},
	} {
		got, err := fn(part.p)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: part.name, Err: err})
			if len(got) > 0 {
				snaps = append(snaps, got...)
			}
			continue
		}
		snaps = append(snaps, got...)
	}
	if len(snaps) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	if len(failures) > 0 {
		return snaps, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, nil
}
