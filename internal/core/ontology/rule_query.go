package ontology

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sonde/pkg/model"
)

// GetMetricUID returns the registered uid for a metric_id's current version
// (effective_to IS NULL), or "" when the metric is not registered.
func (s *Store) GetMetricUID(ctx context.Context, metricID string) (string, error) {
	var uid string
	err := s.db.QueryRow(ctx, `
		SELECT uid FROM metric_definitions
		WHERE id = $1 AND effective_to IS NULL LIMIT 1
	`, metricID).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query metric uid: %w", err)
	}
	return uid, nil
}

// GetMetricFrequency returns the declared frequency of a metric's current
// version (effective_to IS NULL), or "" when the metric is not registered.
// Frequency values: "realtime", "hourly", "daily", "weekly", "monthly", "quarterly".
func (s *Store) GetMetricFrequency(ctx context.Context, metricID string) (string, error) {
	var freq string
	err := s.db.QueryRow(ctx, `
		SELECT frequency FROM metric_definitions
		WHERE id = $1 AND effective_to IS NULL LIMIT 1
	`, metricID).Scan(&freq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query metric frequency: %w", err)
	}
	return freq, nil
}

// GetActiveRules returns all currently-effective, enabled rules (versioned
// tables:WHERE effective_to IS NULL AND enabled = TRUE).
func (s *Store) GetActiveRules(ctx context.Context) ([]model.Rule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, metric_id, detector_name, severity, config, description,
		       enabled, source, is_override, version, effective_from, effective_to
		FROM rules
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
