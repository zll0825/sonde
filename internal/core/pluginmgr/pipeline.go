package pluginmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/detector"
	"capital_observatory/internal/core/ontology"
	"capital_observatory/pkg/model"
)

// ObservationQuerier fetches observations for a metric UID in a time range,
// bounded to `limit` rows (the query uses LIMIT to protect against unbounded
// scans when the lookback window is large). Satisfied by store.PostgresResearchStore.
type ObservationQuerier interface {
	GetObservations(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]model.Observation, error)
}

// Pipeline orchestrates M3 rule evaluation and durable alert creation after M2
// ingestion succeeds. Research assembly is consumed independently from the
// transactional research.requested event written with each new alert.
//
// Data flow (driven by PluginManager after a successful PushSnapshots):
//
//	snapshots → M2.Ingest → [observations in DB]
//	              ↓
//	Pipeline.EvaluateAndAlert(metricID, pluginID)
//	    → store.GetActiveRules → matching rules
//	    → store.GetMetricFrequency → metric cadence
//	    → frequency-aware lookback window + row limit
//	    → obsQuerier.GetObservations (bounded window + LIMIT)
//	    → detector.EvaluateBatch
//	    → alert.Engine.HandleTrigger (→ alerts + notification/research outbox)
type Pipeline struct {
	rules     *ontology.Store
	obs       ObservationQuerier
	detectors *detector.Engine
	alerts    *alert.Engine
}

// NewPipeline creates a fully-wired M3 evaluation pipeline.
func NewPipeline(
	ruleStore *ontology.Store,
	obsQuerier ObservationQuerier,
	detEngine *detector.Engine,
	alertEng *alert.Engine,
) *Pipeline {
	return &Pipeline{
		rules:     ruleStore,
		obs:       obsQuerier,
		detectors: detEngine,
		alerts:    alertEng,
	}
}

// EvaluateAndAlert 对某个 metric_id 的观测执行 M3 流程：查规则 → 频率感知
// 回看取观测 → 探测器评估 → 触发转告警（去重）→ 未复现的 active 告警
// 自动 resolve。新告警的研究快照由 durable outbox 消费者异步装配。
//
// 由 manager 在摄入落库之后调用，确保探测器查询能看到刚插入的行。
// 不同 metric_id 可并发调用；alerts 唯一部分索引由数据库做行级互斥。
func (p *Pipeline) EvaluateAndAlert(ctx context.Context, metricID, pluginID string) error {
	// Fetch all active, enabled rules for this metric.
	allRules, err := p.rules.GetActiveRules(ctx)
	if err != nil {
		return fmt.Errorf("get active rules: %w", err)
	}
	var rulesForMetric []model.Rule
	for _, r := range allRules {
		if r.MetricID == metricID {
			rulesForMetric = append(rulesForMetric, r)
		}
	}
	if len(rulesForMetric) == 0 {
		log.Debug().Str("metric_id", metricID).Msg("no active rules for metric")
		return nil
	}

	// Resolve metric_id → metric_uid (observations are keyed by UID).
	uid, err := p.rules.GetMetricUID(ctx, metricID)
	if err != nil {
		return fmt.Errorf("resolve metric uid: %w", err)
	}
	if uid == "" {
		log.Debug().Str("metric_id", metricID).Msg("metric uid not yet registered")
		return nil
	}

	// Frequency-aware lookback window. A rule needing N consecutive observations
	// of a metric declared at frequency F needs at least N×F of history (plus
	// headroom) to ever fire. We compute the required points from the rule
	// configs, derive a window from the metric's declared frequency, and bound
	// the query with a row LIMIT. This replaces the prior fixed 7-day window
	// that silently disabled weekly-cadence trend rules on real data.
	until := time.Now()
	freq, err := p.rules.GetMetricFrequency(ctx, metricID)
	if err != nil {
		return fmt.Errorf("resolve metric frequency: %w", err)
	}
	lookback := frequencyAwareLookback(freq, rulesForMetric)
	since := until.Add(-lookback)
	limit := observationsLimit(lookback, freq)
	obs, err := p.obs.GetObservations(ctx, uid, since, until, limit)
	if err != nil {
		return fmt.Errorf("get observations: %w", err)
	}
	if len(obs) == 0 {
		log.Debug().Str("metric_id", metricID).Str("uid", uid).Msg("no recent observations")
		return nil
	}

	// Group observations by MetricID (detector expects grouped input).
	groups := map[string][]model.Observation{metricID: obs}

	// Run detectors.
	triggers := p.detectors.EvaluateBatch(ctx, groups, rulesForMetric)

	// Track which rule IDs fired this round (dedup by rule ID).
	firedRuleIDs := make(map[int]struct{}, len(triggers))
	var alertErr error
	for _, t := range triggers {
		firedRuleIDs[t.RuleID] = struct{}{}
	}

	// Convert triggers → alerts and dispatch.
	for _, t := range triggers {
		modelAlert := triggerToAlert(t, pluginID)
		if err := p.alerts.HandleTrigger(ctx, modelAlert); err != nil {
			log.Error().Err(err).
				Str("metric_id", metricID).
				Str("rule", t.RuleName).
				Msg("alert handle failed")
			if alertErr == nil {
				alertErr = fmt.Errorf("handle alert for rule %q: %w", t.RuleName, err)
			}
			continue
		}
	}

	// Auto-resolve: active alerts for this metric whose rule did NOT fire.
	// PRD §5.4 — when a rule's condition is no longer true, the alert is
	// automatically marked resolved. Evaluate-and-resolve is atomic per-push,
	// so every push tick sweeps stale alerts.
	if len(rulesForMetric) > 0 {
		if err := p.alerts.AutoResolveStaleAlerts(ctx, metricID, firedRuleIDs); err != nil {
			log.Error().Err(err).
				Str("metric_id", metricID).
				Msg("auto-resolve stale alerts failed")
		}
	}
	if alertErr != nil {
		return alertErr
	}

	return nil
}

