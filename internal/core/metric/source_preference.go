package metric

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Decision represents the outcome of source preference resolution.
type Decision struct {
	Accept bool   // true = ingest, false = deduplicate/reject
	Reason string // explanation for the decision
}

// Resolver resolves source preferences by querying the source_preferences table.
type Resolver struct {
	db DB
}

// NewResolver creates a new source preference resolver.
func NewResolver(db DB) *Resolver {
	return &Resolver{db: db}
}

// Resolve determines if the given (metricID, pluginID, provider) combination
// should be ingested. Decision logic:
//
//   - No matching metric_id in metric_definitions_v2 → "unknown_source", Accept=false
//   - No source_preferences row → Accept=true (first to register wins)
//   - source_preferences exists and current source has highest or equal priority → Accept=true
//   - Other source has higher priority → Accept=false
//   - Equal priority → Accept=true (tie-break: first writer wins)
func (r *Resolver) Resolve(ctx context.Context, metricID, pluginID, provider string) (Decision, error) {
	// Verify the metric_id exists (use column `id`, not `metric_id`).
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM metric_definitions_v2 WHERE id = $1 AND effective_to IS NULL
		)`, metricID).Scan(&exists)

	if errors.Is(err, pgx.ErrNoRows) {
		return Decision{Accept: false, Reason: "unknown_source"}, nil
	}
	if err != nil {
		return Decision{}, fmt.Errorf("check metric existence: %w", err)
	}
	if !exists {
		return Decision{Accept: false, Reason: "metric_definition_missing"}, nil
	}

	// Look up the source_preferences row for this source.
	var currentPriority *int
	err = r.db.QueryRow(ctx, `
		SELECT priority FROM source_preferences
		WHERE metric_id = $1 AND source_plugin = $2 AND source_provider = $3`,
		metricID, pluginID, provider).Scan(&currentPriority)

	if errors.Is(err, pgx.ErrNoRows) {
		// No preference row for this source — first to register wins.
		return Decision{Accept: true, Reason: "first_to_register"}, nil
	}
	if err != nil {
		return Decision{}, fmt.Errorf("query source preference: %w", err)
	}
	if currentPriority == nil {
		return Decision{Accept: true, Reason: "first_to_register"}, nil
	}

	// Check whether another source has higher priority (lower number = higher priority).
	var higherExists bool
	err = r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM source_preferences
			WHERE metric_id = $1 AND priority < $2
		)`, metricID, *currentPriority).Scan(&higherExists)

	if err != nil {
		return Decision{}, fmt.Errorf("check higher priority: %w", err)
	}
	if higherExists {
		return Decision{Accept: false, Reason: "higher_priority_exists"}, nil
	}

	// Current source has highest priority (or equal) — accept.
	return Decision{Accept: true, Reason: "equal_priority_first_wins"}, nil
}
