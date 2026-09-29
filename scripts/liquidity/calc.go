// Package liquidity holds the offline net-liquidity and SOFR−IORB formulas.
// These are not registered metrics; go test in the root module is the CI gate.
package liquidity

import (
	"fmt"
	"time"
)

// Wednesday-level net liquidity uses H.4.1 Wednesday Level columns, all in
// millions of USD before ScaleToUSD. WLRRAL is banned (foreign-official heavy).
var wednesdayNetLiquiditySeries = map[string]float64{
	"WALCL":   1e6,
	"WDTGAL":  1e6,
	"WLRRAOL": 1e6,
}

// ScaleToUSD converts a FRED observation to USD.
// RRPONTSYD is billions (×1e9); WALCL-family Wednesday columns are millions (×1e6).
func ScaleToUSD(series string, raw float64) (float64, error) {
	if series == "WLRRAL" {
		return 0, fmt.Errorf("WLRRAL is ~99.85%% foreign official reverse repo; use WLRRAOL")
	}
	if series == "RRPONTSYD" {
		return raw * 1e9, nil
	}
	scale, ok := wednesdayNetLiquiditySeries[series]
	if !ok {
		return 0, fmt.Errorf("series %s is not in the net-liquidity whitelist", series)
	}
	return raw * scale, nil
}

// NetLiquidityUSD is WALCL − WDTGAL − WLRRAOL after each series is in USD.
func NetLiquidityUSD(walcl, wdtgal, wlrraol float64) float64 {
	return walcl - wdtgal - wlrraol
}

// RatePoint is one dated observation used by SOFR−IORB matching.
type RatePoint struct {
	Date  time.Time
	Value float64
}

// MatchIORB returns the IORB rate already in effect on sofrDate.
//
// Each IORB observation is applied forward from its effective date until the
// next change. From SOFR's side that is: latest IORB with Date <= sofrDate.
// FRED publishes IORB about 7 days ahead — those future rows must not pair
// with an earlier SOFR date.
func MatchIORB(sofrDate time.Time, iorbs []RatePoint) (RatePoint, bool) {
	sofrDate = sofrDate.UTC().Truncate(24 * time.Hour)
	var best RatePoint
	found := false
	for _, row := range iorbs {
		d := row.Date.UTC().Truncate(24 * time.Hour)
		if d.After(sofrDate) {
			continue
		}
		if !found || d.After(best.Date) {
			best = RatePoint{Date: d, Value: row.Value}
			found = true
		}
	}
	return best, found
}

// SpreadBP is (sofr − iorb) in basis points.
func SpreadBP(sofr, iorb float64) float64 {
	return (sofr - iorb) * 100
}

func errNonPositive(name string, v float64) error {
	return fmt.Errorf("%s must be positive, got %v", name, v)
}
