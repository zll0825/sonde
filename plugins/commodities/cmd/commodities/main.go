// commodities 插件：覆盖三大商品维度——能源（WTI 原油）、工业金属（IMF 初级铜价）、
// 贵金属（XAUUSD 黄金现货）。WTI/铜来自 FRED，黄金来自 Alpha Vantage。
// 与现有 ETF / Crypto / Macro 形成互补的研究视图：
//   - 油价→全球增长与通胀压力
//   - 铜价→工业需求与全球 PMI 代理（月度 IMF 初级商品价）
//   - 黄金→避险情绪与实际利率
package main

import (
	"context"
	"os"
	"time"

	"capital_observatory/pkg/pluginrunner"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"capital_observatory/plugins/commodities/internal/collector"
)

const (
	pluginVersion             = "0.2.0"
	defaultCollectionInterval = 2 * time.Hour
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "commodities",
		Version:           pluginVersion,
		DefaultInterval:   defaultCollectionInterval, // 12 scheduled gold calls/day leaves free-tier headroom
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			c, err := collector.NewRealCollector()
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}).Run()
}

// compile-time assertion
var _ pluginrunner.Provider = (*collector.RealCollector)(nil)
var _ pluginrunner.WindowedProvider = (*collector.RealCollector)(nil)

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "commodities",
			Version:     pluginVersion,
			Description: "WTI oil, IMF primary copper, and XAUUSD spot gold for growth, demand, and safe-haven research",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "OIL",
				Name:       "Crude Oil (WTI)",
				Namespace:  "commodity",
				EntityType: pb.EntityType_ENTITY_TYPE_INSTRUMENT,
				Tags:       []string{"energy", "oil", "wti", "us"},
			},
			{
				Id:         "COPPER",
				Name:       "Copper (IMF Primary)",
				Namespace:  "commodity",
				EntityType: pb.EntityType_ENTITY_TYPE_INSTRUMENT,
				Tags:       []string{"industrial_metal", "copper", "pmi_proxy"},
			},
			{
				Id:         "GOLD",
				Name:       "Gold Spot (XAUUSD)",
				Namespace:  "commodity",
				EntityType: pb.EntityType_ENTITY_TYPE_INSTRUMENT,
				Tags:       []string{"precious_metal", "gold", "safe_haven", "real_rates"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "oil.energy.wti",
				Name:        "WTI Crude Oil Spot ($/bbl)",
				Description: "West Texas Intermediate crude oil spot price in USD per barrel",
				Unit:        "USD/bbl",
				Frequency:   "daily",
				EntityId:    "OIL",
			},
			{
				Id:          "metal.industrial.copper",
				Name:        "COMEX Copper (IMF Primary, USD/mt)",
				Description: "Copper price from IMF Primary Commodity Prices (PCOPPUSDM), USD per metric ton, monthly",
				Unit:        "USD/mt",
				Frequency:   "monthly",
				EntityId:    "COPPER",
			},
			{
				Id:          "metal.precious.gold",
				Name:        "Gold Spot (XAUUSD, USD/troy oz)",
				Description: "Physical spot gold quoted as XAUUSD by Alpha Vantage, in USD per troy ounce",
				Unit:        "USD/troy oz",
				Frequency:   "daily",
				EntityId:    "GOLD",
			},
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "OIL",
				TargetId:     "GOLD",
				RelationType: "correlates",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "Surging oil prices often coincide with safe-haven gold demand (inflation signal)",
			},
			{
				SourceId:     "COPPER",
				TargetId:     "GOLD",
				RelationType: "correlates",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "Copper-gold ratio is a classic growth-vs-safe-haven regime indicator",
			},
		},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "wti_spike_threshold",
				MetricId:     "oil.energy.wti",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"operator":"gt","value":100}`),
				Description:  "WTI oil above $100/bbl—extreme price levels causing demand destruction",
			},
			{
				Name:         "gold_percentile_surge",
				MetricId:     "metal.precious.gold",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"percentile":90,"consecutive":2}`),
				Description:  "Gold at/above 90th percentile for 2+ days—possible safe-haven stress",
			},
			{
				Name:         "copper_downtrend",
				MetricId:     "metal.industrial.copper",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_INFO,
				Config:       []byte(`{"direction":"down","consecutive":5}`),
				Description:  "Copper declining 5+ days—potential industrial slowdown signal",
			},
		},
		ChangeLog: "Replaced unavailable FRED gold series with Alpha Vantage XAUUSD spot/history; WTI and copper remain on FRED",
	}
}
