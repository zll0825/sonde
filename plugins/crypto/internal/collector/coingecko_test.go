package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testProviderConfig(name string) provider.Config {
	return provider.Config{
		ProviderName: name,
		Timeout:      50 * time.Millisecond,
		RPS:          100,
		Burst:        100,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
}

// newTestRealCollector creates a RealCollector with mocked HTTP clients.
func newTestRealCollector(coingeckoRT, mempoolRT, blockchainRT roundTripFunc) *RealCollector {
	r := &RealCollector{
		coingeckoClient: provider.NewSafeHTTPClientWithHTTPClient(testProviderConfig("coingecko-test"), &http.Client{Transport: coingeckoRT}),
		mempoolClient:   provider.NewSafeHTTPClientWithHTTPClient(testProviderConfig("mempool-test"), &http.Client{Transport: mempoolRT}),
		blockchainInfo:  &BlockchainInfoCollector{client: provider.NewSafeHTTPClientWithHTTPClient(testProviderConfig("blockchain-test"), &http.Client{Transport: blockchainRT})},
	}
	return r
}

func TestRealCollector_ClassifiesEachProviderSnapshot(t *testing.T) {
	r := newTestRealCollector(
		// CoinGecko
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"bitcoin":{"usd":70000}}`)), Header: make(http.Header)}, nil
		}),
		// Mempool
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"currentHashrate":620000000000000000000}`)), Header: make(http.Header)}, nil
		}),
		// Blockchain.com
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"values":[{"x":1700000000,"y":350000}]}`)), Header: make(http.Header)}, nil
		}),
	)

	snaps, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	classes := make(map[string]model.SourceClass, len(snaps))
	for _, snap := range snaps {
		classes[snap.MetricID] = snap.SourceClass
	}
	// All metrics should be real-classified now (no exchange_balance mock)
	if classes["btc.ass.price"] != model.SourceClassReal {
		t.Errorf("price class = %q, want real", classes["btc.ass.price"])
	}
	if classes["btc.ass.hash_rate"] != model.SourceClassReal {
		t.Errorf("hash_rate class = %q, want real", classes["btc.ass.hash_rate"])
	}
	if classes["btc.ass.tx_count"] != model.SourceClassReal {
		t.Errorf("tx_count class = %q, want real", classes["btc.ass.tx_count"])
	}
	// exchange_balance must NOT be present (retired)
	if _, ok := classes["btc.ass.exchange_balance"]; ok {
		t.Error("exchange_balance should not be present (retired metric)")
	}
}

func TestRealCollector_ProviderTimeoutDegradesAndRecovers(t *testing.T) {
	coinGeckoAttempts := 0
	r := newTestRealCollector(
		// CoinGecko with initial failure then recovery
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			coinGeckoAttempts++
			if coinGeckoAttempts == 1 {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"bitcoin":{"usd":70000}}`)), Header: make(http.Header)}, nil
		}),
		// Mempool (always succeeds)
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"currentHashrate":620000000000000000000}`)), Header: make(http.Header)}, nil
		}),
		// Blockchain.com (always succeeds)
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"values":[{"x":1700000000,"y":350000}]}`)), Header: make(http.Header)}, nil
		}),
	)

	first, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("first GetSnapshots: %v", err)
	}
	firstMetrics := make(map[string]bool, len(first))
	for _, snap := range first {
		firstMetrics[snap.MetricID] = true
	}
	if firstMetrics["btc.ass.price"] {
		t.Fatal("timed-out price snapshot was not dropped")
	}
	// Should have hash_rate and tx_count remaining
	if !firstMetrics["btc.ass.hash_rate"] || !firstMetrics["btc.ass.tx_count"] {
		t.Fatalf("partial collection metrics = %v, want hash rate and tx_count", firstMetrics)
	}

	second, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("recovery GetSnapshots: %v", err)
	}
	for _, snap := range second {
		if snap.MetricID == "btc.ass.price" && snap.Provider == providerCoinGecko {
			return
		}
	}
	t.Fatal("CoinGecko price did not recover on the next collection")
}

func TestMockCollector_ClassifiesEverySnapshot(t *testing.T) {
	snaps, err := (Mock{}).GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) == 0 {
		t.Fatal("GetSnapshots returned no snapshots")
	}
	for _, snap := range snaps {
		if snap.SourceClass != model.SourceClassMock {
			t.Errorf("snapshot %q class = %q, want mock", snap.MetricID, snap.SourceClass)
		}
	}
}

