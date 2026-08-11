package signal

import (
	"container/ring"
	"math"
	"sync"
	"time"
)

// RuleStats 跟踪单个规则的触发历史与假阳性估计。
type RuleStats struct {
	mu             sync.Mutex
	ruleID         int
	recentTriggers *ring.Ring // 最近 N 次触发时间戳
	recentResolves *ring.Ring // 最近 N 次解决时间戳
	window         time.Duration

	FP float64 // estimated false positive rate (0..1)
	TP float64 // estimated true positive rate (0..1)
}

// NewRuleStats 创建规则统计跟踪器。
func NewRuleStats(ruleID int, historySize int, window time.Duration) *RuleStats {
	if historySize <= 0 {
		historySize = 20
	}
	return &RuleStats{
		ruleID:         ruleID,
		recentTriggers: ring.New(historySize),
		recentResolves: ring.New(historySize),
		window:         window,
	}
}

// RecordTrigger 记录一次触发。
func (rs *RuleStats) RecordTrigger(at time.Time) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.recentTriggers.Value = at
	rs.recentTriggers = rs.recentTriggers.Next()
	rs.recompute()
}

// RecordResolve 记录一次解决。
func (rs *RuleStats) RecordResolve(at time.Time) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.recentResolves.Value = at
	rs.recentResolves = rs.recentResolves.Next()
}

// ResolveRate 返回估计解决率。
func (rs *RuleStats) ResolveRate() float64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.TP
}

// FPRate 返回估计假阳性率。
func (rs *RuleStats) FPRate() float64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.FP
}

// recompute 重新计算假阳性率估计。
func (rs *RuleStats) recompute() {
	now := time.Now()
	total := 0
	resolved := 0
	outliers := 0

	rs.recentTriggers.Do(func(v interface{}) {
		if v == nil {
			return
		}
		t := v.(time.Time)
		total++
		if rs.wasResolved(t) {
			resolved++
		}
		if now.Sub(t) > rs.window {
			outliers++
		}
	})

	if total > 0 {
		rs.TP = float64(resolved) / float64(total)
		rs.FP = math.Max(0, 1-rs.TP-float64(outliers)/float64(total))
	}
}

func (rs *RuleStats) wasResolved(triggerTime time.Time) bool {
	found := false
	rs.recentResolves.Do(func(v interface{}) {
		if v == nil {
			return
		}
		t := v.(time.Time)
		resolveDelay := t.Sub(triggerTime)
		if resolveDelay > 0 && resolveDelay < rs.window {
			found = true
		}
	})
	return found
}

// Tracker 管理所有规则统计。
type Tracker struct {
	mu    sync.Mutex
	stats map[int]*RuleStats
}

// NewTracker 创建跟踪器。
func NewTracker() *Tracker {
	return &Tracker{stats: make(map[int]*RuleStats)}
}

// Register 注册规则（如果尚未注册）。
func (t *Tracker) Register(ruleID int, window time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.stats[ruleID]; !ok {
		t.stats[ruleID] = NewRuleStats(ruleID, 20, window)
	}
}

// Stats 返回一个规则的统计。
func (t *Tracker) Stats(ruleID int) *RuleStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats[ruleID]
}

// Unregister 移除规则统计。
func (t *Tracker) Unregister(ruleID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.stats, ruleID)
}
