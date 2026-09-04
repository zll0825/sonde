// crypto 插件：BTC 价格（CoinGecko）、全网算力（mempool.space）、
// 链上交易数（blockchain.com），小时级轮询。
// 退役指标: btc.ass.exchange_balance (无免费可信数据源)
// 新增指标: btc.ass.tx_count (真实链上活跃度指标)
package main

import (
	"context"
	"os"
	"time"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	"sonde/plugins/crypto/internal/collector"
)

const (
	pluginVersion = "0.2.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "crypto",
		Version:           pluginVersion,
		DefaultInterval:   1 * time.Hour, // free-tier cadence for multiple sources
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			return collector.NewRealCollector(), nil
		},
	}).Run()
}

// compile-time assertions
var _ pluginrunner.Provider = (*collector.RealCollector)(nil)
var _ pluginrunner.WindowedProvider = (*collector.RealCollector)(nil)

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "crypto",
			Version:     pluginVersion,
			Description: "BTC price, network hash rate, and on-chain activity tracker",
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
				Id:          "btc.ass.price",
				Name:        "BTC Price (USD)",
				Description: "Current BTC spot price in USD",
				Unit:        "USD",
				Frequency:   "hourly",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.hash_rate",
				Name:        "BTC Network Hash Rate (EH/s)",
				Description: "Bitcoin network hash rate in exahashes per second",
				Unit:        "EH/s",
				Frequency:   "hourly",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.tx_count",
				Name:        "BTC Daily Transaction Count",
				Description: "Number of Bitcoin transactions per day; on-chain activity proxy for network usage",
				Unit:        "transactions",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.flow_proxy",
				Name:        "BTC Active Addresses 7d Change (flow proxy)",
				Description: "7-day percentage change in active addresses; serves as on-chain exchange-flow proxy",
				Unit:        "%",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
		},
		Relations: []*pb.RelationSuggestion{},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "btc_price_change",
				MetricId:     "btc.ass.price",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"percentile":95,"consecutive":1}`),
				Description:  "BTC price exceeds 95th percentile of recent history",
			},
			{
				Name:         "btc_hashrate_drop",
				MetricId:     "btc.ass.hash_rate",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"direction":"down","consecutive":3}`),
				Description:  "BTC hash rate declining for 3+ consecutive observations (network security concern)",
			},
			{
				Name:         "btc_tx_surge",
				MetricId:     "btc.ass.tx_count",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_INFO,
				Config:       []byte(`{"percentile":90,"consecutive":1}`),
				Description:  "BTC daily transaction count above 90th percentile (high network activity)",
			},
			{
				Name:         "flow_proxy_spike",
				MetricId:     "btc.ass.flow_proxy",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"percentile":90,"consecutive":1}`),
				Description:  "Active-address 7d change (flow proxy) exceeds 90th percentile — unusual exchange flow",
			},
		},
		ChangeLog: "Retired btc.ass.exchange_balance (no free source); added btc.ass.tx_count (blockchain.com real source); added btc.ass.flow_proxy (active address 7d change as exchange-flow proxy); new rules: btc_hashrate_drop, btc_tx_surge, flow_proxy_spike",
	}
}
