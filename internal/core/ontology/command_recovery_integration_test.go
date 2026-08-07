package ontology

import (
	"context"
	"sync"
	"testing"
	"time"

	"capital_observatory/internal/core/store"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_CommandLeaseConcurrentClaimAndRecovery(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerCommandPlugin(t, db)
	insertCommand(t, db, "cmd_lease_race", pluginID)
	insertCommand(t, db, "cmd_offline", "plg_offline")
	commandStore := store.NewPostgresCommandStore(db)

	start := make(chan struct{})
	results := make(chan []store.Command, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := commandStore.ClaimDispatchable(ctx, []string{pluginID}, 10, time.Minute)
			results <- claimed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ClaimDispatchable: %v", err)
		}
	}
	claimedCount := 0
	for claimed := range results {
		claimedCount += len(claimed)
	}
	if claimedCount != 1 {
		t.Fatalf("concurrent claimed count = %d, want 1", claimedCount)
	}

	var status string
	var attempts int
	var leaseExpiresAt time.Time
	var acceptedAt *time.Time
	if err := db.QueryRow(ctx, `
		SELECT status, attempts, lease_expires_at, accepted_at
		FROM command_log WHERE command_id = 'cmd_lease_race'
	`).Scan(&status, &attempts, &leaseExpiresAt, &acceptedAt); err != nil {
		t.Fatalf("query claimed command: %v", err)
	}
	if status != "dispatched" || attempts != 1 || !leaseExpiresAt.After(time.Now()) {
		t.Fatalf("claimed state = %s/%d/%s", status, attempts, leaseExpiresAt)
	}
	if acceptedAt != nil {
		t.Fatalf("accepted_at set before plugin ACK: %s", *acceptedAt)
	}
	if err := commandStore.RenewLease(ctx, "cmd_lease_race", time.Minute); err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT accepted_at FROM command_log WHERE command_id = 'cmd_lease_race'
	`).Scan(&acceptedAt); err != nil {
		t.Fatalf("query accepted command: %v", err)
	}
	if acceptedAt == nil {
		t.Fatal("progress ACK did not set accepted_at")
	}

	if _, err := db.Exec(ctx, `
		UPDATE command_log SET lease_expires_at = NOW() - INTERVAL '1 second'
		WHERE command_id = 'cmd_lease_race'
	`); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	restartedStore := store.NewPostgresCommandStore(db)
	reclaimed, err := restartedStore.ClaimDispatchable(ctx, []string{pluginID}, 10, time.Minute)
	if err != nil {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].CommandID != "cmd_lease_race" || reclaimed[0].Attempts != 2 {
		t.Fatalf("reclaimed = %+v, want cmd_lease_race attempt 2", reclaimed)
	}
	if reclaimed[0].LastError == "" {
		t.Fatal("expired lease reclaim did not retain a last_error")
	}

	var offlineStatus string
	var offlineAttempts int
	if err := db.QueryRow(ctx, `
		SELECT status, attempts FROM command_log WHERE command_id = 'cmd_offline'
	`).Scan(&offlineStatus, &offlineAttempts); err != nil {
		t.Fatalf("query offline command: %v", err)
	}
	if offlineStatus != "pending" || offlineAttempts != 0 {
		t.Fatalf("offline state = %s/%d, want pending/0", offlineStatus, offlineAttempts)
	}
}

func TestIntegration_CommandAttemptBudgetAndLateSuccess(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerCommandPlugin(t, db)
	insertCommand(t, db, "cmd_retry_budget", pluginID)
	commandStore := store.NewPostgresCommandStore(db)

	for attempt := 1; attempt <= store.CommandMaxAttempts; attempt++ {
		claimed, err := commandStore.ClaimDispatchable(ctx, []string{pluginID}, 1, time.Minute)
		if err != nil {
			t.Fatalf("claim attempt %d: %v", attempt, err)
		}
		if len(claimed) != 1 || claimed[0].Attempts != attempt {
			t.Fatalf("claim attempt %d = %+v", attempt, claimed)
		}
		status, err := commandStore.MarkAttemptFailed(ctx, "cmd_retry_budget", "provider unavailable")
		if err != nil {
			t.Fatalf("MarkAttemptFailed %d: %v", attempt, err)
		}
		want := store.CmdStatusPending
		if attempt == store.CommandMaxAttempts {
			want = store.CmdStatusFailed
		}
		if status != want {
			t.Fatalf("attempt %d status = %s, want %s", attempt, status, want)
		}
	}

	var status, lastError string
	var attempts int
	var completedAt *time.Time
	if err := db.QueryRow(ctx, `
		SELECT status, attempts, last_error, completed_at
		FROM command_log WHERE command_id = 'cmd_retry_budget'
	`).Scan(&status, &attempts, &lastError, &completedAt); err != nil {
		t.Fatalf("query terminal command: %v", err)
	}
	if status != "failed" || attempts != store.CommandMaxAttempts || lastError == "" || completedAt == nil {
		t.Fatalf("terminal state = %s/%d/%q/%v", status, attempts, lastError, completedAt)
	}

	if err := commandStore.MarkCompleted(ctx, "cmd_retry_budget", 7); err != nil {
		t.Fatalf("late MarkCompleted: %v", err)
	}
	var collected int
	if err := db.QueryRow(ctx, `
		SELECT status, attempts, last_error, collected_count
		FROM command_log WHERE command_id = 'cmd_retry_budget'
	`).Scan(&status, &attempts, &lastError, &collected); err != nil {
		t.Fatalf("query late success: %v", err)
	}
	if status != "completed" || attempts != store.CommandMaxAttempts || lastError == "" || collected != 7 {
		t.Fatalf("late success state = %s/%d/%q/%d", status, attempts, lastError, collected)
	}
}

func TestIntegration_CommandExpiredFinalLeaseBecomesTerminal(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerCommandPlugin(t, db)
	insertCommand(t, db, "cmd_expired_final", pluginID)
	if _, err := db.Exec(ctx, `
		UPDATE command_log
		SET status = 'dispatched', attempts = $2,
		    lease_expires_at = NOW() - INTERVAL '1 second'
		WHERE command_id = $1
	`, "cmd_expired_final", store.CommandMaxAttempts); err != nil {
		t.Fatalf("seed expired final lease: %v", err)
	}

	commandStore := store.NewPostgresCommandStore(db)
	claimed, err := commandStore.ClaimDispatchable(ctx, nil, 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimDispatchable: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed exhausted command: %+v", claimed)
	}
	var status, lastError string
	if err := db.QueryRow(ctx, `
		SELECT status, last_error FROM command_log WHERE command_id = 'cmd_expired_final'
	`).Scan(&status, &lastError); err != nil {
		t.Fatalf("query exhausted command: %v", err)
	}
	if status != "failed" || lastError == "" {
		t.Fatalf("expired final state = %s/%q, want failed with error", status, lastError)
	}
}

func registerCommandPlugin(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	pluginID, _, err := NewStore(db).RegisterPlugin(context.Background(), &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "CommandRecovery", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatalf("RegisterPlugin fixture: %v", err)
	}
	return pluginID
}

func insertCommand(t *testing.T, db *pgxpool.Pool, commandID, pluginID string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `
		INSERT INTO command_log (
			command_id, command_type, target_plugin, requested_by, status, metric_ids
		) VALUES ($1, 'sync', $2, 'test', 'pending', '[]')
	`, commandID, pluginID); err != nil {
		t.Fatalf("insert command %s: %v", commandID, err)
	}
}
