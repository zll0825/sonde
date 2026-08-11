// Package ontology 扩展：证据支持的统计关系候选。
package ontology

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// CandidateConfig 候选发现配置。
type CandidateConfig struct {
	MinCorrelation  float64
	MinObservations int
	MaxLag          time.Duration
	Lookback        time.Duration
}

// DefaultCandidateConfig 返回保守默认配置。
func DefaultCandidateConfig() CandidateConfig {
	return CandidateConfig{
		MinCorrelation:  0.6,
		MinObservations: 30,
		MaxLag:          7 * 24 * time.Hour,
		Lookback:        30 * 24 * time.Hour,
	}
}

// Candidate 统计关系候选。
type Candidate struct {
	SourceID      string   `json:"source_id"`
	TargetID      string   `json:"target_id"`
	RelationType  string   `json:"relation_type"`
	Direction     string   `json:"direction"`
	Correlation   float64  `json:"correlation"`
	Lag           Duration `json:"lag"`
	Observations  int      `json:"observations"`
	Confidence    float64  `json:"confidence"`
	FirstObserved time.Time `json:"first_observed"`
	LastObserved  time.Time `json:"last_observed"`
	EvidenceKey   string   `json:"evidence_key"`
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

// ObservationPoint is a single observation.
type ObservationPoint struct {
	Time  time.Time
	Value float64
}

// ObservationAPI is the observation query interface.
type ObservationAPI struct {
	Fetch func(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]ObservationPoint, error)
}

// CandidateFinder discovers statistical candidate relations.
type CandidateFinder struct {
	store  *Store
	config CandidateConfig
	api    ObservationAPI
}

// NewCandidateFinder creates a finder.
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

// FindBetween finds candidate relations between two entities.
func (cf *CandidateFinder) FindBetween(ctx context.Context, sourceID, targetID string) ([]Candidate, error) {
	if cf.api.Fetch == nil {
		return nil, fmt.Errorf("observation API not set")
	}

	until := time.Now()
	since := until.Add(-cf.config.Lookback)

	sourceObs, err := cf.api.Fetch(ctx, sourceID, since, until, 1000)
	if err != nil {
		return nil, fmt.Errorf("fetch source observations: %w", err)
	}
	targetObs, err := cf.api.Fetch(ctx, targetID, since, until, 1000)
	if err != nil {
		return nil, fmt.Errorf("fetch target observations: %w", err)
	}

	if len(sourceObs) < cf.config.MinObservations || len(targetObs) < cf.config.MinObservations {
		return nil, nil
	}

	return cf.computeCandidates(sourceID, targetID, sourceObs, targetObs), nil
}

// Discover scans entity pairs for candidate relations.
func (cf *CandidateFinder) Discover(ctx context.Context, entityPairs [][2]string) ([]Candidate, error) {
	var all []Candidate
	for _, pair := range entityPairs {
		cands, err := cf.FindBetween(ctx, pair[0], pair[1])
		if err != nil {
			continue
		}
		all = append(all, cands...)
	}
	sort.Slice(all, func(i, j int) bool {
		return math.Abs(all[i].Correlation) > math.Abs(all[j].Correlation)
	})
	return all, nil
}

func (cf *CandidateFinder) computeCandidates(sourceID, targetID string, sourceObs, targetObs []ObservationPoint) []Candidate {
	var candidates []Candidate

	corr := pearsonCorrelation(sourceObs, targetObs)
	if math.Abs(corr) >= cf.config.MinCorrelation {
		candidates = append(candidates, Candidate{
			SourceID:     sourceID,
			TargetID:     targetID,
			RelationType: "correlates",
			Direction:    "forward",
			Correlation:  corr,
			Lag:          Duration{0},
			Observations: min(len(sourceObs), len(targetObs)),
			Confidence:   math.Abs(corr),
		})
	}

	return candidates
}

// pearsonCorrelation computes Pearson's r.
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
