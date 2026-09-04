package collector

import (
	fredprov "sonde/pkg/provider/fred"
)

// NewFREDCollector builds the shared FRED collector from bindings.yaml bytes.
func NewFREDCollector(bindingsYAML []byte) (*fredprov.Collector, error) {
	bindings, err := fredprov.LoadBindings(bindingsYAML)
	if err != nil {
		return nil, err
	}
	lookback, err := fredprov.LatestLookbackFromYAML(bindingsYAML)
	if err != nil {
		return nil, err
	}
	return fredprov.NewCollector(fredprov.Options{
		Bindings:       bindings,
		LatestLookback: lookback,
	})
}

// MaxHistoricalWindow re-exports the shared FRED window cap used by RealCollector.
const MaxHistoricalWindow = fredprov.MaxHistoricalWindow
