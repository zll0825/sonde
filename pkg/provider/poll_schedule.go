package provider

import (
	"sync"
	"time"
)

// pollSlack 吸收 ticker 抖动与单轮采集耗时：一小时的 tick 落在上次拉取后
// 59m59s 时，不该把小时频序列推迟到下一个 tick。
const pollSlack = 5 * time.Minute

// PollSchedule 记录每个 key（通常是 metric ID）最近一次成功拉取的时间，
// 按 PollInterval 判定本轮是否到期。零值可用，并发安全。
//
// 状态只在进程内存里：插件重连会新建采集器，因而重连后的首轮会全量拉取一次。
// 这与 lifecycle「会话建立即采集」的约定一致，观测幂等索引会去重。
type PollSchedule struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// Due 判断 key 在 now 时刻是否该拉取。从未拉取过的 key 总是到期。
func (s *PollSchedule) Due(key, frequency string, now time.Time) bool {
	interval, _ := PollInterval(frequency)
	s.mu.Lock()
	last, ok := s.last[key]
	s.mu.Unlock()
	if !ok || interval <= 0 {
		return true
	}
	return now.Sub(last) >= interval-pollSlack
}

// MarkPolled 记录 key 在 at 时刻完成了一次拉取（含「上游尚未发布新值」）。
// 拉取失败时不要调用，下一个 tick 会立即重试。
func (s *PollSchedule) MarkPolled(key string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = make(map[string]time.Time)
	}
	s.last[key] = at
}
