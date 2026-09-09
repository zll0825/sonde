// crypto 插件：BTC 价格（CoinGecko）、全网算力（mempool.space）、
// 链上交易数（blockchain.com），小时级轮询。
// 退役指标: btc.ass.exchange_balance (无免费可信数据源)
// 退役指标: btc.ass.flow_proxy (与 btc.ass.tx_count 同源，是该序列的 7 日变化率；
// 名字写着「活跃地址」，测的也不是资金流)
//
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
				Name:       "比特币",
				Namespace:  "btc",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"crypto", "bitcoin"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "btc.ass.price",
				Name:        "比特币现货价（美元）",
				Description: "比特币兑美元现货价格",
				Unit:        "USD",
				Frequency:   "hourly",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.hash_rate",
				Name:        "比特币全网算力（EH/s）",
				Description: "比特币网络算力，单位 EH/s",
				Unit:        "EH/s",
				Frequency:   "hourly",
				EntityId:    "BTC",
			},
			{
				Id:          "btc.ass.tx_count",
				Name:        "比特币日交易笔数",
				Description: "比特币链上每日交易笔数，网络使用活跃度的代理",
				Unit:        "transactions",
				Frequency:   "daily",
				EntityId:    "BTC",
			},
		},
		Relations: []*pb.RelationSuggestion{},
		Rules: []*pb.RuleSuggestion{
			// 原为 percentile 95：对价格水平做分位检测，在上行趋势里创新高即持续
			// 处于高分位。用 702 条真实观测按实际回看窗口（hourly 指标被 7 天
			// 下限锁死，约 140 点）复算，现状在 22.8% 的评估上触发。
			//
			// volatility 也不行——同一窗口下 cv 分布本身双峰，任何阈值都在
			// 12~25% 之间。trend 要求连续同向上涨，结构上会自行终止，且尺度无关
			// （绝对 margin 会随 BTC 价格水平老化）。
			// consecutive 4 + tolerance 0.002 实测触发率 0.9%。
			{
				Name:         "btc_price_change",
				MetricId:     "btc.ass.price",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"direction":"up","consecutive":4,"tolerance":0.002,"min_observations":10}`),
				DisplayName:  "比特币连续 4 个采集周期上涨且每次涨幅 ≥0.2%",
				Description:  "BTC 连续 4 个采集周期上涨且每次涨幅 ≥0.2%——持续走高而非某个价位",
			},
			// 原为 consecutive 3 / tolerance 0：在这条序列上等价于抛硬币。
			// 716 个真实观测（36 天小时级）里下跌步占 55.0%，3 连跌的自然发生率
			// (0.55)^3≈16.6%，实测 13.74% 的评估触发、覆盖 30/36 天——它测的是
			// 采样噪声，不是算力事件。被测序列本身还已经平滑过：采集器读的是
			// mempool.space /mining/hashrate/3d 的 currentHashrate，一条 3 日滚动
			// 估计，剩下的波动主要是估计误差。
			//
			// 不用 moving_average：它的 margin 是绝对值，而算力 3 年从 358 涨到
			// 881 EH/s，value-SMA30 的 p5 同期从 -52 漂到 -153 EH/s，固定 margin
			// 会随算力增长在相对含义上收紧，规则从常亮慢慢变成常哑。
			// tolerance 是比例，尺度无关，与上面 btc_price_change 同形态、仅方向相反。
			// consecutive 4 + tolerance 0.002 实测覆盖 5/36 天。
			//
			// 样本局限（与其他规则不同，必须记住）：mempool.space 只提供日频
			// avgHashrate 的历史，那是另一条序列（相邻日可差 ±14%：915/1040/881，
			// 而同期 currentHashrate 为 926.5），数值对不上，无法用来回标。
			// 本参数只由 soak 自身 716 点 / 36 天定出，样本比 FRED 系规则薄。
			//
			// 不写 min_observations：hourly 指标的回看被 frequencyAwareLookback 的
			// 7 天下限锁死（internal/core/pluginmgr/pipeline.go），约 168 点，远超
			// consecutive+1。兄弟规则写 10 是给 daily 指标留余量，这里照抄只会误导。
			{
				Name:         "btc_hashrate_drop",
				MetricId:     "btc.ass.hash_rate",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"direction":"down","consecutive":4,"tolerance":0.002}`),
				DisplayName:  "比特币全网算力连续 4 个采集周期下降且每次跌幅 ≥0.2%",
				Description:  "比特币全网算力连续 4 个采集周期下降且每次跌幅 ≥0.2%——持续走低而非单点回落",
			},
			{
				Name:         "btc_tx_surge",
				MetricId:     "btc.ass.tx_count",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_INFO,
				Config:       []byte(`{"percentile":90,"consecutive":1}`),
				DisplayName:  "比特币日交易笔数处于 90 分位以上",
				Description:  "比特币日交易笔数处于 90 分位以上（链上活跃度偏高）",
			},
		},
		ChangeLog: "Retired btc.ass.exchange_balance (no free source) and btc.ass.flow_proxy (duplicate of btc.ass.tx_count: it was that series' 7d change rate, mislabelled as active addresses), with its flow_proxy_spike rule; added btc.ass.tx_count (blockchain.com real source); rules: btc_hashrate_drop, btc_tx_surge; reshaped btc_hashrate_drop from trend(down, consecutive 3, no tolerance) to trend(down, consecutive 4, tolerance 0.002) — the old shape fired on 13.74% of evaluations across 30 of 36 days, which is sampling noise on an already 3-day-smoothed series, not a hash rate event",
	}
}
