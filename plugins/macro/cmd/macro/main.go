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
	pluginVersion = "0.1.0"
)

func main() {
	pluginrunner.NewLifecycle(pluginrunner.Config{
		PluginName:        "macro",
		Version:           pluginVersion,
		DefaultInterval:   1 * time.Hour, // FRED publishes daily/weekly — hourly poll respects API quota
		BuildRegistration: buildRegistration,
		SetupCollector: func(ctx context.Context) (pluginrunner.Provider, bool, error) {
			if os.Getenv("PROVIDER") == "mock" {
				prov := collector.Mock{}
				_, windowed := pluginrunner.Provider(prov).(pluginrunner.WindowedProvider)
				return prov, windowed, nil
			}
			fred, err := collector.NewFREDCollector()
			if err != nil {
				// Fail fast: without a real key the macro plugin cannot honor its
				// promise of authentic data; surfacing the error to the operator is
				// preferable to silently emitting mock values under the "fred" brand.
				return nil, false, err
			}
			return fred, true, nil
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
			Description: "Fed balance sheet, US yields, USD index & USDCNY tracker",
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
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "FED",
				TargetId:     "US",
				RelationType: "influences",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "Fed policy influences US macro conditions",
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
		},
		ChangeLog: "FRED real-data source enabled; default interval 1h",
	}
}
