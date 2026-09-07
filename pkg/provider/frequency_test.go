package provider

import (
	"testing"
	"time"
)

func TestFrequencyPeriod_CoversMergedVocabulary(t *testing.T) {
	// 词表是两份历史映射的并集：fred_backfill 只认 daily/weekly/monthly，
	// pluginmgr 还认 realtime/hourly/quarterly。收敛时不能丢掉后三个。
	want := map[string]time.Duration{
		"realtime":  time.Minute,
		"hourly":    time.Hour,
		"daily":     24 * time.Hour,
		"weekly":    7 * 24 * time.Hour,
		"monthly":   30 * 24 * time.Hour,
		"quarterly": 91 * 24 * time.Hour,
	}
	for freq, expect := range want {
		got, ok := FrequencyPeriod(freq)
		if !ok || got != expect {
			t.Errorf("FrequencyPeriod(%q) = %v %v, want %v true", freq, got, ok, expect)
		}
	}
	if got, ok := FrequencyPeriod("fortnightly"); ok || got != 0 {
		t.Errorf("unknown frequency = %v %v, want 0 false", got, ok)
	}
}

func TestLatestLookback_CoversCPIAUCSLPublicationLag(t *testing.T) {
	// 实测场景：CPIAUCSL 的 7 月观测日期是 2026-07-01，8-12 才发布；8 月观测
	// 要等到 9-11。因此 2026-09-07 当天能拿到的最新观测已陈旧 68 天，原来的
	// 30 天窗口内观测数为 0，指标连续失败 36 次。
	obs := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

	window, known := LatestLookback("monthly")
	if !known {
		t.Fatal("monthly must be a known frequency")
	}
	if start := today.Add(-window); start.After(obs) {
		t.Fatalf("monthly window starts %s, misses the observation at %s",
			start.Format("2006-01-02"), obs.Format("2006-01-02"))
	}
}

func TestLatestLookback_MonthlyIndependentOfBindingsFloor(t *testing.T) {
	// PCOPPUSDM 现在只是「碰巧」被 commodities 的 latestLookbackDays: 45 罩住。
	// 月频窗口必须自己站得住，与 45 这个偶然值无关——所以这里根本不提 45。
	obs := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

	window, _ := LatestLookback("monthly")
	if window <= 45*24*time.Hour {
		t.Fatalf("monthly window = %v; it must not rely on the 45-day floor", window)
	}
	if start := today.Add(-window); start.After(obs) {
		t.Fatalf("monthly window starts %s, misses PCOPPUSDM at %s",
			start.Format("2006-01-02"), obs.Format("2006-01-02"))
	}
}

func TestLatestLookback_UnknownFrequencyIsMostConservative(t *testing.T) {
	quarterly, _ := LatestLookback("quarterly")
	got, known := LatestLookback("")
	if known {
		t.Fatal(`"" must report as unknown so the caller can WARN`)
	}
	if got != quarterly {
		t.Fatalf("unknown fallback = %v, want the most conservative window %v", got, quarterly)
	}
}

func TestLatestLookback_DailyAndWeeklyDoNotRegress(t *testing.T) {
	// 日频保持 30 天：与改动前的全局默认一致，行为不得退化。
	if got, _ := LatestLookback("daily"); got != 30*24*time.Hour {
		t.Fatalf("daily = %v, want 30d (unchanged from the previous global default)", got)
	}
	// 周频要罩得住「上一条 + 发布滞后」，必须严格大于日频。
	weekly, _ := LatestLookback("weekly")
	if weekly <= 30*24*time.Hour {
		t.Fatalf("weekly = %v, want more than the daily window", weekly)
	}
}

func TestStaleThreshold_SeparatesReleaseGapFromOutage(t *testing.T) {
	// 月频：陈旧 68 天（CPIAUCSL 的真实间隔）仍算正常，不该计入连续失败。
	monthly, known := StaleThreshold("monthly")
	if !known || monthly < 68*24*time.Hour {
		t.Fatalf("monthly stale threshold = %v %v, want >= 68d", monthly, known)
	}
	// 日频：停更一周以上是真异常，阈值不能宽到把它盖掉。
	daily, _ := StaleThreshold("daily")
	if daily > 7*24*time.Hour {
		t.Fatalf("daily stale threshold = %v, want <= 7d so an outage still surfaces", daily)
	}
	if _, known := StaleThreshold("fortnightly"); known {
		t.Fatal("unknown frequency must report as unknown")
	}
}

func TestDetectGaps_BehaviorUnchangedAfterConvergence(t *testing.T) {
	// A2 回归：DetectGaps 改调共用映射后，daily/weekly/monthly 判定不变，
	// 未知频率仍保留 30 天兜底。
	day := 24 * time.Hour
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		freq     string
		gapDays  int
		wantGaps int
	}{
		{"daily within period", "daily", 2, 0},
		{"daily beyond 2x period", "daily", 3, 1},
		{"weekly within period", "weekly", 14, 0},
		{"weekly beyond 2x period", "weekly", 15, 1},
		{"monthly within period", "monthly", 60, 0},
		{"monthly beyond 2x period", "monthly", 61, 1},
		{"unknown keeps 30d fallback", "fortnightly", 61, 1},
		{"unknown within 30d fallback", "fortnightly", 60, 0},
	}
	for _, tc := range cases {
		times := []time.Time{base, base.Add(time.Duration(tc.gapDays) * day)}
		if got := len(DetectGaps(times, tc.freq)); got != tc.wantGaps {
			t.Errorf("%s: gaps = %d, want %d", tc.name, got, tc.wantGaps)
		}
	}
}
