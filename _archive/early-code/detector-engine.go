// Package detector 实现异常检测引擎。
//
// Detector 独立于 Plugin，可以作用于任意 Metric。
// 检测的是统计属性，不是业务属性。
package detector

import (
	"context"
	"fmt"
	"math"

	"github.com/capital-observatory/capital-observatory/core"
)

// ──────────────────────────────────────────────
// Detector 接口
// ──────────────────────────────────────────────

// Detector 是单个检测算法的接口。
type Detector interface {
	// Type 返回检测器类型。
	Type() core.DetectorType

	// Detect 对一组 MetricSnapshot 执行检测。
	// 如果发现异常，返回 Alert；否则返回 nil。
	Detect(ctx context.Context, config core.DetectorConfig, snapshots []core.MetricSnapshot) (*core.Alert, error)
}

// ──────────────────────────────────────────────
// Engine
// ──────────────────────────────────────────────

// Engine 是检测引擎，管理所有 Detector 实现并执行检测。
type Engine struct {
	detectors map[core.DetectorType]Detector
	metricSvc core.MetricService
	alertSvc  core.AlertService
}

// NewEngine 创建一个检测引擎。
func NewEngine(metricSvc core.MetricService, alertSvc core.AlertService) *Engine {
	e := &Engine{
		detectors: make(map[core.DetectorType]Detector),
		metricSvc: metricSvc,
		alertSvc:  alertSvc,
	}
	// 注册内置 Detector
	e.Register(&ThresholdDetector{})
	e.Register(&PercentileDetector{})
	e.Register(&TrendDetector{})
	return e
}

// Register 注册一个 Detector 实现。
func (e *Engine) Register(d Detector) {
	e.detectors[d.Type()] = d
}

// RunAll 对所有已注册的 Detector 配置执行检测。
func (e *Engine) RunAll(ctx context.Context) ([]core.Alert, error) {
	metrics, err := e.metricSvc.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}

	var alerts []core.Alert
	for _, m := range metrics {
		a, err := e.RunForMetric(ctx, m.ID)
		if err != nil {
			continue // 单个 Metric 检测失败不影响其他
		}
		alerts = append(alerts, a...)
	}
	return alerts, nil
}

// RunForMetric 对指定 Metric 执行检测。
func (e *Engine) RunForMetric(ctx context.Context, metricID core.MetricID) ([]core.Alert, error) {
	// 获取最近 90 天的数据
	snapshots, err := e.metricSvc.QueryLatest(ctx, metricID, 1000)
	if err != nil {
		return nil, fmt.Errorf("query metric %s: %w", metricID, err)
	}

	if len(snapshots) < 2 {
		return nil, nil
	}

	var alerts []core.Alert

	// 对每种 Detector 类型执行检测
	for detType, det := range e.detectors {
		config := core.DetectorConfig{
			Type:     detType,
			MetricID: metricID,
			Enabled:  true,
		}

		alert, err := det.Detect(ctx, config, snapshots)
		if err != nil {
			continue
		}

		if alert != nil {
			// 去重检查
			exists, _ := e.alertSvc.Dedup(ctx, alert.DedupKey)
			if !exists {
				if err := e.alertSvc.Create(ctx, *alert); err == nil {
					alerts = append(alerts, *alert)
				}
			}
		}
	}

	return alerts, nil
}

// ──────────────────────────────────────────────
// Threshold Detector
// ──────────────────────────────────────────────

// ThresholdDetector 检测值是否超过阈值。
type ThresholdDetector struct{}

func (d *ThresholdDetector) Type() core.DetectorType { return core.DetThreshold }

func (d *ThresholdDetector) Detect(_ context.Context, config core.DetectorConfig, snapshots []core.MetricSnapshot) (*core.Alert, error) {
	if len(snapshots) == 0 {
		return nil, nil
	}

	latest := snapshots[len(snapshots)-1]

	// 从 config.Params 读取阈值
	upper, _ := config.Params["upper"].(float64)
	lower, _ := config.Params["lower"].(float64)

	if upper != 0 && latest.Value > upper {
		return &core.Alert{
			Title:      fmt.Sprintf("%s 超过上限 %.2f", config.MetricID, upper),
			Summary:    fmt.Sprintf("当前值 %.2f 超过阈值 %.2f", latest.Value, upper),
			Severity:   core.SeverityWarning,
			MetricID:   config.MetricID,
			DetectorID: string(config.Type),
			Timestamp:  latest.Timestamp,
			DedupKey:   fmt.Sprintf("threshold:%s:upper:%s", config.MetricID, latest.Timestamp.Format("2006-01-02")),
			Evidence: map[string]interface{}{
				"current_value": latest.Value,
				"threshold":     upper,
			},
		}, nil
	}

	if lower != 0 && latest.Value < lower {
		return &core.Alert{
			Title:      fmt.Sprintf("%s 低于下限 %.2f", config.MetricID, lower),
			Summary:    fmt.Sprintf("当前值 %.2f 低于阈值 %.2f", latest.Value, lower),
			Severity:   core.SeverityWarning,
			MetricID:   config.MetricID,
			DetectorID: string(config.Type),
			Timestamp:  latest.Timestamp,
			DedupKey:   fmt.Sprintf("threshold:%s:lower:%s", config.MetricID, latest.Timestamp.Format("2006-01-02")),
			Evidence: map[string]interface{}{
				"current_value": latest.Value,
				"threshold":     lower,
			},
		}, nil
	}

	return nil, nil
}

