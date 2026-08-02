package main

import (
	"context"
	"time"

	"capital_observatory/pkg/pluginrunner"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"capital_observatory/plugins/crypto/internal/collector"
)

const (
	pluginVersion = "0.1.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "crypto",
		Version:           pluginVersion,
		DefaultInterval:   10 * time.Second,
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, bool, error) {
			prov := collector.Mock{}
			_, ok := pluginrunner.Provider(prov).(pluginrunner.WindowedProvider)
			return prov, ok, nil
		},
	}).Run()
}

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "crypto",
			Version:     pluginVersion,
			Description: "BTC on-chain & exchange balance tracker",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "BTC",
				Name:       "Bitcoin",
				Namespace:  "btc",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"crypto", "bitcoin"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "btc.ass.exchange_balance",
				Name:        "BTC Exchange Balance",
				Description: "Total BTC held across major exchanges",
				Unit:        "BTC",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.hash_rate",
				Name:        "BTC Network Hash Rate (EH/s)",
				Description: "Bitcoin network hash rate in exahashes per second",
				Unit:        "EH/s",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.price",
				Name:        "BTC Price (USD)",
				Description: "Current BTC spot price in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
		},
		Relations: []*pb.RelationSuggestion{},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "btc_exchange_drop",
				MetricId:     "btc.ass.exchange_balance",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_CRITICAL,
				Config:       []byte(`{"operator":"lt","value":1800000,"consecutive":2}`),
				Description:  "BTC exchange balance drops below 1.8M for 2+ days",
			},
			{
				Name:         "btc_price_change",
				MetricId:     "btc.ass.price",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"percentile":95,"consecutive":1}`),
				Description:  "BTC price exceeds 95th percentile of recent history",
			},
			{
				Name:         "btc_outflow_trend",
				MetricId:     "btc.ass.exchange_balance",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"direction":"down","consecutive":7}`),
				Description:  "BTC exchange balance declining for 7+ consecutive days",
			},
		},
		ChangeLog: "Initial Crypto plugin registration",
	}
}
