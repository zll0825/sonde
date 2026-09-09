package liquidity

import (
	"testing"
	"time"
)

func TestWLRRAOLAcceptedAndWLRRALFails(t *testing.T) {
	got, err := ScaleToUSD("WLRRAOL", 675)
	if err != nil {
		t.Fatal(err)
	}
	if got != 675*1e6 {
		t.Fatalf("WLRRAOL scale = %v, want %v", got, 675*1e6)
	}
	if _, err := ScaleToUSD("WLRRAL", 675); err == nil {
		t.Fatal("WLRRAL must fail")
	}
}

func TestWALCLScaleIsMillions(t *testing.T) {
	got, err := ScaleToUSD("WALCL", 7_200_000)
	if err != nil {
		t.Fatal(err)
	}
	if got != 7_200_000*1e6 {
		t.Fatalf("WALCL scale = %v, want millions ×1e6", got)
	}
}

func TestRRPONTSYDScaleIsBillions(t *testing.T) {
	got, err := ScaleToUSD("RRPONTSYD", 0.675)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0.675*1e9 {
		t.Fatalf("RRPONTSYD scale = %v, want billions ×1e9", got)
	}
}

func TestNetLiquidityWednesdayPath(t *testing.T) {
	walcl, err := ScaleToUSD("WALCL", 7_000_000)
	if err != nil {
		t.Fatal(err)
	}
	tga, err := ScaleToUSD("WDTGAL", 800_000)
	if err != nil {
		t.Fatal(err)
	}
	rrp, err := ScaleToUSD("WLRRAOL", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	got := NetLiquidityUSD(walcl, tga, rrp)
	want := (7_000_000 - 800_000 - 200_000) * 1e6
	if got != want {
		t.Fatalf("net liquidity = %v, want %v", got, want)
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestIORBForwardMatchAcrossEffectiveDateBoundary(t *testing.T) {
	iorbs := []RatePoint{
		{Date: date(2024, 9, 18), Value: 5.40},
		{Date: date(2024, 9, 19), Value: 4.90},
		{Date: date(2024, 9, 25), Value: 4.90}, // FRED 7-day lookahead
	}

	got, ok := MatchIORB(date(2024, 9, 18), iorbs)
	if !ok || got.Value != 5.40 {
		t.Fatalf("SOFR 2024-09-18 matched %+v ok=%v, want 5.40", got, ok)
	}
	got, ok = MatchIORB(date(2024, 9, 19), iorbs)
	if !ok || got.Value != 4.90 {
		t.Fatalf("SOFR 2024-09-19 matched %+v ok=%v, want 4.90", got, ok)
	}

	if got := SpreadBP(5, 4); got != 100 {
		t.Fatalf("spread = %v bp, want 100", got)
	}
}

func TestIORBDoesNotPairFutureNotYetEffective(t *testing.T) {
	iorbs := []RatePoint{
		{Date: date(2024, 7, 31), Value: 5.40},
		{Date: date(2024, 9, 19), Value: 4.90},
	}
	got, ok := MatchIORB(date(2024, 9, 18), iorbs)
	if !ok || got.Value != 5.40 {
		t.Fatalf("SOFR 2024-09-18 must keep 5.40, got %+v ok=%v", got, ok)
	}
}
