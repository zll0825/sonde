// commodities 插件：覆盖两大商品维度——能源（WTI 原油）与工业金属（IMF 初级铜价），
// 均来自 FRED。
// 退役指标: metal.precious.gold (与 etf 的 gld.ass.price 重复跟踪黄金，却独占
// Alpha Vantage 25 次/天的全部配额)；连带退役 GOLD 实体、gold_percentile_surge
// 规则与两条 GOLD 关系，ALPHAVANTAGE_API_KEY 随之无使用者。
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	commodities "sonde/plugins/commodities"
	"sonde/plugins/commodities/internal/collector"
)

const defaultCollectionInterval = 2 * time.Hour

func main() {
	reg := mustRegistration()

	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        reg.GetInfo().GetName(),
		Version:           reg.GetInfo().GetVersion(),
		DefaultInterval:   defaultCollectionInterval,
		BuildRegistration: func() *pb.RegisterPluginRequest { return reg },
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			return collector.NewRealCollector(mustBindingsYAML())
		},
	}).Run()
}

func mustRegistration() *pb.RegisterPluginRequest {
	raw, err := commodities.CatalogFS.ReadFile("manifest.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("read manifest.yaml")
	}
	reg, err := pluginrunner.LoadRegistration(raw)
	if err != nil {
		log.Fatal().Err(err).Msg("compile manifest.yaml")
	}
	return reg
}

func mustBindingsYAML() []byte {
	raw, err := commodities.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("read bindings.yaml")
	}
	return raw
}

func buildRegistration() *pb.RegisterPluginRequest {
	return mustRegistration()
}

var (
	_ pluginrunner.Provider         = (*collector.RealCollector)(nil)
	_ pluginrunner.WindowedProvider = (*collector.RealCollector)(nil)
)
