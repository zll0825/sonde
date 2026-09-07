package ontology

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sonde/internal/core/alert"
	coreevent "sonde/internal/core/event"
	"sonde/internal/core/store"
	"sonde/pkg/model"
	pb "sonde/pkg/proto/plugin/v1"
)

func TestIntegration_ResearchLinkage_DurableAndImmutable(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerResearchPlugin(t, db)

	alertStore := store.NewPostgresAlertStore(db)
	engine := alert.NewEngine(alertStore)
	persisted := researchTestAlert("alt_research_durable", "research-durable", pluginID)
	if err := engine.HandleTrigger(ctx, persisted); err != nil {
		t.Fatalf("HandleTrigger: %v", err)
	}
	if err := engine.HandleTrigger(ctx, persisted); err != nil {
		t.Fatalf("HandleTrigger duplicate: %v", err)
	}

	var notificationCount, researchCount int
	if err := db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = $1),
			COUNT(*) FILTER (WHERE event_type = $2 AND dedup_key = $3)
		FROM event_outbox
	`, alert.EventTypeAlertTriggered, coreevent.TypeResearchRequested, persisted.ID).
		Scan(&notificationCount, &researchCount); err != nil {
		t.Fatalf("query alert events: %v", err)
	}
	if notificationCount != 1 || researchCount != 1 {
		t.Fatalf("alert events notification/research = %d/%d, want 1/1", notificationCount, researchCount)
	}
	var dedupCount int
	var lastDeduplicatedAt *time.Time
	if err := db.QueryRow(ctx, `
		SELECT dedup_count, last_deduplicated_at FROM alerts WHERE id = $1
	`, persisted.ID).Scan(&dedupCount, &lastDeduplicatedAt); err != nil {
		t.Fatalf("query dedup audit: %v", err)
	}
	if dedupCount != 1 || lastDeduplicatedAt == nil {
		t.Fatalf("dedup audit count/time = %d/%v, want 1/non-nil", dedupCount, lastDeduplicatedAt)
	}

	loaded, err := alertStore.GetAlertByID(ctx, persisted.ID)
	if err != nil {
		t.Fatalf("GetAlertByID: %v", err)
	}
	if loaded == nil || loaded.ID != persisted.ID || loaded.MetricID != persisted.MetricID ||
		loaded.SourceProvider != persisted.SourceProvider || loaded.SourceClass != persisted.SourceClass {
		t.Fatalf("loaded alert = %+v, want persisted alert", loaded)
	}

	researchStore := store.NewPostgresResearchStore(db)
	frozenAt := time.Now().UTC().Truncate(time.Microsecond)
	first := model.ResearchSnapshot{
		AlertID:          persisted.ID,
		Context:          []byte(`{"version":"first"}`),
		OntologyFrozenAt: frozenAt,
	}
	if err := researchStore.SaveSnapshot(ctx, first); err != nil {
		t.Fatalf("SaveSnapshot first: %v", err)
	}

	const deliveries = 8
	errCh := make(chan error, deliveries)
	var wg sync.WaitGroup
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func(delivery int) {
			defer wg.Done()
			errCh <- researchStore.SaveSnapshot(ctx, model.ResearchSnapshot{
				AlertID:          persisted.ID,
				Context:          []byte(fmt.Sprintf(`{"version":"retry-%d"}`, delivery)),
				OntologyFrozenAt: frozenAt.Add(time.Duration(delivery+1) * time.Hour),
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent SaveSnapshot: %v", err)
		}
	}

	var snapshotCount int
	var version string
	var storedFrozenAt time.Time
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*), MAX(context->>'version'), MAX(ontology_frozen_at)
		FROM research_snapshots WHERE alert_id = $1
	`, persisted.ID).Scan(&snapshotCount, &version, &storedFrozenAt); err != nil {
		t.Fatalf("query frozen snapshot: %v", err)
	}
	if snapshotCount != 1 || version != "first" || !storedFrozenAt.Equal(frozenAt) {
		t.Fatalf("snapshot count/version/frozen_at = %d/%q/%s, want 1/first/%s",
			snapshotCount, version, storedFrozenAt, frozenAt)
	}

	audit, err := store.NewPostgresOutboxStore(db).AuditResearchLinks(ctx)
	if err != nil {
		t.Fatalf("AuditResearchLinks: %v", err)
	}
	if audit.MissingSnapshots != 0 || audit.DuplicateSnapshots != 0 || audit.OrphanSnapshots != 0 || audit.FailedRequests != 0 {
		t.Fatalf("research link audit = %+v, want all zero", audit)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO research_snapshots (alert_id, context, ontology_frozen_at)
		VALUES ('alt_orphan', '{}', NOW())
	`); err == nil {
		t.Fatal("orphan research snapshot insert succeeded")
	}
}

func TestIntegration_ResearchReconciliation_BoundedAndIdempotent(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerResearchPlugin(t, db)
	alertStore := store.NewPostgresAlertStore(db)

	alertIDs := []string{"alt_reconcile_1", "alt_reconcile_2"}
	for i, alertID := range alertIDs {
		persisted := researchTestAlert(alertID, fmt.Sprintf("reconcile-%d", i), pluginID)
		if err := alertStore.CreateAlertWithEvents(ctx, persisted, []alert.PendingEvent{
			{EventType: alert.EventTypeAlertTriggered, Payload: []byte(`{"alert_id":"` + alertID + `"}`)},
		}); err != nil {
			t.Fatalf("create legacy alert %s: %v", alertID, err)
		}
	}

	outboxStore := store.NewPostgresOutboxStore(db)
	for attempt, want := range []int64{1, 1, 0} {
		got, err := outboxStore.ReconcileResearchRequests(ctx, 1)
		if err != nil {
			t.Fatalf("reconciliation %d: %v", attempt+1, err)
		}
		if got != want {
			t.Fatalf("reconciliation %d inserted %d, want %d", attempt+1, got, want)
		}
	}

	var eventCount int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM event_outbox WHERE event_type = $1
	`, coreevent.TypeResearchRequested).Scan(&eventCount); err != nil {
		t.Fatalf("count reconciled events: %v", err)
	}
	if eventCount != len(alertIDs) {
		t.Fatalf("reconciled event count = %d, want %d", eventCount, len(alertIDs))
	}

	researchStore := store.NewPostgresResearchStore(db)
	for _, alertID := range alertIDs {
		if err := researchStore.SaveSnapshot(ctx, model.ResearchSnapshot{
			AlertID:          alertID,
			Context:          []byte(`{"reconciled":true}`),
			OntologyFrozenAt: time.Now(),
		}); err != nil {
			t.Fatalf("save reconciled snapshot %s: %v", alertID, err)
		}
	}
	audit, err := outboxStore.AuditResearchLinks(ctx)
	if err != nil {
		t.Fatalf("AuditResearchLinks: %v", err)
	}
	if audit.MissingSnapshots != 0 || audit.DuplicateSnapshots != 0 || audit.OrphanSnapshots != 0 {
		t.Fatalf("research link audit = %+v, want zero link defects", audit)
	}
}

