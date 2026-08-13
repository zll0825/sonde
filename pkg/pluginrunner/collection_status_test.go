package pluginrunner

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCollectionStatusTransitions(t *testing.T) {
	status := &collectionStatus{}
	startedAt := time.Unix(100, 0)

	status.record(startedAt, startedAt.Add(125*time.Millisecond), 3, nil)
	got := status.snapshot(nil)
	if got.GetLastCollectAt() != 100 || got.GetLastCollectCount() != 3 || got.GetLastCollectDurationMs() != 125 || got.GetLastCollectError() != "" || got.GetConsecutiveErrors() != 0 {
		t.Fatalf("full success status = %+v", got)
	}

	status.record(startedAt, startedAt.Add(250*time.Millisecond), 2, errors.New("gold timeout"))
	got = status.snapshot(map[string]string{"circuit_state": "closed"})
	if got.GetLastCollectCount() != 2 || got.GetLastCollectError() == "" || got.GetConsecutiveErrors() != 1 || got.GetRuntime()["circuit_state"] != "closed" {
		t.Fatalf("partial status = %+v", got)
	}

	status.record(startedAt, startedAt.Add(300*time.Millisecond), 0, errors.New("provider unavailable"))
	got = status.snapshot(nil)
	if got.GetLastCollectAt() != 100 || got.GetLastCollectCount() != 0 || got.GetConsecutiveErrors() != 2 {
		t.Fatalf("complete failure status = %+v", got)
	}

	status.record(startedAt, startedAt.Add(400*time.Millisecond), 4, nil)
	got = status.snapshot(nil)
	if got.GetLastCollectCount() != 4 || got.GetLastCollectError() != "" || got.GetConsecutiveErrors() != 0 {
		t.Fatalf("recovery status = %+v", got)
	}
}

func TestSanitizeCollectionErrorRedactsAndBounds(t *testing.T) {
	err := errors.New("GET https://user:pass@example.test/path?api_key=secret&token=also-secret returned Bearer abc.def, password=hunter2, and {\"access_token\":\"json-secret\"} " + strings.Repeat("body", 300))
	got := SanitizeCollectionError(err)
	for _, secret := range []string{"user:pass", "secret", "abc.def", "hunter2", "json-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized error contains %q: %q", secret, got)
		}
	}
	if len(got) > maxCollectionErrorLength {
		t.Fatalf("sanitized error length = %d, max %d", len(got), maxCollectionErrorLength)
	}
}

func TestSummarizeCollectionFailuresIncludesIdentity(t *testing.T) {
	err := SummarizeCollectionFailures([]CollectionFailure{{
		MetricID: "metal.precious.gold", Provider: "fred", Err: errors.New("timeout"),
	}})
	if err == nil || !strings.Contains(err.Error(), "fred/metal.precious.gold") || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("summary = %v", err)
	}
}

func TestSummarizeCollectionFailuresSanitizesIdentity(t *testing.T) {
	err := SummarizeCollectionFailures([]CollectionFailure{{
		MetricID: "metric?token=metric-secret",
		Provider: "https://user:pass@example.test/provider?api_key=provider-secret",
		Err:      errors.New("timeout"),
	}})
	if err == nil {
		t.Fatal("summary error is nil")
	}
	for _, secret := range []string{"metric-secret", "provider-secret", "user:pass"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("summary contains %q: %q", secret, err)
		}
	}
}

func TestRuntimeOnlyHeartbeatHasNoCollectionOutcome(t *testing.T) {
	status := (&collectionStatus{}).snapshot(map[string]string{"circuit_state": "open"})
	if status == nil || status.GetLastCollectAt() != 0 || status.GetLastCollectError() != "" || status.GetRuntime()["circuit_state"] != "open" {
		t.Fatalf("runtime-only status = %+v", status)
	}
}

func TestCollectionStatusIgnoresOlderCompletion(t *testing.T) {
	status := &collectionStatus{}
	base := time.Unix(100, 0)
	status.record(base, base.Add(2*time.Second), 2, nil)
	status.record(base, base.Add(time.Second), 0, errors.New("older failure"))

	got := status.snapshot(nil)
	if got.GetLastCollectCount() != 2 || got.GetLastCollectError() != "" || got.GetConsecutiveErrors() != 0 {
		t.Fatalf("older completion overwrote status: %+v", got)
	}
}

func TestLifecycleCollectionPathsShareStatusAndRetryFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		commandID string
	}{
		{name: "periodic"},
		{name: "sync", commandID: "cmd_sync"},
		{name: "backfill", commandID: "cmd_backfill"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle := NewLifecycle(Config{})
			calls := 0
			collect := func() ([]Snapshot, error) {
				calls++
				if calls == 1 {
					return []Snapshot{{MetricID: "metric.ok"}}, errors.New("sibling failed")
				}
				return []Snapshot{{MetricID: "metric.ok"}, {MetricID: "metric.recovered"}}, nil
			}

			first := lifecycle.executeCollection(tc.commandID, collect)
			status := lifecycle.status.snapshot(nil)
			if first.err == nil || len(first.snapshots) != 1 || status.GetLastCollectError() == "" || status.GetConsecutiveErrors() != 1 {
				t.Fatalf("partial result/status = %+v / %+v", first, status)
			}

			second := lifecycle.executeCollection(tc.commandID, collect)
			status = lifecycle.status.snapshot(nil)
			if second.err != nil || len(second.snapshots) != 2 || status.GetLastCollectError() != "" || status.GetConsecutiveErrors() != 0 {
				t.Fatalf("recovery result/status = %+v / %+v", second, status)
			}
			if calls != 2 {
				t.Fatalf("collector calls = %d, want 2", calls)
			}
		})
	}
}
