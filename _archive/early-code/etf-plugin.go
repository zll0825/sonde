// Package etf 实现 ETF Plugin。
//
// ETF Plugin 负责：
//   - 采集 ETF 资金流、规模、价格数据
//   - 声明 Metric 定义
//   - 声明 Ontology Entity 和 Relation
//   - 提供默认检测策略
//   - 构建 Research 上下文
package etf

import (
	"context"
	"math/rand"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
)

// Plugin 是 ETF 领域的能力包。
type Plugin struct{}

func init() {
	core.RegisterPlugin(&Plugin{})
}

func (p *Plugin) Name() string    { return "etf" }
func (p *Plugin) Version() string { return "0.1.0" }

// ──────────────────────────────────────────────
// Metric 声明
// ──────────────────────────────────────────────

func (p *Plugin) Metrics() []core.Metric {
	return []core.Metric{
		{
			ID:               "gold.etf.net_inflow",
			Name:             "黄金ETF净流入",
			Description:      "黄金ETF每日净流入金额",
			Unit:             "USD",
			Frequency:        24 * time.Hour,
			Plugin:           "etf",
			ObservedEntityID: "gold_etf_flow",
			ObservedProperty: "net_inflow",
		},
		{
			ID:               "gold.etf.aum",
			Name:             "黄金ETF资产管理规模",
			Description:      "黄金ETF总资产管理规模",
			Unit:             "USD",
			Frequency:        24 * time.Hour,
			Plugin:           "etf",
			ObservedEntityID: "gold_etf",
			ObservedProperty: "aum",
		},
		{
			ID:               "gold.etf.price",
			Name:             "黄金ETF价格",
			Description:      "黄金ETF（GLD）收盘价",
			Unit:             "USD",
			Frequency:        24 * time.Hour,
			Plugin:           "etf",
			ObservedEntityID: "gold_etf",
			ObservedProperty: "price",
		},
		{
			ID:               "silver.etf.net_inflow",
			Name:             "白银ETF净流入",
			Description:      "白银ETF每日净流入金额",
			Unit:             "USD",
			Frequency:        24 * time.Hour,
			Plugin:           "etf",
			ObservedEntityID: "silver_etf_flow",
			ObservedProperty: "net_inflow",
		},
	}
}

// ──────────────────────────────────────────────
// Ontology 声明
// ──────────────────────────────────────────────

func (p *Plugin) Entities() []core.Entity {
	return []core.Entity{
		{
			ID:         "gold_etf",
			Name:       "黄金ETF (GLD)",
			Namespace:  "etf",
			EntityType: core.EntityInstrument,
			Tags:       []string{"gold", "etf", "commodity"},
			Plugin:     "etf",
		},
		{
			ID:         "gold_etf_flow",
			Name:       "黄金ETF资金流",
			Namespace:  "etf",
			EntityType: core.EntityFlow,
			Tags:       []string{"gold", "etf", "flow"},
			Plugin:     "etf",
		},
		{
			ID:         "silver_etf",
			Name:       "白银ETF (SLV)",
			Namespace:  "etf",
			EntityType: core.EntityInstrument,
			Tags:       []string{"silver", "etf", "commodity"},
			Plugin:     "etf",
		},
		{
			ID:         "silver_etf_flow",
			Name:       "白银ETF资金流",
			Namespace:  "etf",
			EntityType: core.EntityFlow,
			Tags:       []string{"silver", "etf", "flow"},
			Plugin:     "etf",
		},
	}
}

func (p *Plugin) Relations() []core.Relation {
	return []core.Relation{
		{
			SourceID:      "gold_etf_flow",
			TargetID:      "gold_etf",
			RelationType:  core.RelLeads,
			Direction:     core.DirForward,
			Confidence:    0.7,
			TypicalLag:    24 * time.Hour,
			Description:   "ETF资金流通常领先ETF价格变化1-2天",
			Plugin:        "etf",
		},
		{
			SourceID:      "silver_etf_flow",
			TargetID:      "silver_etf",
			RelationType:  core.RelLeads,
			Direction:     core.DirForward,
			Confidence:    0.65,
			TypicalLag:    24 * time.Hour,
			Description:   "白银ETF资金流领先价格变化",
			Plugin:        "etf",
		},
	}
}

// ──────────────────────────────────────────────
// 数据采集
// ──────────────────────────────────────────────

func (p *Plugin) Collect(ctx context.Context) ([]core.MetricSnapshot, error) {
	now := time.Now()

	// MVP 阶段：模拟数据
	// 实际实现中这里会调用真实 API（如 Yahoo Finance, ETF.com）
	snapshots := []core.MetricSnapshot{
		{
			MetricID:  "gold.etf.net_inflow",
			Value:     simulateInflow(now, 15), // 模拟连续流入
			Timestamp: now,
		},
		{
			MetricID:  "gold.etf.aum",
			Value:     85e9 + rand.Float64()*5e9, // ~850亿美元
			Timestamp: now,
		},
		{
			MetricID:  "gold.etf.price",
			Value:     190 + rand.Float64()*10,
			Timestamp: now,
		},
		{
			MetricID:  "silver.etf.net_inflow",
			Value:     (rand.Float64() - 0.5) * 100e6,
			Timestamp: now,
		},
	}

	return snapshots, nil
}

// simulateInflow 模拟连续流入数据。
// 当前日期的 dayOfYear 对 30 取模，如果 < 15 则模拟连续流入。
func simulateInflow(t time.Time, consecutiveDays int) float64 {
	dayOfYear := t.YearDay()
	// 模拟最近 N 天连续流入
	if dayOfYear%30 < consecutiveDays {
		return 50e6 + rand.Float64()*30e6 // 5000万 ~ 8000万美元净流入
	}
	return (rand.Float64() - 0.5) * 100e6
}

// ──────────────────────────────────────────────
// 默认检测策略
// ──────────────────────────────────────────────

func (p *Plugin) DefaultDetectors() []core.DetectorConfig {
	return []core.DetectorConfig{
		{
			ID:       "etf-gold-inflow-trend",
			Type:     core.DetTrend,
			MetricID: "gold.etf.net_inflow",
			Params: map[string]interface{}{
				"min_consecutive": 7.0,
			},
			Enabled: true,
		},
		{
			ID:       "etf-gold-inflow-percentile",
			Type:     core.DetPercentile,
			MetricID: "gold.etf.net_inflow",
			Params: map[string]interface{}{
				"percentile": 0.95,
			},
			Enabled: true,
		},
		{
			ID:       "etf-silver-inflow-trend",
			Type:     core.DetTrend,
			MetricID: "silver.etf.net_inflow",
			Params: map[string]interface{}{
				"min_consecutive": 7.0,
			},
			Enabled: true,
		},
	}
}

// ──────────────────────────────────────────────
// Research 上下文
// ──────────────────────────────────────────────

func (p *Plugin) BuildResearchContext(ctx context.Context, alert core.Alert) (*core.ResearchContext, error) {
	return &core.ResearchContext{
		Alert: alert,
		Narrative: "黄金ETF资金流异常。历史上，ETF资金持续流入通常与宏观经济避险情绪、" +
			"实际利率下行、美元走弱等因素相关。建议关注：1) 美联储政策动向；" +
			"2) 美元指数走势；3) 美债收益率变化。",
	}, nil
}