// ──────────────────────────────────────────────
// Percentile Detector
// ──────────────────────────────────────────────

// PercentileDetector 检测值是否处于历史极值分位。
type PercentileDetector struct{}

func (d *PercentileDetector) Type() core.DetectorType { return core.DetPercentile }

func (d *PercentileDetector) Detect(_ context.Context, config core.DetectorConfig, snapshots []core.MetricSnapshot) (*core.Alert, error) {
	if len(snapshots) < 20 {
		return nil, nil // 数据不足
	}

	latest := snapshots[len(snapshots)-1]

	// 计算历史分位
	values := make([]float64, len(snapshots))
	for i, s := range snapshots {
		values[i] = s.Value
	}

	percentile := calcPercentile(values, latest.Value)

	// 默认检测 95% 和 99% 分位
	threshold := 0.95
	if t, ok := config.Params["percentile"].(float64); ok {
		threshold = t
	}

	if percentile >= threshold {
		severity := core.SeverityWarning
		if percentile >= 0.99 {
			severity = core.SeverityCritical
		}

		return &core.Alert{
			Title:      fmt.Sprintf("%s 达到历史 %.0f%% 分位", config.MetricID, percentile*100),
			Summary:    fmt.Sprintf("当前值 %.2f 处于历史 %.1f%% 分位", latest.Value, percentile*100),
			Severity:   severity,
			MetricID:   config.MetricID,
			DetectorID: string(config.Type),
			Timestamp:  latest.Timestamp,
			DedupKey:   fmt.Sprintf("percentile:%s:%s", config.MetricID, latest.Timestamp.Format("2006-01-02")),
			Evidence: map[string]interface{}{
				"current_value": latest.Value,
				"percentile":    percentile,
				"sample_count":  len(snapshots),
			},
		}, nil
	}

	return nil, nil
}

func calcPercentile(sorted []float64, value float64) float64 {
	// 先排序
	data := make([]float64, len(sorted))
	copy(data, sorted)
	for i := 0; i < len(data); i++ {
		for j := i + 1; j < len(data); j++ {
			if data[i] > data[j] {
				data[i], data[j] = data[j], data[i]
			}
		}
	}

	count := 0
	for _, v := range data {
		if v <= value {
			count++
		}
	}
	return float64(count) / float64(len(data))
}

// ──────────────────────────────────────────────
// Trend Detector
// ──────────────────────────────────────────────

// TrendDetector 检测是否存在连续单向变化。
type TrendDetector struct{}

func (d *TrendDetector) Type() core.DetectorType { return core.DetTrend }

func (d *TrendDetector) Detect(_ context.Context, config core.DetectorConfig, snapshots []core.MetricSnapshot) (*core.Alert, error) {
	if len(snapshots) < 5 {
		return nil, nil
	}

	// 默认检测连续 7 天
	minConsecutive := 7
	if m, ok := config.Params["min_consecutive"].(float64); ok {
		minConsecutive = int(m)
	}

	// 从最新数据往前数连续上涨/下跌天数
	upCount := 0
	downCount := 0

	for i := len(snapshots) - 1; i > 0; i-- {
		if snapshots[i].Value > snapshots[i-1].Value {
			if downCount > 0 {
				break
			}
			upCount++
		} else if snapshots[i].Value < snapshots[i-1].Value {
			if upCount > 0 {
				break
			}
			downCount++
		} else {
			break
		}
	}

	if upCount >= minConsecutive {
		return &core.Alert{
			Title:      fmt.Sprintf("%s 连续上涨 %d 天", config.MetricID, upCount),
			Summary:    fmt.Sprintf("连续 %d 天上涨，处于趋势异常", upCount),
			Severity:   core.SeverityWarning,
			MetricID:   config.MetricID,
			DetectorID: string(config.Type),
			Timestamp:  snapshots[len(snapshots)-1].Timestamp,
			DedupKey:   fmt.Sprintf("trend:%s:up:%s", config.MetricID, snapshots[len(snapshots)-1].Timestamp.Format("2006-01-02")),
			Evidence: map[string]interface{}{
				"consecutive_days": upCount,
				"direction":        "up",
			},
		}, nil
	}

	if downCount >= minConsecutive {
		return &core.Alert{
			Title:      fmt.Sprintf("%s 连续下跌 %d 天", config.MetricID, downCount),
			Summary:    fmt.Sprintf("连续 %d 天下跌，处于趋势异常", downCount),
			Severity:   core.SeverityWarning,
			MetricID:   config.MetricID,
			DetectorID: string(config.Type),
			Timestamp:  snapshots[len(snapshots)-1].Timestamp,
			DedupKey:   fmt.Sprintf("trend:%s:down:%s", config.MetricID, snapshots[len(snapshots)-1].Timestamp.Format("2006-01-02")),
			Evidence: map[string]interface{}{
				"consecutive_days": downCount,
				"direction":        "down",
			},
		}, nil
	}

	return nil, nil
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stddev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := mean(values)
	sum := 0.0
	for _, v := range values {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)))
}
