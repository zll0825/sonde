package collector

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
)

// latestPositioning collects the ETF-flow and derivatives metrics (TFTC, OKX,
// Deribit). Each source fails independently: a failure is logged and only that
// metric is dropped. Nil sub-collectors are skipped.
func (r *RealCollector) latestPositioning(ctx context.Context) []pluginrunner.Snapshot {
	var out []pluginrunner.Snapshot
	type latestFn struct {
		metric, provider string
		enabled          bool
		fetch            func() (float64, time.Time, error)
	}
	for _, f := range []latestFn{
		{MetricETFNetFlow, providerTFTC, r.etfFlows != nil, func() (float64, time.Time, error) { return r.etfFlows.GetLatestFlow(ctx) }},
		{MetricOKXOpenInterest, providerOKX, r.okx != nil, func() (float64, time.Time, error) { return r.okx.GetLatestOpenInterest(ctx) }},
		{MetricOKXFundingRate, providerOKX, r.okx != nil, func() (float64, time.Time, error) { return r.okx.GetLatestFundingRate(ctx) }},
		{MetricDVOL, providerDeribit, r.deribit != nil, func() (float64, time.Time, error) { return r.deribit.GetLatestDVOL(ctx) }},
	} {
		if !f.enabled {
			continue
		}
		v, ts, err := f.fetch()
		if err != nil {
			log.Warn().Err(err).Str("metric", f.metric).Str("provider", f.provider).Msg("fetch failed; dropping metric")
			continue
		}
		out = append(out, formatSeries(f.metric, f.provider, []time.Time{ts}, []float64{v}, time.Now())...)
	}
	return out
}

// historyPositioning backfills the ETF-flow and derivatives metrics. Partial
// histories (a later page failed) are kept.
func (r *RealCollector) historyPositioning(ctx context.Context, start, end time.Time) []pluginrunner.Snapshot {
	var out []pluginrunner.Snapshot
	type historyFn struct {
		metric, provider string
		enabled          bool
		maxGap           time.Duration
		fetch            func() ([]time.Time, []float64, error)
	}
	for _, f := range []historyFn{
		// ETF flows skip weekends and US holidays: allow a 4-day gap.
		{MetricETFNetFlow, providerTFTC, r.etfFlows != nil, 96 * time.Hour, func() ([]time.Time, []float64, error) { return r.etfFlows.GetFlowHistory(ctx, start, end) }},
		{MetricOKXOpenInterest, providerOKX, r.okx != nil, 48 * time.Hour, func() ([]time.Time, []float64, error) { return r.okx.GetOpenInterestHistory(ctx, start, end) }},
		{MetricOKXFundingRate, providerOKX, r.okx != nil, 16 * time.Hour, func() ([]time.Time, []float64, error) { return r.okx.GetFundingRateHistory(ctx, start, end) }},
		{MetricDVOL, providerDeribit, r.deribit != nil, 48 * time.Hour, func() ([]time.Time, []float64, error) { return r.deribit.GetDVOLHistory(ctx, start, end) }},
	} {
		if !f.enabled {
			continue
		}
		times, values, err := f.fetch()
		if err != nil {
			log.Warn().Err(err).Str("metric", f.metric).Str("provider", f.provider).Int("partial_samples", len(times)).Msg("history fetch failed")
		}
		if len(times) == 0 {
			continue
		}
		s := formatSeries(f.metric, f.provider, times, values, time.Now())
		detectGaps(f.metric, s, start, end, f.maxGap)
		out = append(out, s...)
	}
	return out
}
