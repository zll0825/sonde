package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// newTestRealCollector creates a RealCollector with mocked HTTP clients.
func newTestRealCollector(coingeckoRT, mempoolRT, blockchainRT roundTripFunc) *RealCollector {
	r := &RealCollector{
		coingeckoClient: provider.NewSafeHTTPClientWithHTTPClient(provider.CoinGeckoConfig(), &http.Client{Transport: coingeckoRT}),
		mempoolClient:   provider.NewSafeHTTPClientWithHTTPClient(provider.MempoolConfig(), &http.Client{Transport: mempoolRT}),
		blockchainInfo:  &BlockchainInfoCollector{client: provider.NewSafeHTTPClientWithHTTPClient(provider.BlockchainInfoConfig(), &http.Client{Transport: blockchainRT})},
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