// TestRealCollectorWindow_ProducesRealHistory verifies that backfill uses
// real historical data, not mock. Exchange balance (retired) no longer appears.
func TestRealCollectorWindow_ProducesRealHistory(t *testing.T) {
	end := time.Now()
	start := end.AddDate(0, 0, -10)

	r := newTestRealCollector(
		// CoinGecko - not used in window
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"bitcoin":{"usd":70000}}`)), Header: make(http.Header)}, nil
		}),
		// Mempool history
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hashrates":[{"timestamp":1700000000,"avgHashrate":620000000000000000000}]}`)), Header: make(http.Header)}, nil
		}),
		// Blockchain.com history
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"values":[{"x":1700000000,"y":350000},{"x":1700086400,"y":360000}]}`)), Header: make(http.Header)}, nil
		}),
	)

	snaps, err := r.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	if len(snaps) == 0 {
		t.Fatal("expected backfill data, got none")
	}
	for _, s := range snaps {
		if s.SourceClass == model.SourceClassMock {
			t.Errorf("backfill for %q has mock source class — should be real", s.MetricID)
		}
		if s.MetricID == "btc.ass.exchange_balance" {
			t.Error("retired metric btc.ass.exchange_balance should not appear in backfill")
		}
	}
}

// TestFetchHistoricalPrices_AutoSplitWindow verifies that fetchHistoricalPrices
// automatically splits a range larger than maxHistoricalWindowSize into multiple
// windowed requests and aggregates the results.
func TestFetchHistoricalPrices_AutoSplitWindow(t *testing.T) {
	// Total range: 800 days (forces 3 windows: 365 + 365 + 70)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -800)

	windowCalls := 0
	r := newTestRealCollector(
		// CoinGecko market_chart/range — returns one unique timestamp per call
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			windowCalls++
			// Each window gets a unique timestamp so dedup doesn't collapse boundaries
			ts := time.Date(2024, 5, 23, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (windowCalls-1)*30)
			tsMs := ts.Unix() * 1000
			body := fmt.Sprintf(`{"prices":[[%d,70000],[%d,71000]]}`, tsMs, tsMs+3600000)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}),
		// Mempool (empty — not relevant)
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hashrates":[]}`)), Header: make(http.Header)}, nil
		}),
		// Blockchain.com (empty — not relevant)
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"values":[]}`)), Header: make(http.Header)}, nil
		}),
	)

	// The inter-window delay is 1500ms in production; this test uses a 30s context timeout
	// which is sufficient for 2 inter-window pauses (2 × 1.5s = 3s).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snaps, err := r.GetSnapshotsForWindow(ctx, start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}

	// Should have made 3 calls (365 + 365 + 70 days)
	if windowCalls != 3 {
		t.Errorf("expected 3 windowed calls for 800-day range, got %d", windowCalls)
	}

	// Verify we got BTC price snapshots in the result
	priceCount := 0
	for _, s := range snaps {
		if s.MetricID == "btc.ass.price" {
			priceCount++
		}
	}
	if priceCount == 0 {
		t.Fatal("expected at least one btc.ass.price snapshot from historical backfill")
	}
	t.Logf("CoinGecko historical backfill produced %d price snapshots", priceCount)
}

// TestFetchHistoricalPrices_ContextCancel verifies that fetchHistoricalPrices
// respects context cancellation and returns early.
func TestFetchHistoricalPrices_ContextCancel(t *testing.T) {
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -800) // >1 window

	callCount := 0
	r := newTestRealCollector(
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount > 1 {
				// After first successful window, block to simulate rate limiting
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			// First window returns some data with a unique timestamp
			tsMs := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).Unix() * 1000
			body := fmt.Sprintf(`{"prices":[[%d,70000],[%d,71000]]}`, tsMs, tsMs+3600000)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}),
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hashrates":[]}`)), Header: make(http.Header)}, nil
		}),
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"values":[]}`)), Header: make(http.Header)}, nil
		}),
	)

	// Use a short timeout context so the second window hits cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// This may or may not error depending on timing, but should not hang
	_, _ = r.GetSnapshotsForWindow(ctx, start, end)
	// Just verifying no infinite hang — test passes if we get here
}

func TestRealCollector_CoinGeckoDemoKeyHeader(t *testing.T) {
	for _, tc := range []struct {
		name, key string
	}{
		{"with key", "demo-key"},
		{"keyless", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			r := newTestRealCollector(
				roundTripFunc(func(req *http.Request) (*http.Response, error) {
					got = append(got, req.Header.Get("x-cg-demo-api-key"))
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"bitcoin":{"usd":70000}}`)), Header: make(http.Header)}, nil
				}),
				nil, nil,
			)
			r.coingeckoKey = tc.key

			if _, err := r.fetchPrice(context.Background()); err != nil {
				t.Fatalf("fetchPrice: %v", err)
			}
			if len(got) != 1 || got[0] != tc.key {
				t.Fatalf("x-cg-demo-api-key = %q, want %q", got, tc.key)
			}
		})
	}
}
