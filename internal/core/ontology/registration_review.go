package ontology

import (
	"context"
	"fmt"
	"strings"

	"capital_observatory/internal/core/relationmgr"
	"capital_observatory/internal/core/rulemgr"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"github.com/jackc/pgx/v5"
)

const (
	reviewStatusAutoAccepted = "auto_accepted"
	reviewStatusAccepted     = "accepted"
	reviewStatusPending      = "pending"
	reviewStatusRejected     = "rejected"
)

// reviewAndAcceptRelations processes plugin-suggested relations through the
// taxonomy-based review pipeline (relationmgr.Review) and writes accepted
// entries into relations_v2. Already-pending suggestions that have not changed
// are left in place (idempotent).
func reviewAndAcceptRelations(ctx context.Context, tx pgx.Tx, pluginID string, suggestions []*pb.RelationSuggestion) (accepted, unchanged, rejected int, err error) {
	for _, rel := range suggestions {
		layer, ok := relationmgr.LayerOf(rel.GetRelationType())
		if !ok {
			// Unknown relation type → keep as pending, don't reject (plugin may
			// be using a future taxonomy entry we haven't added yet).
			if err := insertRelationSuggestion(ctx, tx, pluginID, rel); err != nil {
				return accepted, unchanged, rejected, err
			}
			continue
		}

		// Extract evidence fields from the proto StatisticalEvidence.
		var hasEvidence bool
		var pValue float64
		var sampleSize int
		if se := rel.GetStatisticalEvidence(); se != nil {
			hasEvidence = true
			pValue = se.GetPValue()
			sampleSize = int(se.GetSampleSize())
		}

		decision := relationmgr.Review(layer, hasEvidence, pValue, sampleSize)

		switch decision.Decision {
		case reviewStatusAutoAccepted, reviewStatusAccepted:
			// Write to the authoritative relations_v2 table.
			if err := acceptRelation(ctx, tx, pluginID, rel); err != nil {
				return accepted, unchanged, rejected, fmt.Errorf("accept relation %q→%q: %w",
					rel.GetSourceId(), rel.GetTargetId(), err)
			}
			accepted++
		case reviewStatusPending:
			if err := insertRelationSuggestion(ctx, tx, pluginID, rel); err != nil {
				return accepted, unchanged, rejected, err
			}
			unchanged++
		case reviewStatusRejected:
			// Record as rejected for audit (status='rejected' in suggestions).
			if err := insertRejectedRelationSuggestion(ctx, tx, pluginID, rel, decision.Reason); err != nil {
				return accepted, unchanged, rejected, err
			}
			rejected++
		}
	}
	return accepted, unchanged, rejected, nil
}

// reviewAndAcceptRules processes plugin-suggested rules through the source-priority
// review pipeline (rulemgr.Review). Accepted rules are written to rules_v2 with
// a new plugin_suggested version. Unchanged ones (skip) leave the prior row intact.
func reviewAndAcceptRules(ctx context.Context, tx pgx.Tx, pluginID string, suggestions []*pb.RuleSuggestion) (accepted, skipped, pendingConflict int, err error) {
	for _, rule := range suggestions {
		// Look up the current rule by (name, metric_id, detector_name).
		var currentSource string
		var currentConfig []byte
		queryErr := tx.QueryRow(ctx, `
			SELECT source, config FROM rules_v2
			WHERE name = $1 AND metric_id = $2 AND detector_name = $3 AND effective_to IS NULL
			ORDER BY version DESC LIMIT 1
		`, rule.GetName(), rule.GetMetricId(), rule.GetDetectorName()).Scan(&currentSource, &currentConfig)

		if queryErr != nil {
			// No existing rule → accept unconditionally (it's new).
			if err := acceptRule(ctx, tx, pluginID, rule); err != nil {
				return accepted, skipped, pendingConflict, fmt.Errorf("accept rule %q: %w", rule.GetName(), err)
			}
			accepted++
			continue
		}

		outcome := rulemgr.Review(rulemgr.Source(currentSource), currentConfig, rule.GetConfig())
		switch outcome.Action {
		case "skip":
			skipped++
		case "accept":
			if err := acceptRule(ctx, tx, pluginID, rule); err != nil {
				return accepted, skipped, pendingConflict, fmt.Errorf("accept rule %q: %w", rule.GetName(), err)
			}
			accepted++
		case "pending_conflict":
			if err := insertRuleSuggestion(ctx, tx, pluginID, rule); err != nil {
				return accepted, skipped, pendingConflict, err
			}
			pendingConflict++
		}
	}
	return accepted, skipped, pendingConflict, nil
}

