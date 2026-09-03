package collector

import (
	"strings"
	"testing"
)

const testBindings = `
provider: fred
series:
  - metric: us.mkt.ten_year_yield
    series: DGS10
    frequency: daily
`

func TestNewFREDCollector_RequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	_, err := NewFREDCollector([]byte(testBindings))
	if err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewFREDCollector_RequiresBindings(t *testing.T) {
	t.Setenv("FRED_API_KEY", "k")
	_, err := NewFREDCollector([]byte(`provider: fred
series: []
`))
	if err == nil {
		t.Fatal("empty series should fail")
	}
}
