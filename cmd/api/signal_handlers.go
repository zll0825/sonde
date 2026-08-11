// Signal quality timeline endpoints. Exposes GET /api/signal/quality/{metric_uid}
// so frontend / researchers can inspect the quality score time series for a
// metric without touching the observation ingestion pipeline.
package main

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/signal"
	"capital_observatory/pkg/model"
)

// signalQualityPoint is a single observation row enriched with a computed
// quality score. Served by signalQualityHandler.
type signalQualityPoint struct {
	Time         time.Time         `json:"time"`
	MetricID     string            `json:"metric_id"`
	Value        float64           `json:"value"`
	SourceClass  model.SourceClass `json:"source_class"`
	Grade        string            `json:"grade"`
	SourceCount  int               `json:"source_count"`
	QualityScore float64           `json:"quality_score"`
}

// signalQualityHandler serves GET /api/signal/quality/{metric_uid}. It scans the
// most recent 100 observations for the given metric_uid, computes a quality
// score per row using signal.ComputeQuality, and returns the timeline.
func signalQualityHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
			return
		}

		metricUID := r.PathValue("metric_uid")
		if metricUID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metric_uid required"})
			return
		}

		rows, err := db.Query(r.Context(), `
			SELECT o.time, o.metric_id, o.value, o.source_class, o.quality_grade,
			       COALESCE(sc.cnt, 1) AS source_count
			FROM observations o
			LEFT JOIN (
				SELECT time, metric_id, COUNT(DISTINCT source_provider) AS cnt
				FROM observations
				WHERE metric_uid = $1
				  AND time > now() - interval '1 day'
				GROUP BY time, metric_id
			) sc ON sc.time = o.time AND sc.metric_id = o.metric_id
			WHERE o.metric_uid = $1
			ORDER BY o.time DESC
			LIMIT 100
		`, metricUID)
		if err != nil {
			log.Error().Err(err).Str("metric_uid", metricUID).Msg("signal quality query failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		points := []signalQualityPoint{}
		for rows.Next() {
			var (
				p        signalQualityPoint
				grade    string
				srcClass model.SourceClass
			)
			if err := rows.Scan(&p.Time, &p.MetricID, &p.Value, &srcClass, &grade, &p.SourceCount); err != nil {
				log.Warn().Err(err).Str("metric_uid", metricUID).Msg("scan signal quality row failed")
				continue
			}
			p.SourceClass = srcClass
			p.Grade = grade
			freshness := time.Since(p.Time)
			p.QualityScore = signal.ComputeQuality(srcClass, grade, freshness, p.SourceCount)
			points = append(points, p)
		}
		if err := rows.Err(); err != nil {
			log.Error().Err(err).Str("metric_uid", metricUID).Msg("iterate signal quality rows failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		if points == nil {
			points = []signalQualityPoint{}
		}
		writeJSON(w, http.StatusOK, points)
	}
}
