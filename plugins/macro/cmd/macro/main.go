// macro 插件：美联储资产负债表、美债十年期收益率、美元指数、美元兑人民币、
// CPI指数、同比通胀率，数据源为 FRED（需 FRED_API_KEY），小时级轮询。
// 新增指标: us.mkt.cpi (CPI指数), us.mkt.inflation_yoy (同比通胀率)
package main

import (
	"context"
	"os"
	"time"

	"capital_observatory/pkg/pluginrunner"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"capital_observatory/plugins/macro/internal/collector"
)

const (
	pluginVersion = "0.2.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "macro",
		Version:           pluginVersion,
		DefaultInterval:   1 * time.Hour, // FRED publishes daily/weekly — hourly poll respects API quota
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, error) {
			if os.Getenv("PROVIDER") == "mock" {
				return collector.Mock{}, nil
			}
			// 快速失败：没有 FRED_API_KEY 就不启动，宁可报错也不能让
			// mock 数据顶着 "fred" 的名义入库。
			fred, err := collector.NewFREDCollector()
			if err != nil {
				return nil, err
			}
			return fred, nil
		},
	}).Run()
}

// compile-time assertion: FREDCollector implements both interfaces.
var _ pluginrunner.Provider = (*collector.FREDCollector)(nil)
var _ pluginrunner.WindowedProvider = (*collector.FREDCollector)(nil)

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "macro",
			Version:     pluginVersion,
			Description: "Fed balance sheet, US yields, USD index, USDCNY, CPI & inflation tracker",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "FED",
				Name:       "Federal Reserve",
				Namespace:  "fed",
				EntityType: pb.EntityType_ENTITY_TYPE_INSTITUTION,
				Tags:       []string{"central_bank", "us"},
			},
			{
				Id:         "US",
				Name:       "United States",
				Namespace:  "us",
				EntityType: pb.EntityType_ENTITY_TYPE_MARKET,
				Tags:       []string{"us"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "fed.ins.balance_sheet",
				Name:        "Fed Balance Sheet (USD)",
				Description: "Total assets held by the Federal Reserve in USD",
				Unit:        "USD",
				Frequency:   "weekly",
				EntityId:    "FED",
			},
			{
				Id:          "us.mkt.ten_year_yield",
				Name:        "US 10Y Treasury Yield (%)",
				Description: "Yield on the 10-year US Treasury note",
				Unit:        "%",
				Frequency:   "daily",
				EntityId:    "US",
			},
			{
				Id:          "us.mkt.dollar_index",
				Name:        "US Dollar Index",
				Description: "DXY US Dollar Index",
				Unit:        "index",
				Frequency:   "daily",
				EntityId:    "US",
			},
			{
				Id:          "us.mkt.usd_cny",
				Name:        "USD/CNY Exchange Rate",
				Description: "Offshore USD to CNY exchange rate",
				Unit:        "CNY per USD",
				Frequency:   "daily",
				EntityId:    "US",
			},
			{
				Id:          "us.mkt.cpi",
				Name:        "US Consumer Price Index",
				Description: "Consumer Price Index for All Urban Consumers (CPI-U), seasonally adjusted",
				Unit:        "index",
				Frequency:   "monthly",
				EntityId:    "US",
			},
			{
				Id:          "us.mkt.inflation_yoy",
				Name:        "US YoY Inflation Rate (%)",
				Description: "CPI-based year-over-year percent change in consumer prices",
				Unit:        "%",
				Frequency:   "monthly",
				EntityId:    "US",
			},
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "FED",
				TargetId:     "US",
				RelationType: "causes",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "Fed policy causes changes in US macro conditions",
			},
		},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "fed_balance_drop",
				MetricId:     "fed.ins.balance_sheet",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_INFO,
				Config:       []byte(`{"direction":"down","consecutive":4}`),
				Description:  "Fed balance sheet shrinking 4+ weeks in a row (QT signal)",
			},
			{
				Name:         "yield_spike_percentile",
				MetricId:     "us.mkt.ten_year_yield",
				DetectorName: "percentile",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"percentile":90,"consecutive":2}`),
				Description:  "10Y yield at/above 90th percentile for 2+ days",
			},
			{
				Name:         "usd_index_extreme",
				MetricId:     "us.mkt.dollar_index",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_INFO,
				Config:       []byte(`{"operator":"gt","value":105}`),
				Description:  "DXY above 105",
			},
			{
				Name:         "inflation_above_target",
				MetricId:     "us.mkt.inflation_yoy",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"operator":"gt","value":3.0,"consecutive":2}`),
				Description:  "YoY inflation above 3% for 2+ consecutive months (above Fed target)",
			},
		},
		ChangeLog: "Added CPI (us.mkt.cpi) and YoY inflation (us.mkt.inflation_yoy) metrics; added inflation_above_target rule",
	}
}
