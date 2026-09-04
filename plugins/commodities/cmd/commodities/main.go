// commodities 插件：覆盖三大商品维度——能源（WTI 原油）、工业金属（IMF 初级铜价）、
// 贵金属（XAUUSD 黄金现货）。WTI/铜来自 FRED，黄金来自 Alpha Vantage。
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
