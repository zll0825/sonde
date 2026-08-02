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
	pluginVersion = "0.1.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "etf",
		Version:           pluginVersion,
		DefaultInterval:   10 * time.Second,
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, bool, error) {
			var prov pluginrunner.Provider
			if os.Getenv("PROVIDER") == "mock" {
				prov = collector.Mock{}
			} else {
				prov = collector.NewYahooCollector()
			}
			_, windowed := prov.(pluginrunner.WindowedProvider)
			return prov, windowed, nil
		},
	}).Run()
}

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "etf",
			Version:     pluginVersion,
			Description: "Gold/Ethereum ETF flow tracker",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "GLD",
				Name:       "GLD",
				Namespace:  "gld",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"gold", "etf"},
			},
			{
				Id:         "ETH-P",
				Name:       "Ethereum Prime",
				Namespace:  "eth",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"ethereum", "etf"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "gld.ass.daily_flow",
				Name:        "GLD Daily Flow (USD)",
				Description: "Daily inflow/outflow for GLD in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
			{
				Id:          "eth.ass.daily_flow",
				Name:        "ETH-P Daily Flow (USD)",
				Description: "Daily inflow/outflow for ETH-P in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "ETH-P",
			},
			{
				Id:          "gld.ass.price",
				Name:        "GLD Price (USD)",
				Description: "Current price of GLD share in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "GLD",
				TargetId:     "ETH-P",
				RelationType: "tracks",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "GLD tracks ETH-P",
			},
		},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "gld_flow_spike",
				MetricId:     "gld.ass.daily_flow",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"operator":"gt","value":500000000,"consecutive":2}`),
				Description:  "GLD daily flow exceeds 500M USD threshold",
			},
		},
		ChangeLog: "Initial ETF plugin registration",
	}
}
