// Package ontology extends: evidence-backed statistical relation candidates.
//
// Candidate discovery always infers a "forward" direction because a lead/lag
// relationship is inherently directional — the correlations here come from
// time-series where source may precede target. The "undirected" option remains
// valid for user-submitted relations (API, manual_relations) but is never
// auto-assigned by the statistical pipeline.
package ontology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
)

// CandidateConfig configures the candidate discovery behaviour.
type CandidateConfig struct {
	MinCorrelation  float64
	MinObservations int
	MaxLag          time.Duration
	Lookback        time.Duration
}

// DefaultCandidateConfig returns a conservative default configuration.
func DefaultCandidateConfig() CandidateConfig {
	return CandidateConfig{
		MinCorrelation:  0.6,
		MinObservations: 30,
		MaxLag:          7 * 24 * time.Hour,
		Lookback:        30 * 24 * time.Hour,
	}
}

// Candidate is a statistical relation candidate. Direction is always "forward"
// — the lag search establishes source-leads-target ordering; sign of the
// correlation (not the direction) distinguishes "correlates" from
// "inversely_correlates".
type Candidate struct {
	SourceID      string    `json:"source_id"`
	TargetID      string    `json:"target_id"`
	RelationType  string    `json:"relation_type"`
	Direction     string    `json:"direction"`
	Correlation   float64   `json:"correlation"`
	Lag           Duration  `json:"lag"`
	TypicalLag    string    `json:"typical_lag"`
	PVale         float64   `json:"p_value"`
	Observations  int       `json:"observations"`
	Confidence    float64   `json:"confidence"`
	FirstObserved time.Time `json:"first_observed"`
	LastObserved  time.Time `json:"last_observed"`
	Evidence      string    `json:"evidence,omitempty"`
	EvidenceKey   string    `json:"evidence_key"`
}

// Duration is a serializable duration.
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", d.String())), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

// ObservationPoint is a single (time, value) observation.
type ObservationPoint struct {
	Time  time.Time
	Value float64
}

// ObservationAPI is the observation query interface.
type ObservationAPI struct {
	Fetch func(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]ObservationPoint, error)
}

// EntityResolver maps an entity id to its most-commonly-queried metric UID.
// Implementations may return found=false to signal "no metric known"; the
// finder falls back to using the raw entity id as the metric UID.
type EntityResolver interface {
	MetricUIDForEntity(ctx context.Context, entityID string) (metricUID string, found bool, err error)
}

// CandidateFinder discovers statistical candidate relations between entities.
type CandidateFinder struct {
	store    *Store
	config   CandidateConfig
	api      ObservationAPI
	resolver EntityResolver
}

// NewCandidateFinder creates a finder. The resolver may be nil — in that case
// entity ids are passed through to the observation API as-is (existing test
// path compatibility).
func NewCandidateFinder(store *Store, cfg CandidateConfig) *CandidateFinder {
	return &CandidateFinder{
		store:  store,
		config: cfg,
	}
}

// SetAPI sets the observation API.
func (cf *CandidateFinder) SetAPI(api ObservationAPI) {
	cf.api = api
}

// SetEntityResolver sets the optional entity → metric UID resolver. Passing
// nil disables resolution (entity ids are used directly as metric UIDs).
func (cf *CandidateFinder) SetEntityResolver(r EntityResolver) {
	cf.resolver = r
}

// FindBetween finds candidate relations between two entities.
//
// Metric UID resolution: when an EntityResolver is configured, entity ids are
// resolved to their most-commonly-queried metric UIDs before fetching the
// observation series. Without a resolver the entity ids are used verbatim as
// metric UIDs (backwards compatible).
//
// Time-series alignment pairs observations by timestamp (tolerance = 12 h,
// i.e. half the 24 h lag step). The lag search then tries source-leads-target
// lags from 0 to MaxLag and keeps the one with the highest |r|.
func (cf *CandidateFinder) FindBetween(ctx context.Context, sourceID, targetID string) ([]Candidate, error) {
	if cf.api.Fetch == nil {
		return nil, fmt.Errorf("observation API not set")
	}

	until := time.Now()
	since := until.Add(-cf.config.Lookback)

	sourceMetricUID, targetMetricUID, err := cf.resolveMetricUIDs(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}

	sourceObs, err := cf.api.Fetch(ctx, sourceMetricUID, since, until, 1000)
	if err != nil {
		return nil, fmt.Errorf("fetch source observations: %w", err)
	}
	targetObs, err := cf.api.Fetch(ctx, targetMetricUID, since, until, 1000)
	if err != nil {
		return nil, fmt.Errorf("fetch target observations: %w", err)
	}

	if len(sourceObs) < cf.config.MinObservations || len(targetObs) < cf.config.MinObservations {
		return nil, nil
	}

	return cf.computeCandidates(sourceID, targetID, sourceObs, targetObs), nil
}

