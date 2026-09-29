package collector

import (
	"context"
	"sort"
	"time"

	"sonde/pkg/pluginrunner"
)

const (
	metricDR007OMOSpread = "cn.mkt.dr007_omo_spread"
	providerSpread       = "cfets_pbc"
	// The OMO rate is carried forward to later DR007 dates (the policy rate
	// stands between operations) but never further than this.
	omoMaxCarry = 31 * 24 * time.Hour
)

type windowedProvider interface {
	pluginrunner.Provider
	pluginrunner.WindowedProvider
}

// RealCollector composes East Money (M1/M2, LPR), CFETS (DR007), and the
// PBoC site (AFRE, OMO). No secrets. One failing source never drops the
// others' snapshots.
type RealCollector struct {
	eastmoney windowedProvider
	cfets     windowedProvider
	afre      windowedProvider
	omo       windowedProvider
}

// NewRealCollector wires the public-data collectors; both PBoC collectors
// share one rate-limited, cookie-carrying client.
func NewRealCollector() *RealCollector {
	pbc := NewPBCHTTPClient()
	return &RealCollector{
		eastmoney: NewEastMoneyCollector(),
		cfets:     NewCFETSCollector(),
		afre:      NewAFRECollector(pbc),
		omo:       NewOMOCollector(pbc),
	}
}

type part struct {
	name string
	p    windowedProvider
}

func (r *RealCollector) parts() []part {
	return []part{
		{providerEastMoney, r.eastmoney},
		{providerCFETS, r.cfets},
		{providerPBC + "_afre", r.afre},
		{providerPBC + "_omo", r.omo},
	}
}

func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	snaps, failures := r.collect(func(p part) ([]pluginrunner.Snapshot, error) {
		return p.p.GetSnapshots(ctx)
	})
	snaps = append(snaps, deriveDR007OMOSpread(snaps)...)
	return finish(snaps, failures, true)
}

// GetSnapshotsForWindow backfills each source as far as it reaches. OMO is
// read from omoMaxCarry before start so the first in-window DR007 dates can
// be joined; those lead-in OMO rows are then dropped.
func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	snaps, failures := r.collect(func(p part) ([]pluginrunner.Snapshot, error) {
		if p.p == r.omo {
			return p.p.GetSnapshotsForWindow(ctx, start.Add(-omoMaxCarry), end)
		}
		return p.p.GetSnapshotsForWindow(ctx, start, end)
	})
	snaps = append(snaps, deriveDR007OMOSpread(snaps)...)
	out := snaps[:0]
	for _, s := range snaps {
		if s.MetricID == metricOMO7D && !inWindow(s.Timestamp, start, end) {
			continue
		}
		out = append(out, s)
	}
	return finish(out, failures, false)
}

func (r *RealCollector) collect(fn func(part) ([]pluginrunner.Snapshot, error)) ([]pluginrunner.Snapshot, []pluginrunner.CollectionFailure) {
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure
	for _, p := range r.parts() {
		got, err := fn(p)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: p.name, Err: err})
		}
		snaps = append(snaps, got...)
	}
	return snaps, failures
}

// deriveDR007OMOSpread computes DR007 − OMO 7d rate (bp) per DR007 date
// using an as-of join: the latest OMO announcement dated on or before that
// date, no older than omoMaxCarry. Emitted only when both inputs exist; a
// preliminary DR007 yields a preliminary spread.
func deriveDR007OMOSpread(snaps []pluginrunner.Snapshot) []pluginrunner.Snapshot {
	var omo []pluginrunner.Snapshot
	for _, s := range snaps {
		if s.MetricID == metricOMO7D {
			omo = append(omo, s)
		}
	}
	if len(omo) == 0 {
		return nil
	}
	sort.Slice(omo, func(i, j int) bool { return omo[i].Timestamp.Before(omo[j].Timestamp) })
	var out []pluginrunner.Snapshot
	for _, dr := range snaps {
		if dr.MetricID != metricDR007 {
			continue
		}
		// last OMO with Timestamp <= dr.Timestamp
		i := sort.Search(len(omo), func(k int) bool { return omo[k].Timestamp.After(dr.Timestamp) }) - 1
		if i < 0 || dr.Timestamp.Sub(omo[i].Timestamp) > omoMaxCarry {
			continue
		}
		grade := "delayed"
		if dr.Grade == "preliminary" {
			grade = "preliminary"
		}
		fetchedAt := dr.FetchedAt
		if omo[i].FetchedAt.After(fetchedAt) {
			fetchedAt = omo[i].FetchedAt
		}
		spread := roundTo((dr.Value-omo[i].Value)*100, 4)
		out = append(out, snap(metricDR007OMOSpread, spread, dr.Timestamp, fetchedAt, providerSpread, grade))
	}
	return out
}
