// Package signal 实现信号质量评分与优先级排序。
// 它将来源元数据（SourceClass、Grade、新鲜度）与规则触发统计结合为单一
// 优先级分数，用于决定研究排序和告警升级。
package signal

import (
	"math"
	"time"

	"capital_observatory/pkg/model"
)

// Quality 对单次触发事件的可信度评估。
type Quality struct {
	SourceClass model.SourceClass `json:"source_class"`
	Grade       string            `json:"grade"`
	Freshness   time.Duration     `json:"freshness"`    // 观测时间到现在的延迟
	SourceCount int               `json:"source_count"` // 多少独立数据源确认

	// Computed score (0 … 100)
	Score float64 `json:"score"`
}

// 计算信号质量分数。
func (q *Quality) Compute() float64 {
	q.Score = ComputeQuality(q.SourceClass, q.Grade, q.Freshness, q.SourceCount)
	return q.Score
}

// 计算信号质量分数。
func ComputeQuality(sourceClass model.SourceClass, grade string, freshness time.Duration, sourceCount int) float64 {
	var score float64

	switch sourceClass {
	case model.SourceClassReal:
		score = 80
	case model.SourceClassTest:
		score = 40
	case model.SourceClassMock:
		score = 20
	default:
		score = 10
	}

	// Grade 调整
	switch grade {
	case "realtime":
		score += 15
	case "delayed":
		score += 5
	case "estimated":
		score -= 10
	default:
		score += 0
	}

	// 新鲜度惩罚（较旧的数据降低信任度）
	if freshness > 0 {
		daysStale := freshness.Hours() / 24
		if daysStale > 0 {
			penalty := math.Min(daysStale*2, 30) // 最大扣30分
			score -= penalty
		}
	}

	// 多源确认奖励
	if sourceCount > 1 {
		score += math.Min(float64(sourceCount)*5, 20)
	}

	// 边界
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// PriorityScore 根据质量分数和严重性计算优先级。
func PriorityScore(quality float64, severity model.Severity, coalesced bool) float64 {
	rank := severityRank(severity)
	// 基础分 = 质量分数 * 严重性权重
	p := quality * (1 + float64(rank)*0.3)
	// 合并事件提升优先级
	if coalesced {
		p *= 1.2
	}
	return p
}

func severityRank(s model.Severity) int {
	switch s {
	case model.SeverityInfo:
		return 0
	case model.SeverityWarning:
		return 1
	case model.SeverityCritical:
		return 2
	default:
		return 0
	}
}
