// fedops 插件：财政部 TGA、纽约联储 RRP/SRF/SOFR、OFR 金融压力指数。
// 无密钥；缺 FRED_API_KEY 不得导致本插件失败。
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	fedops "sonde/plugins/fedops"
	"sonde/plugins/fedops/internal/collector"
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
	raw, err := fedops.CatalogFS.ReadFile("manifest.yaml")
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
