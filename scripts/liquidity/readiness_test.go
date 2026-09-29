package liquidity

import "testing"

func TestReadinessGates(t *testing.T) {
	cases := []struct {
		freq         string
		n            int
		gated, ready bool
	}{
		{"daily", 59, true, false},
		{"daily", 60, true, true},
		{"weekly", 51, true, false},
		{"weekly", 52, true, true},
		{"monthly", 36, true, true},
		{"quarterly", 1000, false, false},
	}
	for _, c := range cases {
		_, gated, ready := Readiness(c.freq, c.n)
		if gated != c.gated || ready != c.ready {
			t.Errorf("Readiness(%s,%d) = gated %v ready %v", c.freq, c.n, gated, ready)
		}
	}
}

func TestVIXTermStructure(t *testing.T) {
	got, err := VIXTermStructure(18, 15)
	if err != nil || got != 1.2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := VIXTermStructure(18, 0); err == nil {
		t.Fatal("want error for zero VIX")
	}
}
