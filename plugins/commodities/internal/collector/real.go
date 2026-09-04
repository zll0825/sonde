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

// RealCollector composes unchanged FRED WTI/copper collection with Alpha
// Vantage XAUUSD gold. A failed leg does not discard successful sibling data.
type RealCollector struct {
	fred windowedCommodityProvider
	gold windowedCommodityProvider
}

// NewRealCollector requires credentials for both real providers.
func NewRealCollector(fredBindingsYAML []byte) (*RealCollector, error) {
	gold, err := NewAlphaVantageGoldCollector()
	if err != nil {
		return nil, err
	}
	baseCommodities, err := NewFREDCollector(fredBindingsYAML)
	if err != nil {
		return nil, err
	}
	return &RealCollector{fred: baseCommodities, gold: gold}, nil
}

func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	return collectBoth(
		func() ([]pluginrunner.Snapshot, error) { return r.fred.GetSnapshots(ctx) },
		func() ([]pluginrunner.Snapshot, error) { return r.gold.GetSnapshots(ctx) },
	)
}

func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, errors.New("commodities history end precedes start")
	}
	if end.Sub(start) > MaxHistoricalWindow {
		return nil, fmt.Errorf("commodities history window exceeds %s", MaxHistoricalWindow)
	}
	return collectBoth(
		func() ([]pluginrunner.Snapshot, error) { return r.fred.GetSnapshotsForWindow(ctx, start, end) },
		func() ([]pluginrunner.Snapshot, error) { return r.gold.GetSnapshotsForWindow(ctx, start, end) },
	)
}

// LastCoverage delegates coverage lookups to the provider that owns the metric.
func (r *RealCollector) LastCoverage(metricID string) (provider.BackfillCoverage, bool) {
	if metricID == goldMetricID {
		if gold, ok := r.gold.(interface {
			LastCoverage(string) (provider.BackfillCoverage, bool)
		}); ok {
			return gold.LastCoverage(metricID)
		}
		return provider.BackfillCoverage{}, false
	}
	if fred, ok := r.fred.(interface {
		LastCoverage(string) (provider.BackfillCoverage, bool)
	}); ok {
		return fred.LastCoverage(metricID)
	}
	return provider.BackfillCoverage{}, false
}

// CircuitState reports both upstream circuits without hiding a degraded leg.
func (r *RealCollector) CircuitState() string {
	return fmt.Sprintf("fred=%s,alpha_vantage=%s", circuitState(r.fred), circuitState(r.gold))
}

func circuitState(value any) string {
	if state, ok := value.(interface{ CircuitState() string }); ok {
		return state.CircuitState()
	}
	return "unknown"
}

func collectBoth(first, second func() ([]pluginrunner.Snapshot, error)) ([]pluginrunner.Snapshot, error) {
	firstSnapshots, firstErr := first()
	secondSnapshots, secondErr := second()
	if secondErr != nil {
		secondErr = pluginrunner.SummarizeCollectionFailures([]pluginrunner.CollectionFailure{{
			MetricID: goldMetricID,
			Provider: providerAlphaVantage,
			Err:      secondErr,
		}})
	}
	snapshots := append(firstSnapshots, secondSnapshots...)
	switch {
	case firstErr == nil:
		return snapshots, secondErr
	case secondErr == nil:
		return snapshots, firstErr
	default:
		return snapshots, errors.Join(firstErr, secondErr)
	}
}