// resolveMetricUIDs maps the given entity ids to metric UIDs. When no resolver
// is configured the ids are returned as-is.
func (cf *CandidateFinder) resolveMetricUIDs(ctx context.Context, sourceID, targetID string) (string, string, error) {
	sourceUID, targetUID := sourceID, targetID
	if cf.resolver != nil {
		s, found, err := cf.resolver.MetricUIDForEntity(ctx, sourceID)
		if err != nil {
			return "", "", fmt.Errorf("resolve source metric UID for %q: %w", sourceID, err)
		}
		if found {
			sourceUID = s
		}
		t, found, err := cf.resolver.MetricUIDForEntity(ctx, targetID)
		if err != nil {
			return "", "", fmt.Errorf("resolve target metric UID for %q: %w", targetID, err)
		}
		if found {
			targetUID = t
		}
	}
	return sourceUID, targetUID, nil
}

// Discover scans entity pairs for candidate relations.
//
// Returns []Candidate + error; error aggregates per-pair failures. Each failed
// pair is logged at Warn level and skipped — errors are never silently
// swallowed.
func (cf *CandidateFinder) Discover(ctx context.Context, entityPairs [][2]string) ([]Candidate, error) {
	var all []Candidate
	var aggErrs []error
	for _, pair := range entityPairs {
		cands, err := cf.FindBetween(ctx, pair[0], pair[1])
		if err != nil {
			log.Warn().
				Str("source", pair[0]).
				Str("target", pair[1]).
				Err(err).
				Msg("candidate discovery failed for pair")
			aggErrs = append(aggErrs, err)
			continue
		}
		all = append(all, cands...)
	}
	sort.Slice(all, func(i, j int) bool {
		return math.Abs(all[i].Correlation) > math.Abs(all[j].Correlation)
	})
	if len(aggErrs) > 0 {
		return all, fmt.Errorf("discover encountered %d/%d pair errors: %w",
			len(aggErrs), len(entityPairs), errors.Join(aggErrs...))
	}
	return all, nil
}

// computeCandidates runs a lag search on the raw series and produces up to one
// Candidate when the best |r| clears MinCorrelation. Direction is always
// "forward" — the lag itself encodes the directional relationship (source
// leads target).
func (cf *CandidateFinder) computeCandidates(sourceID, targetID string, sourceObs, targetObs []ObservationPoint) []Candidate {
	bestLag, bestR, bestP := maxLagSearch(sourceObs, targetObs, cf.config.MaxLag, cf.config.MinObservations)

	if math.Abs(bestR) < cf.config.MinCorrelation {
		return nil
	}

	// Align at bestLag to get observation bounds and final count. maxLagSearch
	// already used lagTolerance internally per lag step, so the alignment is
	// deterministic.
	shifted := shiftSeries(sourceObs, bestLag)
	alignedSrc, _ := alignTimeSeries(shifted, targetObs, lagTolerance())
	observations := len(alignedSrc)

	relType := "correlates"
	if bestR < 0 {
		relType = "inversely_correlates"
	}

	firstObs, lastObs := timeBounds(alignedSrc)
	evidence := map[string]interface{}{
		"p_value":       bestP,
		"sample_size":   observations,
		"lookback_days": int(cf.config.Lookback.Hours() / 24),
		"method":        "pearson",
	}
	evidenceJSON, _ := json.Marshal(evidence)

	return []Candidate{{
		SourceID:      sourceID,
		TargetID:      targetID,
		RelationType:  relType,
		Direction:     "forward",
		Correlation:   bestR,
		Lag:           Duration{bestLag},
		TypicalLag:    bestLag.String(),
		PVale:         bestP,
		Observations:  observations,
		Confidence:    math.Abs(bestR),
		FirstObserved: firstObs,
		LastObserved:  lastObs,
		Evidence:      string(evidenceJSON),
	}}
}

