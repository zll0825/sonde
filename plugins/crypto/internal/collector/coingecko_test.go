package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRealCollector_ClassifiesEachProviderSnapshot(t *testing.T) {
	r := NewRealCollector()
	r.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"currentHashrate":620000000000000000000}`
		if strings.Contains(req.URL.Host, "coingecko") {
			body = `{"bitcoin":{"usd":70000}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	snaps, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	classes := make(map[string]model.SourceClass, len(snaps))
	for _, snap := range snaps {
		classes[snap.MetricID] = snap.SourceClass
	}
	if classes["btc.ass.price"] != model.SourceClassReal || classes["btc.ass.hash_rate"] != model.SourceClassReal {
		t.Errorf("upstream classes = %+v, want price/hash_rate real", classes)
	}
	if classes["btc.ass.exchange_balance"] != model.SourceClassMock {
		t.Errorf("exchange balance class = %q, want mock", classes["btc.ass.exchange_balance"])
	}
}

func TestRealCollector_ProviderTimeoutDegradesAndRecovers(t *testing.T) {
	r := NewRealCollector()
	coinGeckoAttempts := 0
	r.client = &http.Client{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Host, "coingecko") {
				coinGeckoAttempts++
				if coinGeckoAttempts == 1 {
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"bitcoin":{"usd":70000}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"currentHashrate":620000000000000000000}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

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
	if !firstMetrics["btc.ass.hash_rate"] || !firstMetrics["btc.ass.exchange_balance"] {
		t.Fatalf("partial collection metrics = %v, want hash rate and exchange balance", firstMetrics)
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

// Regression: RealCollector backfill must NOT fabricate price / hash-rate
// history from the Mock random walk — GetObservations has no provider filter,
// so fake baselines would drive the percentile/trend detectors into bogus
// alerts against real live values. Only exchange_balance (mock in the live
// path too) may come from Mock.
func TestRealCollectorWindow_OnlyExchangeBalance(t *testing.T) {
	// Arrange
	r := NewRealCollector()
	end := time.Now()
	start := end.AddDate(0, 0, -10)

	// Act
	snaps, err := r.GetSnapshotsForWindow(context.Background(), start, end)

	// Assert
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	if len(snaps) == 0 {
		t.Fatal("expected exchange_balance history, got none")
	}
	for _, s := range snaps {
		if s.MetricID != "btc.ass.exchange_balance" {
			t.Errorf("backfill fabricated history for %q — only exchange_balance is allowed", s.MetricID)
		}
		if s.SourceClass != model.SourceClassMock {
			t.Errorf("backfill source class = %q, want mock", s.SourceClass)
		}
	}
}