func TestIntegration_ResearchEventFailureRollsBackAlert(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerResearchPlugin(t, db)
	alertID := "alt_research_rollback"

	if _, err := db.Exec(ctx, `
		INSERT INTO event_outbox (event_type, payload, dedup_key)
		VALUES ($1, '{"alert_id":"alt_research_rollback"}', $2)
	`, coreevent.TypeResearchRequested, alertID); err != nil {
		t.Fatalf("seed conflicting research event: %v", err)
	}
	researchKey := alertID
	err := store.NewPostgresAlertStore(db).CreateAlertWithEvents(
		ctx,
		researchTestAlert(alertID, "rollback", pluginID),
		[]alert.PendingEvent{
			{EventType: alert.EventTypeAlertTriggered, Payload: []byte(`{"alert_id":"alt_research_rollback"}`)},
			{EventType: coreevent.TypeResearchRequested, Payload: []byte(`{"alert_id":"alt_research_rollback"}`), DedupKey: &researchKey},
		},
	)
	if err == nil {
		t.Fatal("CreateAlertWithEvents succeeded despite conflicting research event")
	}

	var alertCount, notificationCount int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM alerts WHERE id = $1`, alertID).Scan(&alertCount); err != nil {
		t.Fatalf("count rolled back alert: %v", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM event_outbox
		WHERE event_type = $1 AND payload->>'alert_id' = $2
	`, alert.EventTypeAlertTriggered, alertID).Scan(&notificationCount); err != nil {
		t.Fatalf("count rolled back notification: %v", err)
	}
	if alertCount != 0 || notificationCount != 0 {
		t.Fatalf("rolled back alert/notification count = %d/%d, want 0/0", alertCount, notificationCount)
	}
}

