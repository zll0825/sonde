// macro 插件：美联储资产负债表、美债十年期收益率、美元指数、美元兑人民币、
// CPI指数、同比通胀率，数据源为 FRED（需 FRED_API_KEY），小时级轮询。
// 领域目录在 plugins/macro/{manifest,bindings}.yaml；新增 FRED 序列改 YAML。
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	fredprov "sonde/pkg/provider/fred"
	macro "sonde/plugins/macro"
	"sonde/plugins/macro/internal/collector"
)

func main() {
	reg := mustRegistration()

	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        reg.GetInfo().GetName(),
		Version:           reg.GetInfo().GetVersion(),
		DefaultInterval:   1 * time.Hour,
		BuildRegistration: func() *pb.RegisterPluginRequest { return reg },
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			return collector.NewFREDCollector(mustBindingsYAML())
		},
	}).Run()
}

func mustRegistration() *pb.RegisterPluginRequest {
	raw, err := macro.CatalogFS.ReadFile("manifest.yaml")
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
	raw, err := macro.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("read bindings.yaml")
	}
	return raw
}

var (
	_ pluginrunner.Provider         = (*fredprov.Collector)(nil)
	_ pluginrunner.WindowedProvider = (*fredprov.Collector)(nil)
)

func buildRegistration() *pb.RegisterPluginRequest {
	return mustRegistration()
}
