package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonde/pkg/pluginrunner"
)

type stubProvider struct {
	snaps []pluginrunner.Snapshot
	err   error
	calls []time.Time
}

func (s *stubProvider) GetSnapshots(context.Context) ([]pluginrunner.Snapshot, error) {
	return s.snaps, s.err
}

func (s *stubProvider) GetSnapshotsForWindow(_ context.Context, start, _ time.Time) ([]pluginrunner.Snapshot, error) {
	s.calls = append(s.calls, start)
	return s.snaps, s.err
}

func drSnap(d time.Time, v float64, grade string) pluginrunner.Snapshot {
	return snap(metricDR007, v, d, d, providerCFETS, grade)
}

func omoSnap(d time.Time, v float64) pluginrunner.Snapshot {
	return snap(metricOMO7D, v, d, d, providerPBC, "delayed")
}

func TestNewRealCollectorNeedsNoSecrets(t *testing.T) {
	c := NewRealCollector()
	if c.eastmoney == nil || c.cfets == nil || c.afre == nil || c.omo == nil {
		t.Fatal("NewRealCollector must compose four collectors without any key")
	}
}

func TestSpreadAsOfJoin(t *testing.T) {
	snaps := []pluginrunner.Snapshot{
		omoSnap(dateUTC(2026, 9, 24), 1.40),
		omoSnap(dateUTC(2026, 9, 28), 1.30),             // hypothetical cut
		drSnap(dateUTC(2026, 9, 23), 1.3841, "delayed"), // before any OMO → no spread
		drSnap(dateUTC(2026, 9, 24), 1.3946, "delayed"), // same-day OMO 1.40
		drSnap(dateUTC(2026, 9, 25), 1.3900, "delayed"), // carried from 09-24
		drSnap(dateUTC(2026, 9, 29), 1.3863, "preliminary"),
	}
	got := map[time.Time]pluginrunner.Snapshot{}
	for _, s := range deriveDR007OMOSpread(snaps) {
		got[s.Timestamp] = s
	}
	if _, ok := got[dateUTC(2026, 9, 23)]; ok {
		t.Fatal("no OMO on/before 09-23 → no spread")
	}
	want := map[time.Time]float64{
		dateUTC(2026, 9, 24): -0.54,
		dateUTC(2026, 9, 25): -1.0,
		dateUTC(2026, 9, 29): 8.63, // vs 09-28's 1.30, not a later rate
	}
	for d, v := range want {
		s, ok := got[d]
		if !ok || s.Value != v || s.MetricID != metricDR007OMOSpread || s.Provider != providerSpread {
			t.Errorf("%v: %+v, want %v bp", d.Format("2006-01-02"), s, v)
		}
	}
	if got[dateUTC(2026, 9, 29)].Grade != "preliminary" {
		t.Error("spread from preliminary DR007 must be preliminary")
	}
}

func TestSpreadDoesNotCarryStaleOMO(t *testing.T) {
	snaps := []pluginrunner.Snapshot{
		omoSnap(dateUTC(2026, 7, 1), 1.40),
		drSnap(dateUTC(2026, 9, 1), 1.39, "delayed"),
	}
	if got := deriveDR007OMOSpread(snaps); len(got) != 0 {
		t.Fatalf("OMO older than %v must not be carried: %+v", omoMaxCarry, got)
	}
}

func TestRealMergeKeepsSiblingsOnFailure(t *testing.T) {
	em := &stubProvider{snaps: []pluginrunner.Snapshot{snap(metricM2Yoy, 7.5, dateUTC(2026, 8, 1), time.Now(), providerEastMoney, "delayed")}}
	cf := &stubProvider{snaps: []pluginrunner.Snapshot{drSnap(dateUTC(2026, 9, 28), 1.3911, "delayed")}}
	afre := &stubProvider{err: errors.New("pbc down")}
	omo := &stubProvider{snaps: []pluginrunner.Snapshot{omoSnap(dateUTC(2026, 9, 28), 1.40)}}
	r := &RealCollector{eastmoney: em, cfets: cf, afre: afre, omo: omo}
	snaps, err := r.GetSnapshots(context.Background())
	if err == nil {
		t.Fatal("want summarized partial error")
	}
	got := byMetric(snaps)
	for _, id := range []string{metricM2Yoy, metricDR007, metricOMO7D, metricDR007OMOSpread} {
		if len(got[id]) != 1 {
			t.Errorf("%s: %d snapshots, want 1", id, len(got[id]))
		}
	}
}

func TestRealWindowLeadsOMOAndTrimsIt(t *testing.T) {
	start, end := dateUTC(2026, 9, 1), dateUTC(2026, 9, 30)
	cf := &stubProvider{snaps: []pluginrunner.Snapshot{drSnap(dateUTC(2026, 9, 1), 1.45, "delayed")}}
	omo := &stubProvider{snaps: []pluginrunner.Snapshot{omoSnap(dateUTC(2026, 8, 29), 1.40)}}
	r := &RealCollector{eastmoney: &stubProvider{}, cfets: cf, afre: &stubProvider{}, omo: omo}
	snaps, err := r.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(omo.calls) != 1 || !omo.calls[0].Equal(start.Add(-omoMaxCarry)) {
		t.Fatalf("omo window start = %v, want start-%v", omo.calls, omoMaxCarry)
	}
	got := byMetric(snaps)
	if len(got[metricOMO7D]) != 0 {
		t.Fatal("lead-in OMO before window start must be trimmed")
	}
	if len(got[metricDR007OMOSpread]) != 1 || got[metricDR007OMOSpread][0].Value != 5 {
		t.Fatalf("spread = %+v, want 5 bp", got[metricDR007OMOSpread])
	}
}
