package event

import "testing"

func TestNewDetectionRequestStableAcrossReplay(t *testing.T) {
	a := NewDetectionRequest("btc.price", "mtr_1", "plg_crypto", "coingecko", "labels", "delayed", 123)
	b := NewDetectionRequest("btc.price", "mtr_1", "plg_crypto", "coingecko", "labels", "delayed", 123)
	if a != b {
		t.Fatalf("replayed observation produced different requests: %#v != %#v", a, b)
	}
}

func TestNewDetectionRequestChangesForCorrection(t *testing.T) {
	delayed := NewDetectionRequest("btc.price", "mtr_1", "plg_crypto", "coingecko", "labels", "delayed", 123)
	revised := NewDetectionRequest("btc.price", "mtr_1", "plg_crypto", "coingecko", "labels", "revised", 123)
	if delayed.DetectionKey == revised.DetectionKey {
		t.Fatal("higher-grade correction must receive distinct detection work")
	}
}
