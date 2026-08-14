package provider

import "time"

// Provider presets for documented public/free APIs.
// Each preset reflects conservative free-tier limits to avoid bans.

// CoinGeckoConfig returns config for CoinGecko's free public API.
// Free tier: 10-30 calls/min (varies), 429 on breach.
func CoinGeckoConfig() Config {
	return Config{
		ProviderName: "coingecko",
		Timeout:      10 * time.Second,
		RPS:          0.5, // 1 req / 2 sec ≈ 30/min (conservative)
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    2 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// MempoolConfig returns config for mempool.space public API.
// No official rate limit; conservative polling recommended.
func MempoolConfig() Config {
	return Config{
		ProviderName: "mempool_space",
		Timeout:      10 * time.Second,
		RPS:          0.25, // 1 req / 4 sec (very conservative)
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    3 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        60 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// YahooFinanceConfig returns config for Yahoo Finance chart API.
// Free tier: ~2000 requests/hour per IP unofficially; conservative polling.
func YahooFinanceConfig() Config {
	return Config{
		ProviderName: "yahoo_finance",
		Timeout:      15 * time.Second,
		RPS:          0.2, // 1 req / 5 sec ≈ 720/hour
		Burst:        3,
		MaxRetries:   3,
		BaseDelay:    2 * time.Second,
		MaxDelay:     30 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        60 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// FREDConfig returns config for FRED API (requires API key).
// Free tier: ~120 requests/hour.
func FREDConfig() Config {
	return Config{
		ProviderName: "fred",
		Timeout:      15 * time.Second,
		RPS:          0.03, // 1 req / 33 sec ≈ 108/hour (under 120 limit)
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    5 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    3,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// AlphaVantageConfig returns config for Alpha Vantage's free API tier.
// The free tier permits 25 calls/day. Callers must pair this preset with a
// collection cadence that stays within that daily budget; retries are kept to
// one so transient failures cannot create an unbounded request burst.
func AlphaVantageConfig() Config {
	return Config{
		ProviderName:  "alpha_vantage",
		Timeout:       15 * time.Second,
		RPS:           0.02, // at most one wire attempt per 50 seconds
		Burst:         1,
		MaxRetries:    1,
		BaseDelay:     5 * time.Second,
		MaxDelay:      60 * time.Second,
		JitterRatio:   0.25,
		MaxConcurrent: 1,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    3,
			OpenDuration:        120 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// BlockchainInfoConfig returns config for blockchain.com charts API.
// No official rate limit documented; conservative polling recommended.
func BlockchainInfoConfig() Config {
	return Config{
		ProviderName: "blockchain_com",
		Timeout:      15 * time.Second,
		RPS:          0.2, // 1 req / 5 sec
		Burst:        2,
		MaxRetries:   3,
		BaseDelay:    3 * time.Second,
		MaxDelay:     60 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        60 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}
