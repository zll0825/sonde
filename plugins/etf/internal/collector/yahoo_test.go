package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"capital_observatory/pkg/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestYahooCollector_ClassifiesRealAndMockSnapshots(t *testing.T) {
	y := NewYahooCollector()
	y.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"chart":{"result":[{"meta":{"regularMarketPrice":215.5}}],"error":null}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	snaps, err := y.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	for _, snap := range snaps {
		want := model.SourceClassMock
		if snap.MetricID == "gld.ass.price" {
			want = model.SourceClassReal
		}
		if snap.SourceClass != want {
			t.Errorf("snapshot %q class = %q, want %q", snap.MetricID, snap.SourceClass, want)
		}
	}
}
