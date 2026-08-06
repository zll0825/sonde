package detector

import (
	"fmt"
	"strconv"
)

// Summarize renders a Trigger's evidence as a one-line human-readable
// explanation (Chinese, matching the dashboard locale). It is stored in
// alerts.summary and forwarded to notification channels so the reader can
// understand WHY the alert fired without opening the evidence JSON.
//
// Unknown detectors or missing evidence keys degrade gracefully to a generic
// sentence — a notification must never fail because evidence is incomplete.
func Summarize(t *Trigger) string {
	switch t.DetectorName {
	case "threshold":
		return summarizeThreshold(t.Evidence)
	case "percentile":
		return summarizePercentile(t.Evidence)
	case "trend":
		return summarizeTrend(t.Evidence)
	default:
		return fmt.Sprintf("规则「%s」在指标 %s 上触发", t.RuleName, t.MetricID)
	}
}

func summarizeThreshold(ev map[string]interface{}) string {
	cur, curOK := evNumber(ev, "current_value")
	thr, thrOK := evNumber(ev, "threshold")
	op, _ := ev["operator"].(string)
	if !curOK || !thrOK || op == "" {
		return "当前值越过配置阈值"
	}
	msg := fmt.Sprintf("当前值 %s 已%s阈值 %s", evFormat(cur), operatorWord(op), evFormat(thr))
	if n, ok := evNumber(ev, "consecutive"); ok && n > 1 {
		msg += fmt.Sprintf("（连续 %d 个数据点）", int(n))
	}
	return msg
}

func summarizePercentile(ev map[string]interface{}) string {
	cur, curOK := evNumber(ev, "current_value")
	thr, thrOK := evNumber(ev, "percentile_threshold")
	pct, pctOK := evNumber(ev, "percentile")
	if !curOK || !thrOK || !pctOK {
		return "当前值处于历史分布的极端区间"
	}
	msg := fmt.Sprintf("当前值 %s 超过历史 P%s 分位（阈值 %s）", evFormat(cur), evFormat(pct), evFormat(thr))
	if n, ok := evNumber(ev, "sample_size"); ok {
		msg += fmt.Sprintf("，样本 %d 个", int(n))
	}
	return msg
}

func summarizeTrend(ev map[string]interface{}) string {
	dir, _ := ev["direction"].(string)
	cur, curOK := evNumber(ev, "current_value")
	n, nOK := evNumber(ev, "consecutive")
	if dir == "" || !curOK || !nOK {
		return "指标出现持续单向变动"
	}
	msg := fmt.Sprintf("指标连续 %d 期%s，当前值 %s", int(n), directionWord(dir), evFormat(cur))
	if start, ok := evNumber(ev, "window_start_value"); ok {
		msg += fmt.Sprintf("（区间起点 %s）", evFormat(start))
	}
	return msg
}

func operatorWord(op string) string {
	switch op {
	case ">", ">=":
		return "高于"
	case "<", "<=":
		return "低于"
	default:
		return "越过"
	}
}

func directionWord(dir string) string {
	switch dir {
	case "up", "increasing":
		return "上升"
	case "down", "decreasing":
		return "下降"
	default:
		return dir
	}
}

// evNumber extracts a numeric evidence value. Evidence maps round-trip through
// JSON, so numbers may arrive as float64, int, or json.Number-style strings.
func evNumber(ev map[string]interface{}, key string) (float64, bool) {
	switch v := ev[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// evFormat prints a value compactly: integers without decimals, everything
// else with up to 2 decimal places (trailing zeros trimmed by %g fallback).
func evFormat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}
