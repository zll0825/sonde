package provider

import "time"

// Presets for China-side public data sources used by plugins/cnmacro.
// None publishes a rate limit; all are polled conservatively.

// EastMoneyDatacenterConfig returns config for the East Money datacenter
// JSON API (datacenter-web.eastmoney.com). Unofficial, keyless.
func EastMoneyDatacenterConfig() Config {
	return Config{
		ProviderName: "eastmoney",
		Timeout:      15 * time.Second,
		RPS:          0.5,
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    2 * time.Second,
		MaxDelay:     30 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// ChinaMoneyConfig returns config for CFETS chinamoney.com.cn static data
// files (prr-md.json / prr-chrt.csv).
func ChinaMoneyConfig() Config {
	return Config{
		ProviderName: "cfets",
		Timeout:      15 * time.Second,
		RPS:          0.5,
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    2 * time.Second,
		MaxDelay:     30 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// PBOCConfig returns config for www.pbc.gov.cn HTML pages (statistics
// tables and open-market-operation announcements). The site is a government
// portal; keep request volume low.
func PBOCConfig() Config {
	return Config{
		ProviderName: "pbc",
		Timeout:      20 * time.Second,
		RPS:          0.5,
		Burst:        1,
		MaxRetries:   2,
		BaseDelay:    3 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        300 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}
