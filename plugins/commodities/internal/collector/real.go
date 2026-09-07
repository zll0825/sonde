package collector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

type windowedCommodityProvider interface {
	pluginrunner.Provider
	pluginrunner.WindowedProvider
}

// RealCollector serves WTI and copper from FRED.
//
// 它曾经还合成一条 Alpha Vantage 的 XAUUSD 黄金腿。metal.precious.gold 退役后
// 这条腿没了：它与 etf 的 gld.ass.price 重复跟踪黄金，却独占 Alpha Vantage
// 25 次/天的全部配额。少了这个 provider，本采集器也不再需要 ALPHAVANTAGE_API_KEY。
type RealCollector struct {
	fred windowedCommodityProvider
}

// NewRealCollector requires FRED credentials.
func NewRealCollector(fredBindingsYAML []byte) (*RealCollector, error) {
	baseCommodities, err := NewFREDCollector(fredBindingsYAML)
	if err != nil {
		return nil, err
	}
	return &RealCollector{fred: baseCommodities}, nil
}

func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	return r.fred.GetSnapshots(ctx)
}

func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, errors.New("commodities history end precedes start")
	}
	if end.Sub(start) > MaxHistoricalWindow {
		return nil, fmt.Errorf("commodities history window exceeds %s", MaxHistoricalWindow)
	}
	return r.fred.GetSnapshotsForWindow(ctx, start, end)
}

// LastCoverage delegates coverage lookups to the FRED provider.
func (r *RealCollector) LastCoverage(metricID string) (provider.BackfillCoverage, bool) {
	if fred, ok := r.fred.(interface {
		LastCoverage(string) (provider.BackfillCoverage, bool)
	}); ok {
		return fred.LastCoverage(metricID)
	}
	return provider.BackfillCoverage{}, false
}

// CircuitState reports the FRED circuit.
func (r *RealCollector) CircuitState() string {
	return fmt.Sprintf("fred=%s", circuitState(r.fred))
}

func circuitState(value any) string {
	if state, ok := value.(interface{ CircuitState() string }); ok {
		return state.CircuitState()
	}
	return "unknown"
}
