package detector

import (
	"strings"
	"testing"
)

func TestSummarize_Threshold(t *testing.T) {
	// Arrange
	trg := &Trigger{
		DetectorName: "threshold",
		Evidence: map[string]interface{}{
			"operator":      ">",
			"threshold":     500000000.0,
			"current_value": 653933135.66,
			"consecutive":   1,
		},
	}

	// Act
	out := Summarize(trg)

	// Assert
	for _, want := range []string{"653933135.66", "高于", "500000000"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary %q missing %q", out, want)
		}
	}
}

func TestSummarize_ThresholdConsecutive(t *testing.T) {
	trg := &Trigger{
		DetectorName: "threshold",
		Evidence: map[string]interface{}{
			"operator":      "<",
			"threshold":     100.0,
			"current_value": 90.0,
			"consecutive":   3,
		},
	}

	out := Summarize(trg)

	if !strings.Contains(out, "连续 3 个数据点") {
		t.Errorf("summary %q should mention consecutive points", out)
	}
	if !strings.Contains(out, "低于") {
		t.Errorf("summary %q should say 低于 for < operator", out)
	}
}

func TestSummarize_Percentile(t *testing.T) {
	trg := &Trigger{
		DetectorName: "percentile",
		Evidence: map[string]interface{}{
			"current_value":        119.7,
			"percentile":           99.0,
			"percentile_threshold": 115.2,
			"sample_size":          90,
		},
	}

	out := Summarize(trg)

	for _, want := range []string{"119.70", "P99", "115.20", "样本 90"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary %q missing %q", out, want)
		}
	}
}

func TestSummarize_Trend(t *testing.T) {
	trg := &Trigger{
		DetectorName: "trend",
		Evidence: map[string]interface{}{
			"direction":          "down",
			"consecutive":        5,
			"current_value":      -2749854.43,
			"window_start_value": -100000.0,
		},
	}

	out := Summarize(trg)

	for _, want := range []string{"连续 5 期", "下降", "-2749854.43"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary %q missing %q", out, want)
		}
	}
}

func TestSummarize_UnknownDetectorFallsBack(t *testing.T) {
	trg := &Trigger{
		DetectorName: "mystery",
		RuleName:     "gld_flow_spike",
		MetricID:     "gld.ass.daily_flow",
	}

	out := Summarize(trg)

	if !strings.Contains(out, "gld_flow_spike") || !strings.Contains(out, "gld.ass.daily_flow") {
		t.Errorf("fallback summary %q should carry rule + metric", out)
	}
}

func TestSummarize_MissingEvidenceDegradesGracefully(t *testing.T) {
	for _, name := range []string{"threshold", "percentile", "trend"} {
		trg := &Trigger{DetectorName: name, Evidence: map[string]interface{}{}}
		if out := Summarize(trg); out == "" {
			t.Errorf("%s with empty evidence must still produce a summary", name)
		}
	}
}
