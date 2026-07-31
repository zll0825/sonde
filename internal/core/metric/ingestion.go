package metric

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/ontology"
	pb "capital_observatory/pkg/proto/plugin/v1"
)

// Ingester processes PushSnapshots: scores quality, resolves source preference,
// deduplicates, and inserts observations into the database.
type Ingester struct {
	store    *ontology.Store
	resolver *Resolver
	pending  *PendingTracker
}

// NewIngester creates a new observation ingester.
func NewIngester(store *ontology.Store, db DB) *Ingester {
	return &Ingester{
		store:    store,
		resolver: NewResolver(db),
		pending:  NewPendingTracker(db),
	}
}

// IngestResult summarizes what happened to a PushSnapshots batch.
type IngestResult struct {
	Inserted    int
	Duplicates  int
	Rejected    int
	PendingSeen []string // metric_ids that landed in pending_metrics
}

// Ingest processes a PushSnapshotsRequest: scores + stores observations.
// isPluginHealthy is passed in by the caller (core side has plugin session state).
func (i *Ingester) Ingest(ctx context.Context, ps *pb.PushSnapshotsRequest, isPluginHealthy bool) (IngestResult, error) {
	result := IngestResult{}
	pluginID := ps.PluginId

	for _, snap := range ps.Snapshots {
		labelsHash := hashLabels(snap.Labels)

		// 1. Quality score.
		observedAt := time.Unix(snap.Timestamp, 0)
		fetchedAt := time.Unix(snap.SourceFetchedAt, 0)
		qr := Score(observedAt, fetchedAt, snap.QualityGrade, isPluginHealthy)

		// 2. Source preference resolve.
		decision, err := i.resolver.Resolve(ctx, snap.MetricId, pluginID, snap.SourceProvider)
		if err != nil {
			log.Warn().Err(err).
				Str("metric_id", snap.MetricId).
				Str("plugin", pluginID).
				Msg("source preference resolve failed")
			result.Rejected++
			continue
		}

		if !decision.Accept {
			log.Debug().
				Str("metric_id", snap.MetricId).
				Str("plugin", pluginID).
				Str("reason", decision.Reason).
				Msg("observation rejected")
			if decision.Reason == "metric_definition_missing" {
				// Park it.
				if err := i.pending.Record(ctx, snap.MetricId, pluginID, snap.SourceProvider); err != nil {
					log.Error().Err(err).
						Str("metric_id", snap.MetricId).
						Msg("pending metric record failed")
				}
				result.PendingSeen = append(result.PendingSeen, snap.MetricId)
			}
			result.Rejected++
			continue
		}

		// 3. Insert observation (dedup handled by unique index on DB).
		if err := i.store.InsertObservation(ctx, snap, qr.Grade, qr.Confidence, qr.SystemScore, labelsHash, pluginID); err != nil {
			elogInsertError(err, snap.MetricId, pluginID)
			result.Rejected++
			continue
		}

		result.Inserted++
	}

	return result, nil
}

// hashLabels creates a deterministic hash of the labels map.
func hashLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write([]byte(labels[k]))
		h.Write([]byte(";"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// elogInsertError handles unique-constraint dedup as "duplicate" rather than error.
func elogInsertError(err error, metricID, pluginID string) {
	if isUniqueViolation(err) {
		log.Debug().
			Str("metric_id", metricID).
			Str("plugin", pluginID).
			Msg("observation deduplicated")
	} else {
		log.Error().Err(err).
			Str("metric_id", metricID).
			Str("plugin", pluginID).
			Msg("observation insert failed")
	}
}

// isUniqueViolation returns true if the error is a PG unique violation (23505).
func isUniqueViolation(err error) bool {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return pgerr.Code == "23505"
	}
	return false
}
