package noise

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryBudget_TracksEmissions(t *testing.T) {
	ctx := context.Background()
	b := NewInMemoryBudget(10)
	now := time.Now()
	b.startedAt = now.Add(-25 * time.Hour) // full 24h window: projected == count

	// 3 emissions from rule 1, 1 emission from rule 2.
	b.Record(ctx, 1, "rule1", now.Add(-1*time.Hour))
	b.Record(ctx, 1, "rule1", now.Add(-2*time.Hour))
	b.Record(ctx, 1, "rule1", now.Add(-3*time.Hour))
	b.Record(ctx, 2, "rule2", now.Add(-30*time.Minute))

	s := b.Status(ctx, now)
	if s.Emissions24h != 4 {
		t.Errorf("Emissions24h = %d, want 4", s.Emissions24h)
	}
	if s.OverBudget {
		t.Error("4 emissions out of a 10/day budget should NOT be over budget")
	}
	if s.Remaining != 6 {
		t.Errorf("Remaining = %d, want 6", s.Remaining)
	}
	// Per-rule counts.
	if len(s.Rules) != 2 {
		t.Fatalf("expected 2 rule stats, got %d", len(s.Rules))
	}
	r1 := findRule(s.Rules, 1)
	if r1 == nil {
		t.Fatal("rule 1 stat missing")
	}
	if r1.Count != 3 {
		t.Errorf("rule1 count = %d, want 3", r1.Count)
	}
	// Rule 1 = 75% of emissions → DecisionNoisy.
	if r1.Decision != DecisionNoisy {
		t.Errorf("rule1 decision = %v, want DecisionNoisy", r1.Decision)
	}
}

func TestInMemoryBudget_OverBudget(t *testing.T) {
	ctx := context.Background()
	b := NewInMemoryBudget(5)
	now := time.Now()
	b.startedAt = now.Add(-25 * time.Hour) // full 24h window: projected == count
	for i := 0; i < 6; i++ {
		b.Record(ctx, 1, "noisy", now.Add(-time.Duration(i)*time.Minute))
	}
	s := b.Status(ctx, now)
	if !s.OverBudget {
		t.Error("6 emissions against budget=5 should mark OverBudget=true")
	}
	if s.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0 (clamped)", s.Remaining)
	}
}

func TestInMemoryBudget_WindowTrimsStale(t *testing.T) {
	ctx := context.Background()
	b := NewInMemoryBudget(10)
	now := time.Now()

	// 8 old emissions (>24h) + 3 fresh. Only fresh remain after trim.
	for i := 0; i < 8; i++ {
		b.Record(ctx, 1, "old", now.Add(-25*time.Hour-time.Duration(i)*time.Minute))
	}
	for i := 0; i < 3; i++ {
		b.Record(ctx, 2, "fresh", now.Add(-time.Duration(i)*time.Minute))
	}
	s := b.Status(ctx, now)
	if s.Emissions24h != 3 {
		t.Errorf("after trim Emissions24h = %d, want 3", s.Emissions24h)
	}
	// Rule 1's counter should have been zeroed by trim.
	for _, r := range s.Rules {
		if r.RuleID == 1 {
			t.Errorf("rule 1 should have been trimmed to 0, got %d", r.Count)
		}
	}
}

func TestInMemoryBudget_DefaultBudget(t *testing.T) {
	b := NewInMemoryBudget(0) // zero → default 10.
	ctx := context.Background()
	s := b.Status(ctx, time.Now())
	if s.BudgetPerDay != DefaultBudgetPerDay {
		t.Errorf("default budget = %d, want %d", s.BudgetPerDay, DefaultBudgetPerDay)
	}

	b = NewInMemoryBudget(-1) // negative → default 10.
	s = b.Status(ctx, time.Now())
	if s.BudgetPerDay != DefaultBudgetPerDay {
		t.Errorf("negative budget → default = %d, want %d", s.BudgetPerDay, DefaultBudgetPerDay)
	}
}

func TestInMemoryBudget_EmptyStatus(t *testing.T) {
	ctx := context.Background()
	b := NewInMemoryBudget(10)
	s := b.Status(ctx, time.Now())
	if s.Emissions24h != 0 {
		t.Errorf("fresh Emissions24h = %d, want 0", s.Emissions24h)
	}
	if s.ProjectedDaily != 0 {
		t.Errorf("fresh ProjectedDaily = %v, want 0", s.ProjectedDaily)
	}
	if s.OverBudget {
		t.Error("empty status should NOT be over budget")
	}
	if s.Remaining != 10 {
		t.Errorf("Remaining = %d, want 10", s.Remaining)
	}
}

func TestInMemoryBudget_ExtrapolatesEarlyWindow(t *testing.T) {
	// 3 alerts within the first 2 hours of uptime project to 36/day —
	// the tracker must flag over-budget NOW, not after a full 24h.
	ctx := context.Background()
	b := NewInMemoryBudget(10)
	now := time.Now()
	b.startedAt = now.Add(-2 * time.Hour)

	for i := 0; i < 3; i++ {
		b.Record(ctx, 1, "early", now.Add(-time.Duration(i)*time.Minute))
	}

	s := b.Status(ctx, now)
	if s.ProjectedDaily < 30 || s.ProjectedDaily > 40 {
		t.Errorf("ProjectedDaily = %v, want ~36 (3 emissions / 2h extrapolated)", s.ProjectedDaily)
	}
	if !s.OverBudget {
		t.Error("projected 36/day against budget=10 should be OverBudget")
	}
}

func TestDecisionThresholds(t *testing.T) {
	ctx := context.Background()
	b := NewInMemoryBudget(100)
	now := time.Now()
	// 25 emissions: rule 1 = 10 (40% share → watch), rule 2 = 15 (60% → noisy).
	for i := 0; i < 10; i++ {
		b.Record(ctx, 1, "almost", now)
	}
	for i := 0; i < 15; i++ {
		b.Record(ctx, 2, "noisy", now)
	}
	s := b.Status(ctx, now)
	r1 := findRule(s.Rules, 1)
	r2 := findRule(s.Rules, 2)
	if r1 == nil || r2 == nil {
		t.Fatal("missing rule stats")
	}
	if r1.Decision != DecisionWatch {
		t.Errorf("rule1 at ~40%% share: decision = %v, want DecisionWatch", r1.Decision)
	}
	if r2.Decision != DecisionNoisy {
		t.Errorf("rule2 at ~60%% share: decision = %v, want DecisionNoisy", r2.Decision)
	}
}

func findRule(rules []RuleStat, id int) *RuleStat {
	for i := range rules {
		if rules[i].RuleID == id {
			return &rules[i]
		}
	}
	return nil
}