func TestIntegration_ResearchTerminalFailureIsAuditable(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerResearchPlugin(t, db)
	persisted := researchTestAlert("alt_research_failed", "research-failed", pluginID)
	if err := alert.NewEngine(store.NewPostgresAlertStore(db)).HandleTrigger(ctx, persisted); err != nil {
		t.Fatalf("HandleTrigger: %v", err)
	}

	var eventID int
	if err := db.QueryRow(ctx, `
		SELECT id FROM event_outbox
		WHERE event_type = $1 AND dedup_key = $2
	`, coreevent.TypeResearchRequested, persisted.ID).Scan(&eventID); err != nil {
		t.Fatalf("query research event: %v", err)
	}
	outboxStore := store.NewPostgresOutboxStore(db)
	for attempt := 1; attempt <= 5; attempt++ {
		if err := outboxStore.MarkFailed(ctx, eventID, "permanent research failure"); err != nil {
			t.Fatalf("MarkFailed attempt %d: %v", attempt, err)
		}
	}

	audit, err := outboxStore.AuditResearchLinks(ctx)
	if err != nil {
		t.Fatalf("AuditResearchLinks: %v", err)
	}
	if audit.MissingSnapshots != 1 || audit.FailedRequests != 1 {
		t.Fatalf("research link audit = %+v, want missing=1 failed=1", audit)
	}
	if count, err := outboxStore.ReconcileResearchRequests(ctx, 100); err != nil {
		t.Fatalf("ReconcileResearchRequests: %v", err)
	} else if count != 0 {
		t.Fatalf("reconciliation requeued %d terminal failures, want 0", count)
	}
}

func registerResearchPlugin(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	pluginID, _, err := NewStore(db).RegisterPlugin(context.Background(), &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "ResearchLinkage", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatalf("RegisterPlugin fixture: %v", err)
	}
	return pluginID
}

func TestIntegration_ObserveTriggerPersistsAlertWithoutOutbox(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	pluginID := registerResearchPlugin(t, db)

	engine := alert.NewEngine(store.NewPostgresAlertStore(db))
	observe := researchTestAlert("alt_observe_only", "observe-only", pluginID)
	observe.Mode = model.RuleModeObserve
	if err := engine.HandleTrigger(ctx, observe); err != nil {
		t.Fatalf("HandleTrigger observe: %v", err)
	}

	var mode string
	if err := db.QueryRow(ctx, `SELECT mode FROM alerts WHERE id = $1`, observe.ID).Scan(&mode); err != nil {
		t.Fatalf("query observe alert: %v", err)
	}
	if mode != string(model.RuleModeObserve) {
		t.Fatalf("alerts.mode = %q, want observe", mode)
	}
	var outbox int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM event_outbox
		WHERE event_type IN ($1, $2)
	`, alert.EventTypeAlertTriggered, coreevent.TypeResearchRequested).Scan(&outbox); err != nil {
		t.Fatalf("query observe outbox: %v", err)
	}
	if outbox != 0 {
		t.Fatalf("observe outbox count = %d, want 0", outbox)
	}

	liveInfo := researchTestAlert("alt_live_info", "live-info", pluginID)
	liveInfo.Severity = model.SeverityInfo
	liveInfo.Mode = model.RuleModeLive
	if err := engine.HandleTrigger(ctx, liveInfo); err != nil {
		t.Fatalf("HandleTrigger live info: %v", err)
	}
	var notificationCount, researchCount int
	if err := db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = $1),
			COUNT(*) FILTER (WHERE event_type = $2 AND dedup_key = $3)
		FROM event_outbox
	`, alert.EventTypeAlertTriggered, coreevent.TypeResearchRequested, liveInfo.ID).
		Scan(&notificationCount, &researchCount); err != nil {
		t.Fatalf("query live info events: %v", err)
	}
	if notificationCount != 1 || researchCount != 1 {
		t.Fatalf("live+info outbox notification/research = %d/%d, want 1/1", notificationCount, researchCount)
	}
}

func researchTestAlert(id, dedupKey, pluginID string) model.Alert {
	now := time.Now().UTC()
	return model.Alert{
		ID:                id,
		Title:             "Research Linkage",
		Summary:           "durable research linkage test",
		Severity:          model.SeverityWarning,
		Status:            "active",
		MetricID:          "research.metric",
		RuleID:            1,
		RuleVersion:       1,
		RuleEffectiveFrom: now,
		DetectorName:      "threshold",
		DedupKey:          dedupKey,
		Evidence:          []byte(`{"metric_uid":"mtr_research"}`),
		PluginID:          pluginID,
		SourceProvider:    "test-fixture",
		SourceClass:       model.SourceClassTest,
		TriggeredAt:       now,
	}
}
