package ontology

import (
	"context"
	"fmt"
	"time"

	"capital_observatory/pkg/model"
)

// GetActiveRules returns all currently-effective, enabled rules (versioned
// tables:WHERE effective_to IS NULL AND enabled = TRUE).
func (s *Store) GetActiveRules(ctx context.Context) ([]model.Rule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, metric_id, detector_name, severity, config, description,
		       enabled, source, is_override, version, effective_from, effective_to
		FROM rules_v2
		WHERE effective_to IS NULL AND enabled = TRUE
	`)
	if err != nil {
		return nil, fmt.Errorf("query active rules: %w", err)
	}
	defer rows.Close()

	var rules []model.Rule
	for rows.Next() {
		var (
			r           model.Rule
			effTo       *time.Time
			configBytes []byte
		)
		if err := rows.Scan(
			&r.ID, &r.Name, &r.MetricID, &r.DetectorName, &r.Severity,
			&configBytes, &r.Description, &r.Enabled, &r.Source,
			&r.IsOverride, &r.Version, &r.EffectiveFrom, &effTo,
		); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		r.EffectiveTo = effTo
		if len(configBytes) > 0 {
			r.Config = configBytes
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule rows: %w", err)
	}
	return rules, nil
}