// maxLagSearch tries lagging the source series by lag=[0, MaxLag] in 24h step.
// Returns the lag producing the highest |r|, along with r and its p-value at
// that lag. A candidate must yield at least minObservations aligned points.
func maxLagSearch(source, target []ObservationPoint, maxLag time.Duration, minObservations int) (bestLag time.Duration, bestR float64, bestP float64) {
	step := 24 * time.Hour
	if maxLag <= 0 {
		maxLag = 7 * 24 * time.Hour
	}
	bestR = 0
	bestP = 1
	for lag := time.Duration(0); lag <= maxLag; lag += step {
		shifted := shiftSeries(source, lag)
		alignedSource, alignedTarget := alignTimeSeries(shifted, target, lagTolerance())
		if len(alignedSource) < minObservations {
			continue
		}
		r := pearsonCorrelation(alignedSource, alignedTarget)
		p := pearsonPValue(r, len(alignedSource))
		if math.Abs(r) > math.Abs(bestR) {
			bestR = r
			bestP = p
			bestLag = lag
		}
	}
	return
}

// shiftSeries shifts every observation timestamp forward by lag. Models the
// source leading the target: source[t] is compared against target[t+lag].
func shiftSeries(obs []ObservationPoint, lag time.Duration) []ObservationPoint {
	shifted := make([]ObservationPoint, len(obs))
	for i, p := range obs {
		shifted[i] = ObservationPoint{Time: p.Time.Add(lag), Value: p.Value}
	}
	return shifted
}

// lagTolerance is the half-step tolerance used when aligning series inside
// the lag search (12 h = half of the 24 h step).
func lagTolerance() time.Duration {
	return 12 * time.Hour
}

// pearsonCorrelation computes Pearson's r for aligned (same-length, same-order) series.
func pearsonCorrelation(a, b []ObservationPoint) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	n := min(len(a), len(b))
	var sumA, sumB, sumAB, sumA2, sumB2 float64
	for i := 0; i < n; i++ {
		va := a[i].Value
		vb := b[i].Value
		sumA += va
		sumB += vb
		sumAB += va * vb
		sumA2 += va * va
		sumB2 += vb * vb
	}

	numerator := float64(n)*sumAB - sumA*sumB
	denominator := math.Sqrt((float64(n)*sumA2 - sumA*sumA) * (float64(n)*sumB2 - sumB*sumB))
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// pearsonPValue computes a two-sided p-value from Pearson's r via normal
// approximation (math.Erf).
//
//	t = |r| * sqrt((n-2)/(1 - r^2))
//	p = 2 * (1 - Phi(|t|)), Phi via erf
//
// Conservative for small n; df=n-2.
func pearsonPValue(r float64, n int) float64 {
	if n < 3 {
		return 1.0
	}
	rAbs := math.Abs(r)
	if rAbs >= 1.0 {
		return 0.0
	}
	t := rAbs * math.Sqrt(float64(n-2)) / math.Sqrt(1.0-rAbs*rAbs)
	phi := 0.5 * (1.0 + math.Erf(t/math.Sqrt(2)))
	p := 2.0 * (1.0 - phi)
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	return p
}

// alignTimeSeries pairs two series by nearest timestamp within tolerance. For
// each point in a, the closest point in b is chosen; the pair is kept when
// their timestamp delta is <= tolerance. Unmatched points are dropped. Both
// returned slices are equal-length and time-ordered.
func alignTimeSeries(a, b []ObservationPoint, tolerance time.Duration) ([]ObservationPoint, []ObservationPoint) {
	bByTime := make(map[int64]float64, len(b))
	for _, p := range b {
		bByTime[p.Time.Unix()] = p.Value
	}

	var alignA, alignB []ObservationPoint
	for _, pa := range a {
		bestDelta := tolerance + 1
		var bestVal float64
		var found bool
		for _, pb := range b {
			delta := pa.Time.Sub(pb.Time)
			if delta < 0 {
				delta = -delta
			}
			if delta < bestDelta {
				bestDelta = delta
				bestVal = pb.Value
				found = true
			}
		}
		if found && bestDelta <= tolerance {
			alignA = append(alignA, pa)
			alignB = append(alignB, ObservationPoint{Time: pa.Time, Value: bestVal})
		}
	}
	return alignA, alignB
}

// timeBounds returns the first and last observation timestamps.
func timeBounds(obs []ObservationPoint) (first, last time.Time) {
	if len(obs) == 0 {
		return
	}
	first, last = obs[0].Time, obs[0].Time
	for _, p := range obs {
		if p.Time.Before(first) {
			first = p.Time
		}
		if p.Time.After(last) {
			last = p.Time
		}
	}
	return
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
