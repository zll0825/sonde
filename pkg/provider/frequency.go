package provider

import "time"

// 频率语义分三层，不要混用：
//
//   FrequencyPeriod  两次观测之间的正常间距（"多久出一条"）
//   LatestLookback   拉取最新一条时要回溯多远（"往回够多远才罩得住已发布的那条"）
//   StaleThreshold   超过多久没有新观测才算真异常（"多久没动静算出事了"）
//
// 采集窗口不等于观测周期：月频序列的观测日期是月初，发布却滞后约六周，
// 30 天窗口对 CPIAUCSL 必然落空。见 fred/collector.go 的 GetSnapshots。
//
// 本文件是仓库内频率映射的唯一真源。internal/core/pluginmgr 另有一份
// frequencyDuration，因跨模块引用会把 Core 与 provider 耦合起来（Core 不
// 懂金融语义是硬约束），本轮不动，记为已知重复。

// 词表取两份历史映射的全集，避免收敛时丢掉 realtime / hourly / quarterly。
const (
	FrequencyRealtime  = "realtime"
	FrequencyHourly    = "hourly"
	FrequencyDaily     = "daily"
	FrequencyWeekly    = "weekly"
	FrequencyMonthly   = "monthly"
	FrequencyQuarterly = "quarterly"
)

// FrequencyPeriod 返回该频率下两次观测之间的标称间距。
// 频率不认识时返回 (0, false)，由调用方自己决定兜底，不在这里替它选。
func FrequencyPeriod(freq string) (time.Duration, bool) {
	switch freq {
	case FrequencyRealtime:
		return time.Minute, true
	case FrequencyHourly:
		return time.Hour, true
	case FrequencyDaily:
		return 24 * time.Hour, true
	case FrequencyWeekly:
		return 7 * 24 * time.Hour, true
	case FrequencyMonthly:
		return 30 * 24 * time.Hour, true
	case FrequencyQuarterly:
		return 91 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// LatestLookback 返回拉取最新一条观测所需的回溯窗口。
//
// 窗口 ≥ 观测周期 + 发布滞后 + 一个采集周期的余量。月频取 120 天是实测倒推：
// CPIAUCSL 的 7 月观测 8-12 才发布，8 月观测要等到 9-11，因此 9 月上旬能拿到
// 的最新观测已陈旧 68 天，30 天窗口取不到任何东西。
//
// 频率不认识时取最保守值（季频），宁可窗口过宽——配合 sort_order=desc&limit=1，
// 窗口偏宽不改变取到的那一条，窗口偏窄却会直接落空。
func LatestLookback(freq string) (window time.Duration, known bool) {
	switch freq {
	case FrequencyRealtime, FrequencyHourly:
		return 24 * time.Hour, true
	case FrequencyDaily:
		return 30 * 24 * time.Hour, true
	case FrequencyWeekly:
		return 60 * 24 * time.Hour, true
	case FrequencyMonthly:
		return 120 * 24 * time.Hour, true
	case FrequencyQuarterly:
		return 400 * 24 * time.Hour, true
	default:
		return 400 * 24 * time.Hour, false
	}
}

// StaleThreshold 返回「窗口内没有观测」还能算正常的最长沉默期。
//
// 用来把两种空结果分开：月频序列在两次发布之间本来就没有新观测，不该计入
// 连续失败；日频序列停更一周则是真异常。宁可判宽也不误报——误报会让首页
// 长期挂红字，最终导致所有红字都被忽略。
func StaleThreshold(freq string) (limit time.Duration, known bool) {
	switch freq {
	case FrequencyRealtime:
		return 15 * time.Minute, true
	case FrequencyHourly:
		return 6 * time.Hour, true
	case FrequencyDaily:
		return 7 * 24 * time.Hour, true
	case FrequencyWeekly:
		return 21 * 24 * time.Hour, true
	case FrequencyMonthly:
		return 75 * 24 * time.Hour, true
	case FrequencyQuarterly:
		return 200 * 24 * time.Hour, true
	default:
		return 200 * 24 * time.Hour, false
	}
}

// PollInterval 返回该频率下两次拉取之间的最短间隔（"多久去问一次上游"）。
//
// 插件的 ticker 仍按 DefaultInterval（约一小时）跳动，它只是调度的最小粒度；
// 每条 series 是否真的去拉，由 PollSchedule 按本函数的结果判定。日频序列一天
// 只发布一次，每小时拉一次纯属浪费配额。间隔取得比观测周期短，是为了把「上游
// 已发布 → 本地可见」的延迟压在可接受范围：日频 ≤6h，周频与月频 ≤1 天。
//
// 频率不认识时返回一小时——与改造前的行为一致，宁可多拉也不漏拉。
func PollInterval(freq string) (interval time.Duration, known bool) {
	switch freq {
	case FrequencyRealtime:
		return 0, true
	case FrequencyHourly:
		return time.Hour, true
	case FrequencyDaily:
		return 6 * time.Hour, true
	case FrequencyWeekly, FrequencyMonthly:
		return 24 * time.Hour, true
	case FrequencyQuarterly:
		return 7 * 24 * time.Hour, true
	default:
		return time.Hour, false
	}
}
