package collector

import "testing"

func TestNewRealCollectorDoesNotRequireFREDKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	c := NewRealCollector()
	if c == nil || c.tga == nil || c.nyfed == nil || c.ofr == nil {
		t.Fatal("NewRealCollector must compose three collectors without a FRED key")
	}
}
