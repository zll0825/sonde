package provider

import "sync"

var (
	fredShared     *SafeHTTPClient
	fredSharedOnce sync.Once
)

// SharedFREDClient returns a singleton SafeHTTPClient keyed to FRED.
//
// Intra-process only. Macro and Commodities are deployed as SEPARATE
// containers (plugins are independent go modules / own processes), so each
// gets its own limiter and circuit breaker — they cannot actually share
// this singleton. The code still coalesces the two callers that live in the
// SAME process (e.g. tests, or a future all-in-one binary), but it does NOT
// provide the PRD-mandated "single FRED provider budget across the stack".
//
// TODO: 实现跨进程 provider budget（在 TimescaleDB 建 provider_quota 表，
//       由统一 sidecar 或 Core 协调），否则宏+商品容器各自独立节流，合并
//       RPS 可能突破免费层上限。
func SharedFREDClient() *SafeHTTPClient {
	fredSharedOnce.Do(func() {
		fredShared = NewSafeHTTPClient(FREDConfig())
	})
	return fredShared
}
