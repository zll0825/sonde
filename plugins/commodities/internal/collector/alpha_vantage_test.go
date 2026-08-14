package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/provider"
)

var alphaTestNow = time.Date(2026, 8, 14, 3, 5, 0, 0, time.UTC)

func alphaFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

func alphaResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func newAlphaTestCollector(rt roundTripFunc, now time.Time, configure func(*provider.Config)) *AlphaVantageGoldCollector {
	cfg := provider.AlphaVantageConfig()
	cfg.ProviderName = "alpha-vantage-gold-test"
	cfg.Timeout = time.Second
	cfg.RPS = 100
	cfg.Burst = 10
	cfg.MaxRetries = 0
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = time.Millisecond
	if configure != nil {
		configure(&cfg)
	}
	return &AlphaVantageGoldCollector{
		apiKey: "fixture-key",
		client: provider.NewSafeHTTPClientWithHTTPClient(cfg, &http.Client{Transport: rt}),
		now:    func() time.Time { return now },
	}
}

func TestAlphaVantageGoldCollector_SpotProvenance(t *testing.T) {
	fixture := alphaFixture(t, "alpha_vantage_spot.json")
	collector := newAlphaTestCollector(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if query.Get("function") != "GOLD_SILVER_SPOT" || query.Get("symbol") != "GOLD" || query.Get("apikey") != "fixture-key" {
			t.Fatalf("unexpected query: function=%q symbol=%q key_set=%t", query.Get("function"), query.Get("symbol"), query.Get("apikey") != "")
		}
		return alphaResponse(fixture), nil
	}, alphaTestNow, nil)

	snapshots, err := collector.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snapshots))
	}
	got := snapshots[0]
	wantTime := time.Date(2026, 8, 14, 3, 4, 5, 0, time.UTC)
	if got.MetricID != goldMetricID || got.Value != 1234.56 ||
		!got.Timestamp.Equal(wantTime) || !got.FetchedAt.Equal(alphaTestNow) ||
		got.Provider != providerAlphaVantage || got.SourceClass != model.SourceClassReal ||
		got.Grade != "realtime" {
		t.Fatalf("spot snapshot = %+v", got)
	}
}

func TestAlphaVantageGoldCollector_HistoryFiltersSortsDeduplicatesAndRecordsCoverage(t *testing.T) {
	fixture := alphaFixture(t, "alpha_vantage_history.json")
	collector := newAlphaTestCollector(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if query.Get("function") != "GOLD_SILVER_HISTORY" || query.Get("interval") != "daily" {
			t.Fatalf("unexpected history query: %v", query)
		}
		return alphaResponse(fixture), nil
	}, alphaTestNow, nil)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	snapshots, err := collector.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	if len(snapshots) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(snapshots))
	}
	for i, snapshot := range snapshots {
		if i > 0 && !snapshots[i-1].Timestamp.Before(snapshot.Timestamp) {
			t.Fatalf("history is not strictly ascending: %+v", snapshots)
		}
		if snapshot.Provider != providerAlphaVantage ||
			snapshot.SourceClass != model.SourceClassReal || snapshot.Grade != "delayed" ||
			!snapshot.FetchedAt.Equal(alphaTestNow) {
			t.Fatalf("history provenance = %+v", snapshot)
		}
	}
	if snapshots[1].Value != 1202.30 {
		t.Fatalf("duplicate-date winner = %v, want first provider row", snapshots[1].Value)
	}
	coverage, ok := collector.LastCoverage(goldMetricID)
	if !ok || coverage.SampleCount != 3 || coverage.HasGaps ||
		!coverage.ActualStart.Equal(snapshots[0].Timestamp) ||
		!coverage.ActualEnd.Equal(snapshots[2].Timestamp) {
		t.Fatalf("coverage = %+v, %t", coverage, ok)
	}
}

func TestNewAlphaVantageGoldCollector_RequiresNonBlankAPIKey(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			t.Setenv("ALPHAVANTAGE_API_KEY", value)
			if _, err := NewAlphaVantageGoldCollector(); err == nil {
				t.Fatal("NewAlphaVantageGoldCollector returned nil error")
			}
		})
	}
}

func TestAlphaVantageGoldCollector_RejectsProviderPayloads(t *testing.T) {
	tests := map[string]string{
		"empty":          "",
		"malformed":      "{",
		"information":    `{"Information":"plan message"}`,
		"rate limit":     `{"Note":"request limit reached"}`,
		"provider error": `{"Error Message":"invalid function"}`,
		"wrong nominal":  `{"nominal":"EURUSD","timestamp":"2026-08-14 03:04:05","price":"1234.56"}`,
		"bad timestamp":  `{"nominal":"XAUUSD","timestamp":"not-a-time","price":"1234.56"}`,
		"future":         `{"nominal":"XAUUSD","timestamp":"2026-08-14 03:20:00","price":"1234.56"}`,
		"stale":          `{"nominal":"XAUUSD","timestamp":"2026-08-01 03:04:05","price":"1234.56"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
				return alphaResponse(body), nil
			}, alphaTestNow, nil)
			if snapshots, err := collector.GetSnapshots(context.Background()); err == nil || len(snapshots) != 0 {
				t.Fatalf("GetSnapshots() = %+v, %v; want error", snapshots, err)
			}
		})
	}
}

func TestAlphaVantageGoldCollector_RejectsInvalidPrices(t *testing.T) {
	for _, value := range []string{"", "abc", "0", "-1", "NaN", "+Inf"} {
		t.Run(value, func(t *testing.T) {
			body := fmt.Sprintf(`{"nominal":"XAUUSD","timestamp":"2026-08-14 03:04:05","price":%q}`, value)
			collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
				return alphaResponse(body), nil
			}, alphaTestNow, nil)
			if _, err := collector.GetSnapshots(context.Background()); err == nil {
				t.Fatal("GetSnapshots returned nil error")
			}
		})
	}
}

func TestAlphaVantageGoldCollector_RetriesRateLimitResponse(t *testing.T) {
	fixture := alphaFixture(t, "alpha_vantage_spot.json")
	attempts := 0
	collector := newAlphaTestCollector(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"Note":"slow down"}`)),
				Header:     http.Header{"Retry-After": []string{"1"}},
			}, nil
		}
		return alphaResponse(fixture), nil
	}, alphaTestNow, func(cfg *provider.Config) {
		cfg.MaxRetries = 1
		cfg.MaxDelay = time.Millisecond
	})

	if _, err := collector.GetSnapshots(context.Background()); err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("wire attempts = %d, want 2", attempts)
	}
}

