package ontology

import (
	"context"
	"testing"
	"time"

	pb "capital_observatory/pkg/proto/plugin/v1"
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
