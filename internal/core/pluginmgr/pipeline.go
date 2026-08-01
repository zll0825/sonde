package pluginmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/detector"
	"capital_observatory/internal/core/ontology"
	"capital_observatory/internal/core/research"
	"capital_observatory/pkg/model"
)

// ObservationQuerier fetches observations grouped by metric_uid for a time range.
// Satisfied by store.PostgresResearchStore.
type ObservationQuerier interface {
	GetObservations(ctx context.Context, metricUID string, since, until time.Time) ([]model.Observation, error)
}

// Pipeline orchestrates M3 (rule evaluation → alert) and M4 (research assembly)
// after M2 ingestion succeeds. It is the single wiring point that turns the
// previously-orphan detector/alert/research libraries into a live data flow.
//
// Data flow (driven by PluginManager after a successful PushSnapshots):
//
//	snapshots → M2.Ingest → [observations in DB]
//	              ↓
//	Pipeline.EvaluateAndAlert(metricID, pluginID)
//	    → store.GetActiveRules → matching rules
//	    → obsQuerier.GetObservations (7-day lookback)
//	    → detector.EvaluateBatch
//	    → alert.Engine.HandleTrigger (→ alerts + outbox)
//	    → research.Assemble + SaveSnapshot
type Pipeline struct {
	rules     *ontology.Store
	obs       ObservationQuerier
	research  *research.Assembler
	detectors *detector.Engine
	alerts    *alert.Engine
}

// NewPipeline creates a fully-wired M3+M4 evaluation pipeline.
func NewPipeline(
	ruleStore *ontology.Store,
	obsQuerier ObservationQuerier,
	researchAsmer *research.Assembler,
	detEngine *detector.Engine,
	alertEng *alert.Engine,
) *Pipeline {
	return &Pipeline{
		rules:     ruleStore,
		obs:       obsQuerier,
		research:  researchAsmer,
		detectors: detEngine,
		alerts:    alertEng,
	}
}

// EvaluateAndAlert runs the full M3 → M4 flow for observations of a metric_id.
//
// Called by the manager after ingestion has persisted new observations — this
// gives the detector a chance to see the freshly-inserted rows before querying.
// The function is safe to call concurrently for different metric_ids; the DB
// handles row-level locking on the alerts unique partial index.
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

	// 7-day lookback covers all default rule windows.
	until := time.Now()
	since := until.Add(-7 * 24 * time.Hour)
	obs, err := p.obs.GetObservations(ctx, uid, since, until)
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
			continue
		}

		// M4: assemble + persist research context.
		rc, asmErr := p.research.Assemble(ctx, modelAlert)
		if asmErr != nil {
			log.Error().Err(asmErr).
				Str("alert_id", modelAlert.ID).
				Msg("research assembly failed")
			continue
		}
		if snapErr := p.research.SaveSnapshot(ctx, rc); snapErr != nil {
			log.Error().Err(snapErr).
				Str("alert_id", modelAlert.ID).
				Msg("research snapshot save failed")
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
