// Package crypto 实现 Crypto Plugin。
package crypto

import (
	"context"
	"math/rand"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
)

// Plugin 是 Crypto 领域的能力包。
type Plugin struct{}

func init() {
	core.RegisterPlugin(&Plugin{})
}

func (p *Plugin) Name() string    { return "crypto" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Metrics() []core.Metric {
	return []core.Metric{
		{
			ID:               "btc.exchange.balance",
			Name:             "BTC交易所余额",
			Description:      "所有交易所的BTC总余额",
			Unit:             "BTC",
			Frequency:        24 * time.Hour,
			Plugin:           "crypto",
			ObservedEntityID: "btc_exchange",
			ObservedProperty: "balance",
		},
		{
			ID:               "btc.price",
			Name:             "BTC价格",
			Description:      "BTC/USD价格",
			Unit:             "USD",
			Frequency:        1 * time.Hour,
			Plugin:           "crypto",
			ObservedEntityID: "btc",
			ObservedProperty: "price",
		},
		{
			ID:               "btc.hashrate",
			Name:             "BTC哈希率",
			Description:      "BTC网络哈希率",
			Unit:             "TH/s",
			Frequency:        24 * time.Hour,
			Plugin:           "crypto",
			ObservedEntityID: "btc_network",
			ObservedProperty: "hashrate",
		},
		{
			ID:               "eth.staking_ratio",
			Name:             "ETH质押比例",
			Description:      "ETH总质押比例",
			Unit:             "percent",
			Frequency:        24 * time.Hour,
			Plugin:           "crypto",
			ObservedEntityID: "eth_staking",
			ObservedProperty: "ratio",
		},
	}
}

func (p *Plugin) Entities() []core.Entity {
	return []core.Entity{
		{
			ID:         "btc",
			Name:       "Bitcoin",
			Namespace:  "crypto",
			EntityType: core.EntityAsset,
			Tags:       []string{"crypto", "btc"},
			Plugin:     "crypto",
		},
		{
			ID:         "btc_exchange",
			Name:       "BTC交易所余额",
			Namespace:  "crypto",
			EntityType: core.EntityChannel,
			Tags:       []string{"crypto", "btc", "exchange"},
			Plugin:     "crypto",
		},
		{
			ID:         "btc_network",
			Name:       "BTC网络",
			Namespace:  "crypto",
			EntityType: core.EntityIndicator,
			Tags:       []string{"crypto", "btc", "network"},
			Plugin:     "crypto",
		},
		{
			ID:         "eth_staking",
			Name:       "ETH质押",
			Namespace:  "crypto",
			EntityType: core.EntityChannel,
			Tags:       []string{"crypto", "eth", "staking"},
			Plugin:     "crypto",
		},
	}
}

func (p *Plugin) Relations() []core.Relation {
	return []core.Relation{
		{
			SourceID:      "btc_exchange",
			TargetID:      "btc",
			RelationType:  core.RelSignals,
			Direction:     core.DirForward,
			Confidence:    0.6,
			Description:   "交易所余额下降通常意味着持有意愿增强",
			Plugin:        "crypto",
		},
		{
			SourceID:      "btc_network",
			TargetID:      "btc",
			RelationType:  core.RelCorrelates,
			Direction:     core.DirBidirectional,
			Confidence:    0.5,
			Description:   "哈希率与价格存在长期相关性",
			Plugin:        "crypto",
		},
	}
}

func (p *Plugin) Collect(ctx context.Context) ([]core.MetricSnapshot, error) {
	now := time.Now()

	snapshots := []core.MetricSnapshot{
		{
			MetricID:  "btc.exchange.balance",
			Value:     2.5e6 - rand.Float64()*100000, // 模拟下降趋势
			Timestamp: now,
		},
		{
			MetricID:  "btc.price",
			Value:     60000 + rand.Float64()*5000,
			Timestamp: now,
		},
		{
			MetricID:  "btc.hashrate",
			Value:     600e18 + rand.Float64()*50e18, // 600 EH/s
			Timestamp: now,
		},
		{
			MetricID:  "eth.staking_ratio",
			Value:     27 + rand.Float64()*2, // ~27%
			Timestamp: now,
		},
	}

	return snapshots, nil
}

func (p *Plugin) DefaultDetectors() []core.DetectorConfig {
	return []core.DetectorConfig{
		{
			ID:       "crypto-btc-balance-trend",
			Type:     core.DetTrend,
			MetricID: "btc.exchange.balance",
			Params: map[string]interface{}{
				"min_consecutive": 7.0,
			},
			Enabled: true,
		},
		{
			ID:       "crypto-btc-price-percentile",
			Type:     core.DetPercentile,
			MetricID: "btc.price",
			Params: map[string]interface{}{
				"percentile": 0.95,
			},
			Enabled: true,
		},
	}
}

func (p *Plugin) BuildResearchContext(ctx context.Context, alert core.Alert) (*core.ResearchContext, error) {
	return &core.ResearchContext{
		Alert: alert,
		Narrative: "BTC链上数据异常。交易所余额持续下降通常意味着长期持有者增加，" +
			"卖压减小。建议关注：1) 矿工持仓变化；2) 巨鲸地址动向；" +
			"3) 宏观风险偏好变化。",
	}, nil
}
