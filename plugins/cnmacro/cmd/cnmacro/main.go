// cnmacro 插件：中国流动性子体系 —— M1/M2 同比、社融增量、DR007、
// 央行 7 天逆回购利率、DR007−OMO 利差、LPR。无密钥，只采集不建规则。
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	cnmacro "sonde/plugins/cnmacro"
	"sonde/plugins/cnmacro/internal/collector"
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
			return collector.NewRealCollector(), nil
		},
	}).Run()
}

func mustRegistration() *pb.RegisterPluginRequest {
	raw, err := cnmacro.CatalogFS.ReadFile("manifest.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("read manifest.yaml")
	}
	reg, err := pluginrunner.LoadRegistration(raw)
	if err != nil {
		log.Fatal().Err(err).Msg("compile manifest.yaml")
	}
	return reg
}

func buildRegistration() *pb.RegisterPluginRequest {
	return mustRegistration()
}

var (
	_ pluginrunner.Provider         = collector.Mock{}
	_ pluginrunner.WindowedProvider = collector.Mock{}
	_ pluginrunner.Provider         = (*collector.RealCollector)(nil)
	_ pluginrunner.WindowedProvider = (*collector.RealCollector)(nil)
)
