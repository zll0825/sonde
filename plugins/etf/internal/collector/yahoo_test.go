package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"sonde/pkg/model"
	"sonde/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestYahooCollector_ClassifiesAllSnapshotsAsReal verifies that all snapshots
// returned by YahooCollector are sourced as real (no mock fallback).
// Synthetic metrics (daily_flow) have been retired; only real-source metrics remain.
func TestYahooCollector_ClassifiesAllSnapshotsAsReal(t *testing.T) {
	testClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"chart":{"result":[{"meta":{"regularMarketPrice":215.5,"regularMarketVolume":5000000}}],"error":null}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	y := &YahooCollector{client: provider.NewSafeHTTPClientWithHTTPClient(provider.YahooFinanceConfig(), testClient)}

	snaps, err := y.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	for _, snap := range snaps {
		if snap.SourceClass != model.SourceClassReal {
			t.Errorf("snapshot %q class = %q, want real", snap.MetricID, snap.SourceClass)
		}
	}
	// Verify we get both price and volume
	found := make(map[string]bool)
	for _, s := range snaps {
		found[s.MetricID] = true
	}
	if !found["gld.ass.price"] {
		t.Error("missing gld.ass.price snapshot")
	}
	if !found["gld.ass.volume"] {
		t.Error("missing gld.ass.volume snapshot")
	}
}