// resolveMetricUID was replaced by ontology.Store.GetMetricUID — the previous
// implementation scanned GetCurrentMetrics(ctx, "") which always returned an
// empty set (no plugin has an empty id), silently disabling the pipeline.

// triggerToAlert converts a detector Trigger to a persisted model.Alert.
//
// The alert ID must be unique per alert row (alerts.id is the primary key), so
// it carries a random suffix. Idempotency across retries is NOT the ID's job:
// dedup is enforced by GetActiveAlert + the partial unique index on dedup_key —
// reusing "alt_"+dedup_key as the ID would collide the moment a resolved alert
// re-fires (same key, new row).
func triggerToAlert(t *detector.Trigger, pluginID string) model.Alert {
	now := time.Now()
	return model.Alert{
		ID:                newAlertID(),
		Title:             t.RuleName,
		Summary:           detector.Summarize(t),
		Severity:          t.Severity,
		Status:            "active",
		MetricID:          t.MetricID,
		RuleID:            t.RuleID,
		RuleVersion:       t.RuleVersion,
		RuleEffectiveFrom: t.RuleEffective,
		DetectorName:      t.DetectorName,
		DedupKey:          t.DedupKey,
		WindowStart:       &t.WindowStart,
		WindowEnd:         &t.WindowEnd,
		Evidence:          detector.EvidenceJSON(t.Evidence),
		PluginID:          pluginID,
		SourceProvider:    t.SourceProvider,
		SourceClass:       model.NormalizeSourceClass(t.SourceClass),
		TriggeredAt:       now,
	}
}

// newAlertID generates "alt_" + 12 hex chars from crypto/rand entropy.
func newAlertID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; nanosecond timestamp as last resort.
		return "alt_" + time.Now().Format("20060102T150405.000000000")
	}
	return "alt_" + hex.EncodeToString(b)
}

// frequencyDuration maps a metric's declared frequency to its nominal period.
// Returns 0 for unknown frequencies (callers should fall back to a safe default).
func frequencyDuration(freq string) time.Duration {
	switch freq {
	case "realtime":
		return time.Minute
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	case "quarterly":
		return 91 * 24 * time.Hour
	default:
		return 0
	}
}

// frequencyAwareLookback 计算规则评估需要回看多远，才能为该指标上"最苛刻"
// 的规则凑齐足量观测。
//
// 逻辑：每条规则取 consecutive+1 与 min_observations 的较大者（percentile
// 规则按样本量而非连续长度设门槛），窗口须覆盖该数量 × 声明频率的周期；
// 再加 50% 余量应对修订/缺数（如某周无数据点）。下限 7 天（研究/展示至少
// 要一周上下文），上限 365 天防御病态配置。
func frequencyAwareLookback(freq string, rules []model.Rule) time.Duration {
	period := frequencyDuration(freq)
	if period == 0 {
		// Unknown frequency — fall back to the original 7-day default.
		return 7 * 24 * time.Hour
	}

	maxPoints := 0
	for _, r := range rules {
		var cfg struct {
			Consecutive     int `json:"consecutive"`
			MinObservations int `json:"min_observations"`
		}
		if err := json.Unmarshal(r.Config, &cfg); err != nil {
			continue
		}
		points := cfg.Consecutive + 1
		if cfg.MinObservations > points {
			points = cfg.MinObservations
		}
		if points > maxPoints {
			maxPoints = points
		}
	}

	// Floor at 5 points: the percentile detector refuses to fire below its
	// min_observations default (5) even when the rule config omits it, so a
	// smaller window would leave such rules silently inert.
	periods := maxPoints
	if periods < 5 {
		periods = 5
	}

	// 50% headroom, rounded up.
	lookback := time.Duration(float64(periods) * 1.5 * float64(period))

	// Clamp: min 7 days, max 1 year.
	minLookback := 7 * 24 * time.Hour
	maxLookback := 365 * 24 * time.Hour
	if lookback < minLookback {
		return minLookback
	}
	if lookback > maxLookback {
		return maxLookback
	}
	return lookback
}

// observationsLimit 依据回看窗口与声明频率推算观测查询的行数上限：既要让
// 探测器看全整个窗口（2 倍余量，理由同上），又不能让查询失控膨胀。高频
// 指标（realtime/hourly）单位周期行数天然多，上限收得更紧。
func observationsLimit(lookback time.Duration, freq string) int {
	period := frequencyDuration(freq)
	if period == 0 {
		return 500 // safe default
	}
	// Estimated observation count = lookback / period, with 2x headroom.
	estimated := int(lookback / period * 2)
	// Floor: at least enough for the longest streak we might need.
	if estimated < 20 {
		estimated = 20
	}
	// Ceiling: prevent unbounded scans for very wide windows on fast metrics.
	const maxLimit = 500
	if estimated > maxLimit {
		return maxLimit
	}
	return estimated
}
