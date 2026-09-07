package ontology

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pb "sonde/pkg/proto/plugin/v1"
)

func TestIntegration_RecordHeartbeatPreservesAndRecoversCollectionHealth(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	const pluginID = "plg_heartbeat_health"
	previousCollectAt := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	if _, err := db.Exec(ctx, `
		INSERT INTO plugins (
			id, name, version, healthy, last_collect_at,
			last_collect_duration_ms, last_collect_count,
			last_collect_error, consecutive_errors
		) VALUES ($1, 'heartbeat-health', '1.0.0', FALSE, $2, 88, 1, 'previous failure', 2)
	`, pluginID, previousCollectAt); err != nil {
		t.Fatalf("insert plugin: %v", err)
	}

	store := NewStore(db)
	for name, status := range map[string]*pb.PluginStatus{
		"legacy":       nil,
		"runtime-only": {Runtime: map[string]string{"circuit_state": "open"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.RecordHeartbeat(ctx, pluginID, status); err != nil {
				t.Fatalf("RecordHeartbeat: %v", err)
			}
			assertPluginCollectionHealth(t, db, pluginID, false, previousCollectAt, 88, 1, "previous failure", 2)
		})
	}

	partialAt := previousCollectAt.Add(time.Hour)
	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{
		LastCollectAt: partialAt.Unix(), LastCollectDurationMs: 125,
		LastCollectCount: 2, LastCollectError: "fred/gold: timeout", ConsecutiveErrors: 3,
	}); err != nil {
		t.Fatalf("partial RecordHeartbeat: %v", err)
	}
	assertPluginCollectionHealth(t, db, pluginID, false, partialAt, 125, 2, "fred/gold: timeout", 3)

	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{
		LastCollectDurationMs: 250, LastCollectError: "fred: unavailable", ConsecutiveErrors: 4,
	}); err != nil {
		t.Fatalf("failure RecordHeartbeat: %v", err)
	}
	assertPluginCollectionHealth(t, db, pluginID, false, partialAt, 250, 0, "fred: unavailable", 4)

	recoveredAt := partialAt.Add(time.Hour)
	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{
		LastCollectAt: recoveredAt.Unix(), LastCollectDurationMs: 64, LastCollectCount: 3,
	}); err != nil {
		t.Fatalf("recovery RecordHeartbeat: %v", err)
	}
	assertPluginCollectionHealth(t, db, pluginID, true, recoveredAt, 64, 3, "", 0)
}

func assertPluginCollectionHealth(t *testing.T, db DB, pluginID string, wantHealthy bool, wantAt time.Time, wantDuration, wantCount int, wantError string, wantConsecutive int) {
	t.Helper()
	var (
		healthy         bool
		at              time.Time
		duration, count int
		lastError       *string
		consecutive     int
	)
	if err := db.QueryRow(context.Background(), `
		SELECT healthy, last_collect_at, last_collect_duration_ms,
		       last_collect_count, last_collect_error, consecutive_errors
		FROM plugins WHERE id = $1
	`, pluginID).Scan(&healthy, &at, &duration, &count, &lastError, &consecutive); err != nil {
		t.Fatalf("query plugin health: %v", err)
	}
	gotError := ""
	if lastError != nil {
		gotError = *lastError
	}
	if healthy != wantHealthy || !at.Equal(wantAt) || duration != wantDuration || count != wantCount || gotError != wantError || consecutive != wantConsecutive {
		t.Fatalf("health = %t/%s/%d/%d/%q/%d, want %t/%s/%d/%d/%q/%d",
			healthy, at, duration, count, gotError, consecutive,
			wantHealthy, wantAt, wantDuration, wantCount, wantError, wantConsecutive)
	}
}

// TestIntegration_RecordHeartbeatPersistsRuntime 锁住密钥就绪链路的中间一环。
//
// PluginStatus.runtime（proto 字段 7）一直在传，却从未落库，所以 /api/status
// 只能退而用 os.Getenv 在 API 进程里猜——而密钥只存在于插件容器，结论必然是
// 错的。runtime 必须在两条心跳分支上都持久化：只上报 runtime、没有采集结果的
// 心跳走的正是精简分支。
func TestIntegration_RecordHeartbeatPersistsRuntime(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	const pluginID = "plg_runtime_persist"
	if _, err := db.Exec(ctx, `
		INSERT INTO plugins (id, name, version) VALUES ($1, 'runtime-persist', '1.0.0')
	`, pluginID); err != nil {
		t.Fatalf("insert plugin: %v", err)
	}
	store := NewStore(db)

	readRuntime := func(t *testing.T) map[string]string {
		t.Helper()
		var raw []byte
		if err := db.QueryRow(ctx, `SELECT runtime FROM plugins WHERE id = $1`, pluginID).Scan(&raw); err != nil {
			t.Fatalf("read runtime: %v", err)
		}
		out := map[string]string{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal runtime %q: %v", raw, err)
		}
		return out
	}

	// 精简分支：只有 runtime，没有采集结果。
	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{
		Runtime: map[string]string{"secret.FRED_API_KEY": "present", "circuit_state": "closed"},
	}); err != nil {
		t.Fatalf("liveness heartbeat: %v", err)
	}
	if got := readRuntime(t); got["secret.FRED_API_KEY"] != "present" || got["circuit_state"] != "closed" {
		t.Fatalf("runtime after liveness heartbeat = %+v", got)
	}

	// 完整分支：带采集结果，runtime 同样要落库。
	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{
		LastCollectCount: 6,
		Runtime:          map[string]string{"secret.FRED_API_KEY": "missing"},
	}); err != nil {
		t.Fatalf("collection heartbeat: %v", err)
	}
	if got := readRuntime(t); got["secret.FRED_API_KEY"] != "missing" {
		t.Fatalf("runtime after collection heartbeat = %+v", got)
	}

	// 一次不带 runtime 的心跳不代表插件失去了这些属性，只代表这一拍没报。
	// 覆盖成空对象会让首页在两拍之间闪烁。
	if err := store.RecordHeartbeat(ctx, pluginID, &pb.PluginStatus{LastCollectCount: 7}); err != nil {
		t.Fatalf("heartbeat without runtime: %v", err)
	}
	if got := readRuntime(t); got["secret.FRED_API_KEY"] != "missing" {
		t.Fatalf("runtime was clobbered by a heartbeat that carried none: %+v", got)
	}
}
