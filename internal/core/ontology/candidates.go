// Package ontology 扩展：证据支持的统计关系候选。
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
	SourceID      string    `json:"source_id"`
	TargetID      string    `json:"target_id"`
	RelationType  string    `json:"relation_type"`
	Direction     string    `json:"direction"`
	Correlation   float64   `json:"correlation"`
	Lag           Duration  `json:"lag"`
	PVale         float64   `json:"p_value"`
	Observations  int       `json:"observations"`
	Confidence    float64   `json:"confidence"`
	FirstObserved time.Time `json:"first_observed"`
	LastObserved  time.Time `json:"last_observed"`
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
//
// 超时序对齐：先按时间戳配对（容差 < 1 天），再做相关性计算。
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

	// 超时序对齐：按时间戳配对（容差 < MaxLag 或 1 天，取较小值）
	tolerance := 24 * time.Hour
	if cf.config.MaxLag > 0 && cf.config.MaxLag < tolerance {
		tolerance = cf.config.MaxLag
	}
	alignedSrc, alignedTgt := alignTimeSeries(sourceObs, targetObs, tolerance)
	if len(alignedSrc) < cf.config.MinObservations {
		return nil, nil
	}

	return cf.computeCandidates(sourceID, targetID, alignedSrc, alignedTgt), nil
}

// Discover scans entity pairs for candidate relations.
//
// 返回 []Candidate + error；error 是所有 pair 错误的聚合。
// 对每个失败的 pair 打印一条 Warn 日志后继续，不会静默吞错。
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

// computeCandidates 基于已对齐的时间序列计算候选关系。
func (cf *CandidateFinder) computeCandidates(sourceID, targetID string, sourceObs, targetObs []ObservationPoint) []Candidate {
	var candidates []Candidate

	corr := pearsonCorrelation(sourceObs, targetObs)
	if math.Abs(corr) >= cf.config.MinCorrelation {
		relType := "correlates"
		if corr < 0 {
			relType = "inversely_correlates"
		}
		pVal := pearsonPValue(corr, len(sourceObs))
		firstObs, lastObs := timeBounds(sourceObs)
		candidates = append(candidates, Candidate{
			SourceID:      sourceID,
			TargetID:      targetID,
			RelationType:  relType,
			Direction:     "undirected",
			Correlation:   corr,
			Lag:           Duration{0},
			PVale:         pVal,
			Observations:  len(sourceObs),
			Confidence:    math.Abs(corr),
			FirstObserved: firstObs,
			LastObserved:  lastObs,
		})
	}

	return candidates
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

// pearsonPValue 通过 t 分布近似计算 Pearson 相关系数的双边 p-value。
// 使用 math.Erf 近似正态 CDF（t >= 20 后近似很准）。
//
//	t = r * sqrt((n-2)/(1 - r^2))
//	df = n-2
//	p = 2 * (1 - 0.5*(1 + erf(|t|/sqrt(2))))   // 小样本保守近似
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

// alignTimeSeries 按时间戳配对两条序列。对于 a 中每个点，在 b 中找最近的点，
// 若时间差 <= tolerance 则配成一对；未匹配的点被丢弃。
// 返回的两条序列等长且时间顺序对齐。
func alignTimeSeries(a, b []ObservationPoint, tolerance time.Duration) ([]ObservationPoint, []ObservationPoint) {
	// 为加速查找，建 b 按时间戳映射（最近的那个保留）
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

// timeBounds 返回序列的首尾观测时间。
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
