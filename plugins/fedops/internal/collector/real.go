package collector

import (
	"context"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

// sourceFrequency is the publication frequency shared by every fedops
// source (see manifest.yaml): TGA, auctions, RRP/SRF, SOFR and the OFR index are
// all daily, so none of them needs polling on every hourly tick.
const sourceFrequency = provider.FrequencyDaily

type windowedProvider interface {
	pluginrunner.Provider
	pluginrunner.WindowedProvider
}

// RealCollector composes FiscalData (TGA, auctions), NY Fed, and OFR. It must not require
// FRED_API_KEY — a missing FRED key must not fail this plugin.
type RealCollector struct {
	tga      windowedProvider
	auctions windowedProvider
	nyfed    windowedProvider
	ofr      windowedProvider

	schedule provider.PollSchedule
	now      func() time.Time
}

// NewRealCollector wires the public-data collectors.
func NewRealCollector() *RealCollector {
	return &RealCollector{
		tga:      NewFiscalDataCollector(),
		auctions: NewAuctionsCollector(),
		nyfed:    NewNYFedCollector(),
		ofr:      NewOFRCollector(),
		now:      time.Now,
	}
}

// GetSnapshots polls each source only when its publication frequency makes
// it due; a Sync command (forced collection) polls all of them.
func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := r.now()
	forced := pluginrunner.IsForcedCollection(ctx)
	return r.merge(func(name string, p windowedProvider) ([]pluginrunner.Snapshot, error) {
		if !forced && !r.schedule.Due(name, sourceFrequency, now) {
			return nil, nil
		}
		snaps, err := p.GetSnapshots(ctx)
		if err == nil {
			r.schedule.MarkPolled(name, now)
		}
		return snaps, err
	})
}

func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	return r.merge(func(_ string, p windowedProvider) ([]pluginrunner.Snapshot, error) {
		return p.GetSnapshotsForWindow(ctx, start, end)
	})
}

func (r *RealCollector) merge(fn func(string, windowedProvider) ([]pluginrunner.Snapshot, error)) ([]pluginrunner.Snapshot, error) {
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure
	for _, part := range []struct {
		name string
		p    windowedProvider
	}{
		{providerFiscal, r.tga},
		{providerFiscal + "_auctions", r.auctions},
		{providerNYFed, r.nyfed},
		{providerOFR, r.ofr},
	} {
		got, err := fn(part.name, part.p)
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
