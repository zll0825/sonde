// Package macro 实现 Macro Plugin。
package macro

import (
	"context"
	"math/rand"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
)

// Plugin 是 Macro 领域的能力包。
type Plugin struct{}

func init() {
	core.RegisterPlugin(&Plugin{})
}

func (p *Plugin) Name() string    { return "macro" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Metrics() []core.Metric {
	return []core.Metric{
		{
			ID:               "usd.index",
			Name:             "美元指数",
			Description:      "DXY美元指数",
			Unit:             "index",
			Frequency:        24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "dxy",
			ObservedProperty: "value",
		},
		{
			ID:               "us.10y_yield",
			Name:             "美国10年期国债收益率",
			Description:      "美国10年期国债收益率",
			Unit:             "percent",
			Frequency:        24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "us_10y_yield",
			ObservedProperty: "yield",
		},
		{
			ID:               "us.2y_yield",
			Name:             "美国2年期国债收益率",
			Description:      "美国2年期国债收益率",
			Unit:             "percent",
			Frequency:        24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "us_2y_yield",
			ObservedProperty: "yield",
		},
		{
			ID:               "fed.balance_sheet",
			Name:             "美联储资产负债表规模",
			Description:      "美联储总资产负债表规模",
			Unit:             "USD",
			Frequency:        7 * 24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "fed",
			ObservedProperty: "balance_sheet",
		},
		{
			ID:               "us.cpi.yoy",
			Name:             "美国CPI同比",
			Description:      "美国消费者价格指数同比变化",
			Unit:             "percent",
			Frequency:        30 * 24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "us_cpi",
			ObservedProperty: "yoy",
		},
		{
			ID:               "china.northbound.flow",
			Name:             "北向资金净流入",
			Description:      "沪港通/深港通北向资金净流入",
			Unit:             "CNY",
			Frequency:        24 * time.Hour,
			Plugin:           "macro",
			ObservedEntityID: "china_northbound",
			ObservedProperty: "net_flow",
		},
	}
}

func (p *Plugin) Entities() []core.Entity {
	return []core.Entity{
		{
			ID:         "dxy",
			Name:       "美元指数",
			Namespace:  "forex",
			EntityType: core.EntityFactor,
			Tags:       []string{"usd", "forex", "factor"},
			Plugin:     "macro",
		},
		{
			ID:         "us_10y_yield",
			Name:       "美国10年期国债收益率",
			Namespace:  "bond",
			EntityType: core.EntityFactor,
			Tags:       []string{"us", "bond", "yield"},
			Plugin:     "macro",
		},
		{
			ID:         "us_2y_yield",
			Name:       "美国2年期国债收益率",
			Namespace:  "bond",
			EntityType: core.EntityFactor,
			Tags:       []string{"us", "bond", "yield"},
			Plugin:     "macro",
		},
		{
			ID:         "fed",
			Name:       "美联储",
			Namespace:  "institution",
			EntityType: core.EntityInstitution,
			Tags:       []string{"us", "central_bank"},
			Plugin:     "macro",
		},
		{
			ID:         "us_cpi",
			Name:       "美国CPI",
			Namespace:  "indicator",
			EntityType: core.EntityIndicator,
			Tags:       []string{"us", "inflation", "cpi"},
			Plugin:     "macro",
		},
		{
			ID:         "china_northbound",
			Name:       "北向资金",
			Namespace:  "flow",
			EntityType: core.EntityFlow,
			Tags:       []string{"china", "a_share", "flow"},
			Plugin:     "macro",
		},
	}
}

func (p *Plugin) Relations() []core.Relation {
	return []core.Relation{
		{
			SourceID:      "fed",
			TargetID:      "us_10y_yield",
			RelationType:  core.RelInfluences,
			Direction:     core.DirForward,
			Confidence:    0.9,
			Description:   "美联储政策直接影响长端利率",
			Plugin:        "macro",
		},
		{
			SourceID:      "fed",
			TargetID:      "us_2y_yield",
			RelationType:  core.RelInfluences,
			Direction:     core.DirForward,
			Confidence:    0.95,
			Description:   "美联储政策直接影响短端利率",
			Plugin:        "macro",
		},
		{
			SourceID:      "dxy",
			TargetID:      "china_northbound",
			RelationType:  core.RelInfluences,
			Direction:     core.DirForward,
			Confidence:    0.6,
			Description:   "美元强弱影响跨境资金流向",
			Plugin:        "macro",
		},
		{
			SourceID:      "us_cpi",
			TargetID:      "fed",
			RelationType:  core.RelInfluences,
			Direction:     core.DirForward,
			Confidence:    0.8,
			Description:   "通胀数据影响美联储决策",
			Plugin:        "macro",
		},
	}
}

func (p *Plugin) Collect(ctx context.Context) ([]core.MetricSnapshot, error) {
	now := time.Now()

	snapshots := []core.MetricSnapshot{
		{
			MetricID:  "usd.index",
			Value:     103 + rand.Float64()*2 - 1,
			Timestamp: now,
		},
		{
			MetricID:  "us.10y_yield",
			Value:     4.2 + rand.Float64()*0.3 - 0.15,
			Timestamp: now,
		},
		{
			MetricID:  "us.2y_yield",
			Value:     4.5 + rand.Float64()*0.3 - 0.15,
			Timestamp: now,
		},
		{
			MetricID:  "fed.balance_sheet",
			Value:     7.5e12, // 7.5万亿
			Timestamp: now,
		},
		{
			MetricID:  "us.cpi.yoy",
			Value:     3.0 + rand.Float64()*0.5,
			Timestamp: now,
		},
		{
			MetricID:  "china.northbound.flow",
			Value:     (rand.Float64() - 0.3) * 10e9, // 偏向净流入
			Timestamp: now,
		},
	}

	return snapshots, nil
}

func (p *Plugin) DefaultDetectors() []core.DetectorConfig {
	return []core.DetectorConfig{
		{
			ID:       "macro-dxy-percentile",
			Type:     core.DetPercentile,
			MetricID: "usd.index",
			Params: map[string]interface{}{
				"percentile": 0.95,
			},
			Enabled: true,
		},
		{
			ID:       "macro-10y-trend",
			Type:     core.DetTrend,
			MetricID: "us.10y_yield",
			Params: map[string]interface{}{
				"min_consecutive": 5.0,
			},
			Enabled: true,
		},
		{
			ID:       "macro-northbound-trend",
			Type:     core.DetTrend,
			MetricID: "china.northbound.flow",
			Params: map[string]interface{}{
				"min_consecutive": 5.0,
			},
			Enabled: true,
		},
	}
}

func (p *Plugin) BuildResearchContext(ctx context.Context, alert core.Alert) (*core.ResearchContext, error) {
	return &core.ResearchContext{
		Alert: alert,
		Narrative: "宏观经济指标异常。建议关注：1) 美联储最新会议纪要；" +
			"2) 最新就业和通胀数据；3) 全球主要央行政策动向；" +
			"4) 地缘政治风险变化。",
	}, nil
}
