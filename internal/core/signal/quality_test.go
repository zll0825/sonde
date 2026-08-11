package signal

import (
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

func TestComputeQuality_RealRealtime(t *testing.T) {
	q := ComputeQuality(model.SourceClassReal, "realtime", 0, 1)
	if q < 90 || q > 100 {
		t.Errorf("real+realtime fresh = %v, want 90..100", q)
	}
}

func TestComputeQuality_MockEstimated(t *testing.T) {
	q := ComputeQuality(model.SourceClassMock, "estimated", 48*time.Hour, 1)
	if q > 30 {
		t.Errorf("mock+estimated stale = %v, want <30", q)
	}
}

func TestComputeQuality_TestClass(t *testing.T) {
	q := ComputeQuality(model.SourceClassTest, "delayed", 0, 1)
	if q < 30 || q > 60 {
		t.Errorf("test+delayed = %v, want ~40", q)
	}
}

func TestComputeQuality_FreshnessPenalty(t *testing.T) {
	fresh := ComputeQuality(model.SourceClassReal, "delayed", 0, 1)
	stale := ComputeQuality(model.SourceClassReal, "delayed", 14*24*time.Hour, 1)
	if stale >= fresh {
		t.Errorf("stale (%v) should be lower than fresh (%v)", stale, fresh)
	}
}

func TestComputeQuality_SourceCountBonus(t *testing.T) {
	single := ComputeQuality(model.SourceClassReal, "delayed", 0, 1)
	multi := ComputeQuality(model.SourceClassReal, "delayed", 0, 3)
	if multi <= single {
		t.Errorf("multi-source (%v) should be > single-source (%v)", multi, single)
	}
}

func TestComputeQuality_Bounds(t *testing.T) {
	low := ComputeQuality(model.SourceClassMock, "estimated", 30*24*time.Hour, 0)
	if low < 0 {
		t.Errorf("quality should be >= 0, got %v", low)
	}
	high := ComputeQuality(model.SourceClassReal, "realtime", 0, 10)
	if high > 100 {
		t.Errorf("quality should be <= 100, got %v", high)
	}
}

func TestPriorityScore_SeverityWeight(t *testing.T) {
	q := 80.0
	pInfo := PriorityScore(q, model.SeverityInfo, false)
	pWarn := PriorityScore(q, model.SeverityWarning, false)
	pCrit := PriorityScore(q, model.SeverityCritical, false)

	if pWarn <= pInfo {
		t.Errorf("warning priority should exceed info: warn=%v info=%v", pWarn, pInfo)
	}
	if pCrit <= pWarn {
		t.Errorf("critical priority should exceed warning: crit=%v warn=%v", pCrit, pWarn)
	}
}

func TestPriorityScore_CoalesceBoost(t *testing.T) {
	q := 70.0
	pSingle := PriorityScore(q, model.SeverityWarning, false)
	pCoalesced := PriorityScore(q, model.SeverityWarning, true)

	if pCoalesced <= pSingle {
		t.Errorf("coalesced (%v) should exceed single (%v)", pCoalesced, pSingle)
	}
}

func TestRuleStats_ResolveRate(t *testing.T) {
	rs := NewRuleStats(1, 5, 1*time.Hour)
	now := time.Now()

	rs.RecordTrigger(now.Add(-2 * time.Hour))
	rs.RecordTrigger(now.Add(-90 * time.Minute))
	rs.RecordResolve(now.Add(-80 * time.Minute)) // resolved within window of 2nd trigger

	tp := rs.ResolveRate()
	if tp < 0 || tp > 1 {
		t.Errorf("resolve rate should be 0..1, got %v", tp)
	}
}

func TestRuleStats_FPRate(t *testing.T) {
	rs := NewRuleStats(1, 5, 1*time.Hour)
	now := time.Now()

	// Three triggers, none resolved
	rs.RecordTrigger(now.Add(-30 * time.Minute))
	rs.RecordTrigger(now.Add(-45 * time.Minute))
	rs.RecordTrigger(now.Add(-55 * time.Minute))

	fp := rs.FPRate()
	if fp < 0 || fp > 1 {
		t.Errorf("FP rate should be 0..1, got %v", fp)
	}
}

func TestTracker_RegisterAndGet(t *testing.T) {
	tr := NewTracker()
	tr.Register(1, 1*time.Hour)

	s := tr.Stats(1)
	if s == nil {
		t.Fatal("expected stats for rule 1")
	}
	if s.ruleID != 1 {
		t.Errorf("expected ruleID=1, got %d", s.ruleID)
	}
}

func TestTracker_Unregister(t *testing.T) {
	tr := NewTracker()
	tr.Register(1, 1*time.Hour)
	tr.Unregister(1)

	s := tr.Stats(1)
	if s != nil {
		t.Error("expected nil after unregister")
	}
}
