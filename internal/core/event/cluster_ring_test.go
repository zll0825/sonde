package event

import (
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

func TestClusterRing_BasicPushAndRecent(t *testing.T) {
	r := NewClusterRing(3)

	a := ClusterSnapshot{ClusterID: "evt:1", PrimaryEntity: "SPY", MemberCount: 1, Severity: model.SeverityWarning, LastTriggered: time.Now(), Coalesced: false}
	b := ClusterSnapshot{ClusterID: "evt:2", PrimaryEntity: "QQQ", MemberCount: 2, Severity: model.SeverityCritical, LastTriggered: time.Now(), Coalesced: true}
	c := ClusterSnapshot{ClusterID: "evt:3", PrimaryEntity: "GLD", MemberCount: 1, Severity: model.SeverityInfo, LastTriggered: time.Now(), Coalesced: false}

	r.Push(a)
	r.Push(b)

	got := r.Recent(10)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	// Most recent first: b, then a.
	if got[0].ClusterID != "evt:2" || got[1].ClusterID != "evt:1" {
		t.Fatalf("unexpected order: got %v, %v", got[0].ClusterID, got[1].ClusterID)
	}

	// Push a third — all three still fit.
	r.Push(c)
	got = r.Recent(3)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0].ClusterID != "evt:3" {
		t.Fatalf("expected evt:3 as most recent, got %s", got[0].ClusterID)
	}
}

func TestClusterRing_Overwrite(t *testing.T) {
	r := NewClusterRing(2)

	r.Push(ClusterSnapshot{ClusterID: "old"})
	r.Push(ClusterSnapshot{ClusterID: "mid"})
	// Overwrite "old"
	r.Push(ClusterSnapshot{ClusterID: "new"})

	got := r.Recent(10)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after overwrite, got %d", len(got))
	}
	// new, mid (old overwritten)
	if got[0].ClusterID != "new" || got[1].ClusterID != "mid" {
		t.Fatalf("expected [new, mid], got [%s, %s]", got[0].ClusterID, got[1].ClusterID)
	}
}

func TestClusterRing_ZeroSize(t *testing.T) {
	r := NewClusterRing(0)
	r.Push(ClusterSnapshot{ClusterID: "only"})
	got := r.Recent(5)
	if len(got) != 1 || got[0].ClusterID != "only" {
		t.Fatalf("expected ring of capacity 1 with 'only', got %+v", got)
	}
	// Overwrite the single slot.
	r.Push(ClusterSnapshot{ClusterID: "replaced"})
	got = r.Recent(5)
	if len(got) != 1 || got[0].ClusterID != "replaced" {
		t.Fatalf("expected [replaced], got %+v", got)
	}
}

func TestClusterRing_NilRecent(t *testing.T) {
	r := NewClusterRing(3)
	got := r.Recent(5)
	if got == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestClusterRing_NLargerThanCount(t *testing.T) {
	r := NewClusterRing(5)
	r.Push(ClusterSnapshot{ClusterID: "a"})
	r.Push(ClusterSnapshot{ClusterID: "b"})

	got := r.Recent(100)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].ClusterID != "b" || got[1].ClusterID != "a" {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestNewClusterSnapshot(t *testing.T) {
	now := time.Now()
	s := newClusterSnapshot("evt:x", "SPY", 3, model.SeverityWarning, now, true)
	if s.ClusterID != "evt:x" {
		t.Fatalf("ClusterID: got %s", s.ClusterID)
	}
	if s.PrimaryEntity != "SPY" {
		t.Fatalf("PrimaryEntity: got %s", s.PrimaryEntity)
	}
	if s.MemberCount != 3 {
		t.Fatalf("MemberCount: got %d", s.MemberCount)
	}
	if s.Severity != model.SeverityWarning {
		t.Fatalf("Severity: got %s", s.Severity)
	}
	if !s.Coalesced {
		t.Fatalf("Coalesced: expected true")
	}
	if !s.LastTriggered.Equal(now) {
		t.Fatalf("LastTriggered mismatch")
	}
}

func TestClusterRing_ByID(t *testing.T) {
	r := NewClusterRing(5)

	r.PushByID(ClusterSnapshot{ClusterID: "c1", PrimaryEntity: "A"})
	r.PushByID(ClusterSnapshot{ClusterID: "c2", PrimaryEntity: "B"})

	okSnap, found := r.ByID("c1")
	if !found {
		t.Fatal("expected to find c1")
	}
	if okSnap.PrimaryEntity != "A" {
		t.Fatalf("c1 PrimaryEntity: got %s want A", okSnap.PrimaryEntity)
	}

	_, found = r.ByID("missing")
	if found {
		t.Fatal("expected not to find 'missing'")
	}
}

func TestClusterRing_PushByID_Dedupe(t *testing.T) {
	r := NewClusterRing(5)
	oldTrail := &MergeTrail{Entries: []MergeAudit{{AlertID: "a1", Reason: "created"}}}
	newTrail := &MergeTrail{Entries: []MergeAudit{{AlertID: "a2", Reason: "same_entity", Coalesced: true}}}

	// First push creates.
	r.PushByID(ClusterSnapshot{ClusterID: "c1", MemberCount: 1, Severity: model.SeverityInfo,
		LastTriggered: time.Now(), MergedAlertIDs: []string{"a1"}, Priority: 12, MergeTrail: oldTrail})
	// Same ClusterID — should update in place (not append a second).
	r.PushByID(ClusterSnapshot{ClusterID: "c1", MemberCount: 2, Severity: model.SeverityWarning,
		LastTriggered: time.Now(), MergedAlertIDs: []string{"a2"}, Priority: 87, MergeTrail: newTrail})

	got := r.Recent(10)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after dedupe, got %d", len(got))
	}
	if got[0].MemberCount != 2 {
		t.Fatalf("expected MemberCount=2 (updated), got %d", got[0].MemberCount)
	}
	if got[0].Severity != model.SeverityWarning {
		t.Fatalf("expected severity warning after update, got %s", got[0].Severity)
	}
	// MergedAlertIDs should contain both a1 and a2 (deduplicated).
	if len(got[0].MergedAlertIDs) != 2 {
		t.Fatalf("expected 2 merged alert IDs, got %v", got[0].MergedAlertIDs)
	}
	if got[0].Priority != 87 {
		t.Fatalf("expected updated priority 87, got %v", got[0].Priority)
	}
	if got[0].MergeTrail != newTrail {
		t.Fatalf("expected latest merge trail, got %#v", got[0].MergeTrail)
	}
}
