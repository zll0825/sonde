// etf 插件：GLD 真实价格与交易量（Yahoo Finance），日频采集。
// 退役指标: gld.ass.daily_flow, eth.ass.daily_flow (无免费可信数据源)
// 退役实体: ETH-P (关联的指标已全部退役)
// 新增指标: gld.ass.volume (真实交易量，作为流动性/关注度代理)
package main

import (
	"context"
	"os"
	"time"

	"capital_observatory/pkg/pluginrunner"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"capital_observatory/plugins/etf/internal/collector"
)

const (
	pluginVersion = "0.2.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "etf",
		Version:           pluginVersion,
		DefaultInterval:   1 * time.Hour, // Yahoo Finance public API; conservative hourly polling for free tier
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			return collector.NewYahooCollector(), nil
		},
	}).Run()
}

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "etf",
			Version:     pluginVersion,
			Description: "GLD gold ETF price and volume tracker",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "GLD",
				Name:       "GLD",
				Namespace:  "gld",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"gold", "etf"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "gld.ass.price",
				Name:        "GLD Price (USD)",
				Description: "Current price of GLD share in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
			{
				Id:          "gld.ass.volume",
				Name:        "GLD Trading Volume (shares/day)",
				Description: "Daily trading volume of GLD shares; liquidity and market interest proxy",
				Unit:        "shares",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
		},
		Relations: []*pb.RelationSuggestion{},
		Rules:     []*pb.RuleSuggestion{},
		ChangeLog: "Retired synthetic metrics (gld.ass.daily_flow, eth.ass.daily_flow); retired ETH-P entity; added gld.ass.volume (real source: Yahoo Finance); increased polling to 1h for free-tier compliance",
	}
}
