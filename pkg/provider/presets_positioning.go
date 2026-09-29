package provider

import "time"

// Presets for the positioning plugin (CFTC COT, FINRA margin statistics) and
// the crypto derivatives / ETF-flow sources (TFTC, OKX, Deribit).
// Kept in their own file so presets.go stays untouched by parallel work.

func lowRateConfig(name string, rps float64, burst int) Config {
	return Config{
		ProviderName: name,
		Timeout:      20 * time.Second,
		RPS:          rps,
		Burst:        burst,
		MaxRetries:   3,
		BaseDelay:    3 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// CFTCConfig is for the CFTC Socrata API (publicreporting.cftc.gov).
// Anonymous access is throttled and has shown intermittent 403s; an optional
// app token (X-App-Token) raises the limit.
func CFTCConfig() Config { return lowRateConfig("cftc", 0.2, 1) }

// FINRAConfig is for the FINRA margin statistics page and its XLSX download.
func FINRAConfig() Config { return lowRateConfig("finra", 0.2, 1) }

// TFTCConfig is for tftc.io's static bitcoin-etf-flows JSON.
func TFTCConfig() Config { return lowRateConfig("tftc", 0.2, 1) }

// OKXConfig is for OKX v5 public endpoints. OKX allows 20 req/2s per IP on
// these endpoints; we stay at 2 req/s.
func OKXConfig() Config { return lowRateConfig("okx", 2, 2) }

// DeribitConfig is for Deribit v2 public endpoints.
func DeribitConfig() Config { return lowRateConfig("deribit", 1, 2) }