func TestAlphaVantageGoldCollector_HTTPFailureIsBoundedAndCredentialSafe(t *testing.T) {
	secret := "credential-must-not-escape"
	collector := newAlphaTestCollector(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	}, alphaTestNow, nil)
	collector.apiKey = secret

	_, err := collector.GetSnapshots(context.Background())
	if err == nil {
		t.Fatal("GetSnapshots returned nil error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), alphaVantageEndpoint) {
		t.Fatalf("credentialed transport error leaked: %q", err)
	}
	if len(err.Error()) > 512 {
		t.Fatalf("error length = %d", len(err.Error()))
	}
}

func TestAlphaVantageGoldCollector_Timeout(t *testing.T) {
	collector := newAlphaTestCollector(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}, alphaTestNow, func(cfg *provider.Config) {
		cfg.Timeout = 20 * time.Millisecond
	})
	if _, err := collector.GetSnapshots(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetSnapshots error = %v, want deadline exceeded", err)
	}
}

func TestAlphaVantageGoldCollector_RejectsHTTPFailureAndOversizedResponse(t *testing.T) {
	t.Run("HTTP status", func(t *testing.T) {
		collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 2048))), Header: make(http.Header)}, nil
		}, alphaTestNow, nil)
		_, err := collector.GetSnapshots(context.Background())
		if err == nil || strings.Contains(err.Error(), strings.Repeat("x", 32)) {
			t.Fatalf("HTTP error = %q", err)
		}
	})

	t.Run("response limit", func(t *testing.T) {
		collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
			return alphaResponse(strings.Repeat("x", alphaVantageResponseLimit+1)), nil
		}, alphaTestNow, nil)
		_, err := collector.GetSnapshots(context.Background())
		if !errors.Is(err, provider.ErrResponseTooLarge) {
			t.Fatalf("oversized response error = %v", err)
		}
	})
}

func TestAlphaVantageGoldCollector_RejectsInvalidHistoryWindows(t *testing.T) {
	wireCalls := 0
	collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
		wireCalls++
		return alphaResponse(alphaFixture(t, "alpha_vantage_history.json")), nil
	}, alphaTestNow, nil)
	start := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(-time.Hour)); err == nil {
		t.Fatal("reversed window returned nil error")
	}
	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, start.Add(MaxGoldHistoricalWindow+time.Hour)); err == nil {
		t.Fatal("oversized window returned nil error")
	}
	if wireCalls != 0 {
		t.Fatalf("invalid windows made %d wire calls", wireCalls)
	}
}

func TestAlphaVantageGoldCollector_HistoryRejectsEmptyAndMalformedRows(t *testing.T) {
	tests := map[string]string{
		"empty data":    `{"nominal":"XAUUSD","data":[]}`,
		"bad date":      `{"nominal":"XAUUSD","data":[{"date":"bad","price":"1200"}]}`,
		"invalid price": `{"nominal":"XAUUSD","data":[{"date":"2026-08-13","price":"0"}]}`,
		"wrong nominal": `{"nominal":"GOLDUSD","data":[{"date":"2026-08-13","price":"1200"}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
				return alphaResponse(body), nil
			}, alphaTestNow, nil)
			start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
			if _, err := collector.GetSnapshotsForWindow(context.Background(), start, alphaTestNow); err == nil {
				t.Fatal("GetSnapshotsForWindow returned nil error")
			}
		})
	}
}

func TestAlphaVantageGoldCollector_HistoryReportsTimelineGaps(t *testing.T) {
	body := `{"nominal":"XAUUSD","data":[` +
		`{"date":"2026-08-14","price":"1204"},` +
		`{"date":"2026-08-10","price":"1200"}]}`
	collector := newAlphaTestCollector(func(*http.Request) (*http.Response, error) {
		return alphaResponse(body), nil
	}, alphaTestNow, nil)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	if _, err := collector.GetSnapshotsForWindow(context.Background(), start, alphaTestNow); err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	coverage, ok := collector.LastCoverage(goldMetricID)
	if !ok || !coverage.HasGaps || coverage.GapCount != 1 {
		t.Fatalf("gap coverage = %+v, %t", coverage, ok)
	}
}