// acceptRelation writes an accepted relation to relations_v2.
func acceptRelation(ctx context.Context, tx pgx.Tx, pluginID string, rel *pb.RelationSuggestion) error {
	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM relations_v2
		WHERE source_id = $1 AND target_id = $2 AND relation_type = $3
	`, rel.GetSourceId(), rel.GetTargetId(), rel.GetRelationType()).Scan(&maxVersion); err != nil {
		return fmt.Errorf("query max relation version: %w", err)
	}
	newVersion := maxVersion + 1

	layer, _ := relationmgr.LayerOf(rel.GetRelationType())

	var typicalLag *string
	if secs := rel.GetTypicalLagSecs(); secs > 0 {
		s := fmt.Sprintf("%ds", secs)
		typicalLag = &s
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO relations_v2 (source_id, target_id, relation_type, layer, direction,
			confidence, typical_lag, description, source, version, effective_from
		) VALUES ($1, $2, $3, $4, $5, $6, $7::interval, $8, 'plugin_declared', $9, NOW())
		ON CONFLICT DO NOTHING
	`, rel.GetSourceId(), rel.GetTargetId(), rel.GetRelationType(), string(layer),
		directionToString(rel.GetDirection()), rel.GetConfidence(),
		typicalLag, rel.GetDescription(), newVersion)
	return err
}

// acceptRule writes an accepted rule to rules_v2 with a new version.
func acceptRule(ctx context.Context, tx pgx.Tx, pluginID string, rule *pb.RuleSuggestion) error {
	var maxVersion int
	var priorEnabled bool
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0),
		       (SELECT enabled FROM rules_v2
		        WHERE name = $1 AND metric_id = $2 AND detector_name = $3 AND effective_to IS NULL
		        ORDER BY version DESC LIMIT 1)
		FROM rules_v2
		WHERE name = $1 AND metric_id = $2 AND detector_name = $3
	`, rule.GetName(), rule.GetMetricId(), rule.GetDetectorName()).Scan(&maxVersion, &priorEnabled)
	if err != nil {
		// No prior row — first insert, version=1, enabled=true.
		maxVersion = 0
		priorEnabled = true
	}

	newVersion := maxVersion + 1
	if newVersion > 1 {
		// Close the prior effective version.
		if _, cerr := tx.Exec(ctx, `
			UPDATE rules_v2 SET effective_to = NOW()
			WHERE name = $1 AND metric_id = $2 AND detector_name = $3 AND effective_to IS NULL
		`, rule.GetName(), rule.GetMetricId(), rule.GetDetectorName()); cerr != nil {
			return fmt.Errorf("close prior rule version: %w", cerr)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO rules_v2 (name, metric_id, detector_name, severity, config,
			description, enabled, source, is_override, version, effective_from
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'plugin_suggested', FALSE, $8, NOW())
	`, rule.GetName(), rule.GetMetricId(), rule.GetDetectorName(),
		severityToString(rule.GetSeverity()), rule.GetConfig(),
		rule.GetDescription(), priorEnabled, newVersion)

	if err != nil {
		return fmt.Errorf("insert rule: %w", err)
	}
	return nil
}

// insertRejectedRelationSuggestion records a relation suggestion with status='rejected'.
func insertRejectedRelationSuggestion(ctx context.Context, tx pgx.Tx, pluginID string, rel *pb.RelationSuggestion, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO relation_suggestions (source_id, target_id, relation_type, direction,
			confidence, typical_lag, description, evidence, plugin_id, status, review_reason)
		VALUES ($1, $2, $3, $4, $5, $6::interval, $7, $8, $9, 'rejected', $10)
		ON CONFLICT (source_id, target_id, relation_type, plugin_id) DO NOTHING
	`, rel.GetSourceId(), rel.GetTargetId(), rel.GetRelationType(),
		directionToString(rel.GetDirection()), rel.GetConfidence(),
		nil, strings.TrimSpace(rel.GetDescription()), rel.GetEvidence(), pluginID, reason)
	return err
}
