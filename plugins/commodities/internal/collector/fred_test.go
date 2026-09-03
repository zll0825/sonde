package collector

import (
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

const testFREDBindings = `
provider: fred
latestLookbackDays: 45
series:
  - metric: oil.energy.wti
    series: DCOILWTICO
    frequency: daily
`

func TestNewFREDCollector_RequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	_, err := NewFREDCollector([]byte(testFREDBindings))
	if err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("error = %v", err)
	}
}
