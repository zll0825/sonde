// Package detector 按规则评估观测序列并产出触发事件（Trigger）。内置三类
// 探测器：threshold（阈值越界）、percentile（历史分位异常）、trend（连续
// 趋势）。触发不等于告警——去重与生命周期由 alert 包负责。
package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

// Detector evaluates a single rule against observation data.
// Each detector_type (threshold, percentile, trend) implements this interface.
type Detector interface {
	// Evaluate checks the rule against the provided observations.
	// Returns a Trigger if the rule fires, or nil if it doesn't.
	Evaluate(ctx context.Context, rule model.Rule, observations []model.Observation) (*Trigger, error)

	// Name returns the detector type identifier.
	Name() string
}

// Trigger represents a fired rule evaluation.
type Trigger struct {
	RuleID        int
	RuleName      string
	MetricID      string
	DetectorName  string
	Severity      model.Severity
	WindowStart   time.Time
	WindowEnd     time.Time
	Evidence      map[string]interface{}
	DedupKey      string // stable per (metric_id, rule_id); see ComputeDedupKey
	RuleVersion   int
	RuleEffective time.Time
	PluginID      string
}

// Engine orchestrates rule evaluation across all detectors.
// It is observation-driven: the caller pushes a batch of new observations,
// and the engine evaluates every rule whose metric_id matches.
type Engine struct {
	detectors map[string]Detector // detector_name → detector
}

// NewEngine creates a detector engine with the given detectors registered.
func NewEngine(detectors ...Detector) *Engine {
	d := make(map[string]Detector, len(detectors))
	for _, det := range detectors {
		d[det.Name()] = det
	}
	return &Engine{detectors: d}
}

// EvaluateBatch 对每组观测评估所有匹配规则。入参必须已按 MetricID 分组；
// 引擎为每个 metric_id 找到启用的规则并运行其配置的探测器，产出触发列表。
func (e *Engine) EvaluateBatch(ctx context.Context, groups map[string][]model.Observation, rules []model.Rule) []*Trigger {
	var triggers []*Trigger

	obsByMetric := make(map[string][]model.Observation)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		obs, ok := groups[rule.MetricID]
		if !ok || len(obs) == 0 {
			continue
		}
		obsByMetric[rule.MetricID] = mergeObservations(obsByMetric[rule.MetricID], obs)
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		det, ok := e.detectors[rule.DetectorName]
		if !ok {
			// A rule pointing at an unregistered detector is silently inert —
			// the same failure class as the GetCurrentMetrics("") incident.
			// Warn every cycle so a wiring gap can't hide in the logs.
			log.Warn().
				Int("rule_id", rule.ID).
				Str("rule", rule.Name).
				Str("detector", rule.DetectorName).
				Msg("rule references unregistered detector; rule is inert")
			continue
		}

		obs := obsByMetric[rule.MetricID]
		if len(obs) == 0 {
			continue
		}

		trigger, err := det.Evaluate(ctx, rule, obs)
		if err != nil {
			// One detector failure must not stop the others.
			log.Error().Err(err).
				Int("rule_id", rule.ID).
				Str("metric_id", rule.MetricID).
				Str("detector", rule.DetectorName).
				Msg("detector evaluation failed")
			continue
		}
		if trigger != nil {
			triggers = append(triggers, trigger)
		}
	}

	return triggers
}

// Register adds or replaces a detector implementation.
func (e *Engine) Register(d Detector) {
	e.detectors[d.Name()] = d
}

// HasDetector returns true if the named detector is registered.
func (e *Engine) HasDetector(name string) bool {
	_, ok := e.detectors[name]
	return ok
}

// mergeObservations appends and deduplicates by (metric_uid, time).
func mergeObservations(existing, incoming []model.Observation) []model.Observation {
	seen := make(map[string]struct{}, len(existing))
	for _, o := range existing {
		key := fmt.Sprintf("%s|%d", o.MetricUID, o.Time.UnixNano())
		seen[key] = struct{}{}
	}
	result := append([]model.Observation{}, existing...)
	for _, o := range incoming {
		key := fmt.Sprintf("%s|%d", o.MetricUID, o.Time.UnixNano())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, o)
	}
	return result
}

// ComputeDedupKey creates a stable dedup key for an alert.
//
// The key intentionally excludes the evaluation window: dedup means "while an
// alert for this (metric, rule) condition is ACTIVE, later triggers are folded
// into it" (docs/domain-model.md §5, partial unique index WHERE status='active').
// Including a sliding window timestamp would give every batch a fresh key and
// disable dedup entirely. After the alert resolves, the same key may be reused
// by a new alert — the partial index only constrains ACTIVE rows.
func ComputeDedupKey(metricID string, ruleID int) string {
	return fmt.Sprintf("%s|%d", metricID, ruleID)
}

// EvidenceJSON serializes trigger evidence to JSON bytes for storage.
func EvidenceJSON(ev map[string]interface{}) []byte {
	b, _ := json.Marshal(ev)
	return b
}
