// Package noise 实现"噪音预算"：对系统单位时间可发出的告警总量设置可调
// 上限。按 PRD §十七 成功标准 #4，默认规则配置下全系统预算为 ≤10 条/天，
// 以 soak 期真实误报率为准校准。
//
// BudgetTracker 由 alert-outbox 处理器在每次告警派发时递增；Status 输出
// 当前日投放速率与按规则的消耗排名，帮助运维定位"吵闹"规则。
package noise

import (
	"context"
	"sync"
	"time"
)

// DefaultBudgetPerDay is the first calibration target from PRD §十七.
const DefaultBudgetPerDay = 10

// Decision is the budget's recommendation for a hot rule.
type Decision string

const (
	DecisionOK    Decision = "ok"
	DecisionWatch Decision = "watch" // approaching budget share
	DecisionNoisy Decision = "noisy" // exceeds recommended share
)

// RuleStat is the per-rule breakdown used by operators tuning thresholds.
type RuleStat struct {
	RuleID        int       `json:"rule_id"`
	RuleName      string    `json:"rule_name"`
	Count         int       `json:"count"`
	ShareOfBudget float64   `json:"share_of_budget"` // 0..1 against total emissions
	LastTriggered time.Time `json:"last_triggered"`
	Decision      Decision  `json:"decision"`
}

// Status is the overall budget health at a point in time.
type Status struct {
	BudgetPerDay   int        `json:"budget_per_day"`
	WindowHours    float64    `json:"window_hours"`
	Emissions24h   int        `json:"emissions_24h"`
	ProjectedDaily float64    `json:"projected_daily"`
	Remaining      int        `json:"remaining"`
	OverBudget     bool       `json:"over_budget"`
	Rules          []RuleStat `json:"rules"`
	GeneratedAt    time.Time  `json:"generated_at"`
}

// BudgetTracker records alert emissions and reports budget health.
type BudgetTracker interface {
	// Record notes that an alert fired for the given rule at time t.
	Record(ctx context.Context, ruleID int, ruleName string, t time.Time)

	// Status returns the current budget consumption using the sliding
	// window ending at `now`. Clears points older than 24h.
	Status(ctx context.Context, now time.Time) Status
}

// InMemoryBudget is a process-local BudgetTracker backed by a sliding window.
// It is NOT persisted across restarts (which is fine — after a restart the
// next 24h window re-derives from ongoing emissions). For a durable tracker
// that survives restart, wrap a Postgres-backed implementation in the same
// interface.
type InMemoryBudget struct {
	mu           sync.Mutex
	budgetPerDay int
	startedAt    time.Time  // for extrapolation before a full 24h has elapsed
	emissions    []emission // ordered by time ascending, trimmed lazily
	perRule      map[int]*ruleCounter
}

type emission struct {
	ruleID   int
	ruleName string
	t        time.Time
}

type ruleCounter struct {
	ruleID   int
	ruleName string
	count    int
	last     time.Time
}

// NewInMemoryBudget creates a budget tracker with the given daily cap.
func NewInMemoryBudget(perDay int) *InMemoryBudget {
	if perDay <= 0 {
		perDay = DefaultBudgetPerDay
	}
	return &InMemoryBudget{
		budgetPerDay: perDay,
		startedAt:    time.Now(),
		emissions:    make([]emission, 0, perDay*4),
		perRule:      make(map[int]*ruleCounter),
	}
}

// Record implements BudgetTracker.
func (b *InMemoryBudget) Record(_ context.Context, ruleID int, ruleName string, t time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.emissions = append(b.emissions, emission{ruleID: ruleID, ruleName: ruleName, t: t})
	rc, ok := b.perRule[ruleID]
	if !ok {
		rc = &ruleCounter{ruleID: ruleID, ruleName: ruleName}
		b.perRule[ruleID] = rc
	}
	rc.count++
	rc.last = t
	if ruleName != "" {
		rc.ruleName = ruleName
	}
}

// Status 实现 BudgetTracker：先把滚动窗口裁剪到最近 24h 再计算日投放
// 预测值，保证过期历史不会扭曲调参判断；同时输出按规则的消耗排名。
func (b *InMemoryBudget) Status(_ context.Context, now time.Time) Status {
	b.mu.Lock()
	defer b.mu.Unlock()

	cutoff := now.Add(-24 * time.Hour)
	// Trim emissions older than the 24h window (emissions is time-ordered).
	trimmed := 0
	for trimmed < len(b.emissions) && b.emissions[trimmed].t.Before(cutoff) {
		trimmed++
	}
	if trimmed > 0 {
		b.emissions = append(b.emissions[:0:0], b.emissions[trimmed:]...)
		// Rebuild per-rule counters from the trimmed history so counts match
		// the same window as the projected rate. This is O(n) over the
		// trailing 24h of emissions — fine at the target ≤10/day rate.
		for id := range b.perRule {
			b.perRule[id].count = 0
			b.perRule[id].last = time.Time{}
		}
		for _, e := range b.emissions {
			rc := b.perRule[e.ruleID]
			rc.count++
			if e.t.After(rc.last) {
				rc.last = e.t
			}
		}
	}

	// The effective observation window: 24h once the tracker has been alive
	// that long, otherwise the elapsed time since start. Extrapolating from a
	// shorter window is the whole point — 3 alerts in the first 2 hours
	// projects to 36/day, which operators want to see NOW, not tomorrow.
	n := len(b.emissions)
	windowHours := now.Sub(b.startedAt).Hours()
	if windowHours > 24 {
		windowHours = 24
	}
	if windowHours < 0.25 {
		windowHours = 0.25 // floor at 15min so early emissions don't explode the projection
	}
	var projected float64
	if n > 0 {
		projected = float64(n) * (24.0 / windowHours)
	}
	remaining := b.budgetPerDay - int(projected+0.5)
	if remaining < 0 {
		remaining = 0
	}

	rules := make([]RuleStat, 0, len(b.perRule))
	for _, rc := range b.perRule {
		if rc.count == 0 {
			continue
		}
		share := float64(rc.count) / float64(max(n, 1))
		decision := DecisionOK
		// Single rule consuming >40% of the budget is flag-worthy.
		if share > 0.4 {
			decision = DecisionNoisy
		} else if share > 0.2 {
			decision = DecisionWatch
		}
		rules = append(rules, RuleStat{
			RuleID:        rc.ruleID,
			RuleName:      rc.ruleName,
			Count:         rc.count,
			ShareOfBudget: share,
			LastTriggered: rc.last,
			Decision:      decision,
		})
	}

	return Status{
		BudgetPerDay:   b.budgetPerDay,
		WindowHours:    windowHours,
		Emissions24h:   n,
		ProjectedDaily: projected,
		Remaining:      remaining,
		OverBudget:     int(projected+0.5) > b.budgetPerDay,
		Rules:          rules,
		GeneratedAt:    now,
	}
}
