package collector

import (
	"context"
	"math"
	"math/rand"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
)

// Mock returns deterministic-looking but jittered macro series for all real-source
// metrics (original six, first-batch reserves / IORB / EFFR / TIPS / T10Y3M / NFCI,
// second-batch T10Y2Y / breakeven / HY OAS / VIX / unemployment / Sahm / claims / GDP).
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	return []pluginrunner.Snapshot{
		{
			MetricID:    "fed.ins.balance_sheet",
			Value:       7_200_000_000_000 + rand.Float64()*100_000_000_000, // ~$7.2T
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.ten_year_yield",
			Value:       4.2 + rand.Float64()*0.1, // ~4.2%
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.dollar_index",
			Value:       103.0 + rand.Float64()*0.5,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.usd_cny",
			Value:       7.2 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.cpi",
			Value:       300 + rand.Float64()*5, // CPI index level ~300
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.inflation_yoy",
			Value:       3.0 + rand.Float64()*0.5, // ~3.0% YoY
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "fed.ins.reserves",
			Value:       2_900_000_000_000 + rand.Float64()*10_000_000_000,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.iorb",
			Value:       4.4 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.effr",
			Value:       4.33 + rand.Float64()*0.02,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.real_yield_10y",
			Value:       1.8 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.term_spread_10y3m",
			Value:       0.4 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.nfci",
			Value:       -0.3 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.term_spread_10y2y",
			Value:       0.35 + rand.Float64()*0.02,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.breakeven_10y",
			Value:       2.3 + rand.Float64()*0.03,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.hy_oas",
			Value:       2.9 + rand.Float64()*0.1,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.vix",
			Value:       15.0 + rand.Float64()*1.0,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.unemployment_rate",
			Value:       4.1 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.sahm_realtime",
			Value:       0.0 + rand.Float64()*0.05,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.initial_claims_4wk",
			Value:       205_000 + rand.Float64()*5_000,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
		{
			MetricID:    "us.mkt.gdp_nominal",
			Value:       32_500_000_000_000 + rand.Float64()*100_000_000_000,
			Timestamp:   now,
			FetchedAt:   now,
			Provider:    "mock_macro",
			SourceClass: model.SourceClassMock,
			Grade:       "delayed",
		},
	}, nil
}

// GetSnapshotsForWindow synthesizes a daily macro series across [start, end]
// using bounded random walk drift for all real-source metrics.
func (Mock) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	const day = 24 * time.Hour
	if end.Before(start) {
		end = start
	}
	const maxSamples = 60
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, maxSamples*20)
	for d, n := start, 0; !d.After(end) && n < maxSamples; d, n = d.Add(day), n+1 {
		seeded := rand.New(rand.NewSource(d.Unix() / 86400))
		drift := 1.0 + (seeded.Float64()-0.5)*0.005 // tighter drift — macro moves slower
		drift = math.Max(0.95, math.Min(1.05, drift))
		out = append(out,
			pluginrunner.Snapshot{MetricID: "fed.ins.balance_sheet", Value: 7_200_000_000_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.ten_year_yield", Value: 4.2 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.dollar_index", Value: 103.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.usd_cny", Value: 7.2 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.cpi", Value: 300 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.inflation_yoy", Value: 3.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "fed.ins.reserves", Value: 2_900_000_000_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.iorb", Value: 4.4 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.effr", Value: 4.33 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.real_yield_10y", Value: 1.8 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.term_spread_10y3m", Value: 0.4 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.nfci", Value: -0.3 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.term_spread_10y2y", Value: 0.35 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.breakeven_10y", Value: 2.3 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.hy_oas", Value: 2.9 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.vix", Value: 15.0 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.unemployment_rate", Value: 4.1 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.initial_claims_4wk", Value: 205_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.gdp_nominal", Value: 32_500_000_000_000 * drift, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
			pluginrunner.Snapshot{MetricID: "us.mkt.sahm_realtime", Value: (drift - 1) * 20, Timestamp: d, FetchedAt: fetchedAt, Provider: "mock_macro", SourceClass: model.SourceClassMock, Grade: "estimated"},
		)
	}
	return out, nil
}
