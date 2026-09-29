// positioning 插件：CFTC 持仓报告（Legacy，仅期货）非商业净持仓、FINRA 融资余额。
// 无必需密钥；CFTC_APP_TOKEN 可选（设置后以 X-App-Token 发送，缺省匿名访问）。
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	positioning "sonde/plugins/positioning"
	"sonde/plugins/positioning/internal/collector"
)

func main() {
	reg := mustRegistration()

	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        reg.GetInfo().GetName(),
		Version:           reg.GetInfo().GetVersion(),
		DefaultInterval:   6 * time.Hour, // weekly / monthly sources
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
	raw, err := positioning.CatalogFS.ReadFile("manifest.yaml")
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
