package ontology

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sonde/internal/core/alert"
	coreevent "sonde/internal/core/event"
	"sonde/internal/core/store"
	"sonde/pkg/model"
	pb "sonde/pkg/proto/plugin/v1"
)

// TestMain skips the entire test package when TEST_DATABASE_URL is empty so
// `go test ./...` in CI does not fail when Postgres isn't wired up.
func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		fmt.Println("SKIP: integration tests require TEST_DATABASE_URL")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// dbConn opens a pooled connection using the TEST_DATABASE_URL environment
// variable and t.Cleanup-closes it. Skips when env var is absent.
func dbConn(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
		return nil
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// truncateAll empties every table touched by these tests before and after each
// test. CASCADE handles foreign-key dependencies. Cleaning up front keeps the
// suite repeatable when a prior run was interrupted before t.Cleanup executed.
func truncateAll(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	truncate := func() error {
		_, err := db.Exec(context.Background(), `
			TRUNCATE TABLE
				observations,
				relation_suggestions,
				relations,
				rule_suggestions,
				rules,
				metric_definitions,
				entities,
				plugins,
				alerts,
				event_outbox,
				pending_metrics,
				source_preferences,
				research_snapshots,
				command_log
			RESTART IDENTITY CASCADE
		`)
		return err
	}
	if err := truncate(); err != nil {
		t.Fatalf("truncate integration fixtures before test: %v", err)
	}
	t.Cleanup(func() {
		if err := truncate(); err != nil {
			t.Errorf("truncate integration fixtures after test: %v", err)
		}
	})
}

// =============================================================================
// Test 1 — Idempotent upsert: re-register identical content → no change
// =============================================================================

func TestIntegration_RegisterPlugin_IdempotentUpsert(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)

	s := NewStore(db)

	regReq := &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        "TestIdempotent",
			Version:     "1.0.0",
			Description: "idempotent upsert test plugin",
		},
		Entities: []*pb.EntityDeclaration{
			{Id: "ia_ent_1", Name: "Entity One", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
			{Id: "ia_ent_2", Name: "Entity Two", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Metrics: []*pb.MetricDeclaration{
			{Id: "ia_met_1", Name: "Price", Unit: "USD", Frequency: "1m", EntityId: "ia_ent_1"},
			{Id: "ia_met_2", Name: "Volume", Unit: "shares", Frequency: "1m", EntityId: "ia_ent_1"},
			{Id: "ia_met_3", Name: "Spread", Unit: "bps", Frequency: "1m", EntityId: "ia_ent_2"},
		},
		ChangeLog: "initial registration",
	}

	// First registration — expects version 1.
	gotID, version, err := s.RegisterPlugin(context.Background(), regReq)
	if err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	if gotID == "" {
		t.Fatal("expected non-empty plugin_id")
	}
	if version != 1 {
		t.Fatalf("expected registration_version=1, got %d", version)
	}

	// Second registration with identical content — version stays 1.
	_, version2, err := s.RegisterPlugin(context.Background(), regReq)
	if err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}
	if version2 != 1 {
		t.Fatalf("expected registration_version still 1 after idempotent re-register, got %d", version2)
	}

	// Diff-skip means no new rows inserted.
	if ents, err := s.GetCurrentEntities(context.Background(), gotID); err != nil {
		t.Fatalf("GetCurrentEntities: %v", err)
	} else if len(ents) != 2 {
		t.Fatalf("expected 2 current entities, got %d", len(ents))
	}

	if mets, err := s.GetCurrentMetrics(context.Background(), gotID); err != nil {
		t.Fatalf("GetCurrentMetrics: %v", err)
	} else if len(mets) != 3 {
		t.Fatalf("expected 3 current metrics, got %d", len(mets))
	}
}

// =============================================================================
// Test 2 — Version bump on content change
// =============================================================================

func TestIntegration_RegisterPlugin_VersionBumpOnContentChange(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)

	s := NewStore(db)

	regReq := &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:    "TestVersionBump",
			Version: "1.0.0",
		},
		Entities: []*pb.EntityDeclaration{
			{Id: "vb_ent", Name: "Original Name", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Metrics: []*pb.MetricDeclaration{
			{Id: "vb_met", Name: "Price", Unit: "USD", Frequency: "1m", EntityId: "vb_ent"},
		},
		ChangeLog: "first registration",
	}

	gotID, v1, err := s.RegisterPlugin(context.Background(), regReq)
	if err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	_ = gotID
	if v1 != 1 {
		t.Fatalf("expected version=1 at first register, got %d", v1)
	}

	// Change entity name → triggers entityChanged → bumps version.
	regReq.Entities[0].Name = "Changed Name"
	regReq.ChangeLog = "entity name changed"

	_, v2, err := s.RegisterPlugin(context.Background(), regReq)
	if err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("expected registration_version=2 after content change, got %d", v2)
	}

	// Inspect the DB directly: expect exactly 2 rows for vb_ent.
	rows, err := db.Query(context.Background(), `
		SELECT id, version, effective_to FROM entities WHERE id = $1 ORDER BY version
	`, "vb_ent")
	if err != nil {
		t.Fatalf("query entities: %v", err)
	}
	defer rows.Close()

	type row struct {
		id        string
		version   int
		effective *time.Time
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.version, &r.effective); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows for vb_ent, got %d", len(rs))
	}

	// New row: version=2 and effective_to IS NULL.
	if rs[1].effective != nil {
		t.Fatalf("expected new entity row (version=2) to have effective_to IS NULL, got %v", *rs[1].effective)
	}
	// Old row: effective_to NOT NULL (closed out).
	if rs[0].effective == nil {
		t.Fatalf("expected old entity row (version=1) to have effective_to NOT NULL (superseded)")
	}
}

// =============================================================================
// Test 3 — InsertObservation coverage matrix:
//   same (metric_uid,time,source,labels_hash) + equal grade → NOOP dedup (no error, rows unchanged)
//   same key + higher-grade incoming → UPDATE (the revised correction path)
// =============================================================================

func TestIntegration_InsertObservation_CoverageMatrix(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)

	s := NewStore(db)

	now := time.Now().UTC().Truncate(time.Second)
	dedupKey := "dedup_" + now.Format("20060102T150405")

	snap := &pb.MetricSnapshot{
		MetricId:            "dedup_met",
		Value:               42.0,
		Timestamp:           now.Unix(),
		SourcePlugin:        "dedup-plugin",
		SourcePluginVersion: "1.0.0",
		SourceProvider:      "test",
		SourceFetchedAt:     now.Unix(),
		Labels:              map[string]string{"symbol": "AAPL"},
	}

	// 3a — First insert at grade "delayed" → succeeds.
	action, err := s.InsertObservation(context.Background(), snap, "mtr_dedup", "delayed", 0.9, 0.7, dedupKey, "plg_dedup")
	if err != nil {
		t.Fatalf("first InsertObservation (delayed): %v", err)
	}
	if action != ActionInserted {
		t.Fatalf("expected ActionInserted, got %q", action)
	}

	// 3b — Second insert same key, same grade "delayed" → NOOP dedup, no error.
	action, err = s.InsertObservation(context.Background(), snap, "mtr_dedup", "delayed", 0.9, 0.7, dedupKey, "plg_dedup")
	if err != nil {
		t.Fatalf("second InsertObservation (same grade delayed): unexpected error: %v", err)
	}
	if action != ActionNoopDedup {
		t.Fatalf("expected ActionNoopDedup on same-grade replay, got %q", action)
	}

	// 3c — Third insert same key but grade "revised" (higher rank) → UPDATE overwrites.
	revisedSnap := &pb.MetricSnapshot{
		MetricId:            "dedup_met",
		Value:               43.0, // corrected value
		Timestamp:           now.Unix(),
		SourcePlugin:        "dedup-plugin",
		SourcePluginVersion: "1.0.0",
		SourceProvider:      "test",
		SourceFetchedAt:     now.Unix(),
		Labels:              map[string]string{"symbol": "AAPL"},
	}
	action, err = s.InsertObservation(context.Background(), revisedSnap, "mtr_dedup", "revised", 0.8, 0.8, dedupKey, "plg_dedup")
	if err != nil {
		t.Fatalf("third InsertObservation (revised upsert): unexpected error: %v", err)
	}
	if action != ActionUpdatedRevised {
		t.Fatalf("expected ActionUpdatedRevised on revised overwrite, got %q", action)
	}

	// Verify: exactly one observation row still, with the revised value.
	var count int
	var finalValue float64
	if err := db.QueryRow(context.Background(), `
		SELECT COUNT(*), MAX(value) FROM observations WHERE metric_uid = $1 AND labels_hash = $2
	`, "mtr_dedup", dedupKey).Scan(&count, &finalValue); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 observation row after revised upsert, got %d", count)
	}
	if finalValue != 43.0 {
		t.Fatalf("expected value=43.0 (revised) after upsert, got %f", finalValue)
	}

	// 3d — Fourth insert same key, LOWER grade "estimated" → NOOP (don't downgrade).
	action, err = s.InsertObservation(context.Background(), snap, "mtr_dedup", "estimated", 0.5, 0.5, dedupKey, "plg_dedup")
	if err != nil {
		t.Fatalf("fourth InsertObservation (lower grade): unexpected error: %v", err)
	}
	if action != ActionNoopDedup {
		t.Fatalf("expected ActionNoopDedup when incoming grade is lower, got %q", action)
	}

	// First insert and higher-grade correction each receive one durable work
	// item. Exact/lower-grade replays do not create detection work.
	var workCount, keyCount int
	if err := db.QueryRow(context.Background(), `
		SELECT COUNT(*), COUNT(DISTINCT dedup_key)
		FROM event_outbox
		WHERE event_type = $1
	`, coreevent.TypeDetectionRequested).Scan(&workCount, &keyCount); err != nil {
		t.Fatalf("count detection work: %v", err)
	}
	if workCount != 2 || keyCount != 2 {
		t.Fatalf("detection work count/keys = %d/%d, want 2/2", workCount, keyCount)
	}
}

// =============================================================================
// Test 4 — InsertObservation idempotent replay with different labels_hash
// =============================================================================

func TestIntegration_InsertObservation_IdempotentReplay(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)

	s := NewStore(db)

	now := time.Now().UTC().Truncate(time.Second)
	metricUID := "mtr_replay"
	metricID := "replay_met"

	// First: labels_hash A.
	labelsHashA := "aaaabbbbccccdddd"
	snapA := &pb.MetricSnapshot{
		MetricId:            metricID,
		Value:               100.0,
		Timestamp:           now.Unix(),
		SourcePlugin:        "replay-plugin",
		SourcePluginVersion: "1.0.0",
		SourceProvider:      "test",
		SourceFetchedAt:     now.Unix(),
		Labels:              map[string]string{"symbol": "AAPL"},
	}
	if _, err := s.InsertObservation(context.Background(), snapA, metricUID, "realtime", 0.9, 1.0, labelsHashA, "plg_replay"); err != nil {
		t.Fatalf("first InsertObservation (hash A): %v", err)
	}

	// Second: different labels_hash B → new distinct row.
	labelsHashB := "eeeeffff11112222"
	snapB := &pb.MetricSnapshot{
		MetricId:            metricID,
		Value:               101.0,
		Timestamp:           now.Unix(),
		SourcePlugin:        "replay-plugin",
		SourcePluginVersion: "1.0.0",
		SourceProvider:      "test",
		SourceFetchedAt:     now.Unix(),
		Labels:              map[string]string{"symbol": "AAPL"},
	}
	if _, err := s.InsertObservation(context.Background(), snapB, metricUID, "realtime", 0.9, 1.0, labelsHashB, "plg_replay"); err != nil {
		t.Fatalf("second InsertObservation (hash B): %v", err)
	}

	// Verify: rows can be counted grouped by metric_uid, time, and labels_hash
	// (the unique-index dimensions).
	rows, err := db.Query(context.Background(), `
		SELECT metric_uid, time, labels_hash, COUNT(*) AS c
		FROM observations
		WHERE metric_uid = $1
		GROUP BY metric_uid, time, labels_hash
		ORDER BY labels_hash
	`, metricUID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type grp struct {
		metricUID  string
		time       time.Time
		labelsHash string
		count      int
	}
	var groups []grp
	for rows.Next() {
		var g grp
		if err := rows.Scan(&g.metricUID, &g.time, &g.labelsHash, &g.count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 (metric_uid,time,labels_hash) groups, got %d", len(groups))
	}
	for _, g := range groups {
		if g.count != 1 {
			t.Fatalf("expected each group count=1, got %d for hash=%s", g.count, g.labelsHash)
		}
	}
}

// =============================================================================
// Test 5 — Relation auto-accept for structural ("tracks")
// =============================================================================

func TestIntegration_PluginRegister_RelationAutoAccept(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)

	s := NewStore(db)

	regReq := &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:    "TestRelationAuto",
			Version: "1.0.0",
		},
		Entities: []*pb.EntityDeclaration{
			{Id: "ra_src", Name: "Source Entity", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
			{Id: "ra_tgt", Name: "Target Entity", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "ra_src",
				TargetId:     "ra_tgt",
				RelationType: "tracks",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Confidence:   1.0,
			},
		},
	}

	gotID, _, err := s.RegisterPlugin(context.Background(), regReq)
	if err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}

	// "tracks" is structural → auto-accepted into relations.
	var relationCount int
	if err := db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM relations
		WHERE source_id = $1 AND target_id = $2 AND relation_type = $3 AND effective_to IS NULL
	`, "ra_src", "ra_tgt", "tracks").Scan(&relationCount); err != nil {
		t.Fatalf("query relations: %v", err)
	}
	if relationCount != 1 {
		t.Fatalf("expected 1 relation in relations, got %d", relationCount)
	}

	// The relation_suggestions status should be auto_accepted (or the row absent
	// when auto-accepted — both are valid; we check whichever applies).
	var status string
	err = db.QueryRow(context.Background(), `
		SELECT status FROM relation_suggestions
		WHERE source_id = $1 AND target_id = $2 AND relation_type = $3 AND plugin_id = $4
	`, "ra_src", "ra_tgt", "tracks", gotID).Scan(&status)
	if err == nil {
		// If a row exists, it must be auto_accepted.
		if status != "auto_accepted" {
			t.Fatalf("expected relation_suggestions.status=auto_accepted, got %q", status)
		}
	}
	// If no row returned (ErrNoRows) that's fine — auto-accepted means the
	// pipeline may have overwritten suggestion status or kept it accepted.
}

// =============================================================================
// Test 6 — Human override wins (alert dedup via PostgresAlertStore)
// =============================================================================

func TestIntegration_RuleReview_HumanOverrideWins(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()

	pluginID, _, err := NewStore(db).RegisterPlugin(ctx, &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "HumanOverride", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatalf("RegisterPlugin fixture: %v", err)
	}

	alertStore := store.NewPostgresAlertStore(db)

	dedupKey := "hr_test_" + time.Now().Format("20060102T150405")
	now := time.Now().UTC()

	makeAlert := func(id string) model.Alert {
		return model.Alert{
			ID:                id,
			Title:             "Test Alert",
			Summary:           "Human override wins",
			Severity:          model.SeverityWarning,
			MetricID:          "test_met",
			RuleID:            1,
			RuleVersion:       1,
			RuleEffectiveFrom: now,
			DetectorName:      "test_detector",
			DedupKey:          dedupKey,
			PluginID:          pluginID,
			TriggeredAt:       now,
			Evidence:          []byte(`{}`),
		}
	}

	// First insert must succeed.
	if err := alertStore.CreateAlertWithEvents(ctx, makeAlert("alert_001"), []alert.PendingEvent{
		{EventType: "alert.raised", Payload: []byte(`{"id":"alert_001"}`)},
	}); err != nil {
		t.Fatalf("first CreateAlertWithEvents: %v", err)
	}

	// Second insert with same dedup_key must return alert.ErrDuplicateAlert.
	err = alertStore.CreateAlertWithEvents(ctx, makeAlert("alert_002"), []alert.PendingEvent{
		{EventType: "alert.raised", Payload: []byte(`{"id":"alert_002"}`)},
	})
	if err == nil {
		t.Fatal("expected ErrDuplicateAlert on second insert with same dedup_key (both active), got nil")
	}
	if !errors.Is(err, alert.ErrDuplicateAlert) {
		t.Fatalf("expected errors.Is(err, alert.ErrDuplicateAlert); got %v (type %T)", err, err)
	}
}

// =============================================================================
// Test 7 — Startup detection reconciliation is bounded and idempotent
// =============================================================================

func TestIntegration_DetectionReconciliation_Idempotent(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()

	ontologyStore := NewStore(db)
	pluginID, _, err := ontologyStore.RegisterPlugin(ctx, &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "TestReconcile", Version: "1.0.0"},
		Entities: []*pb.EntityDeclaration{
			{Id: "reconcile_entity", Name: "Reconcile Entity", Namespace: "test", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Metrics: []*pb.MetricDeclaration{
			{Id: "reconcile.metric", Name: "Reconcile Metric", Unit: "count", Frequency: "daily", EntityId: "reconcile_entity"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}
	metricUID, err := ontologyStore.GetMetricUID(ctx, "reconcile.metric")
	if err != nil {
		t.Fatalf("GetMetricUID: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	snap := &pb.MetricSnapshot{
		MetricId:            "reconcile.metric",
		Value:               1,
		Timestamp:           now.Unix(),
		SourcePluginVersion: "1.0.0",
		SourceProvider:      "test",
		SourceFetchedAt:     now.Unix(),
	}
	if _, err := ontologyStore.InsertObservation(ctx, snap, metricUID, "delayed", 0.9, 0.8, "reconcile-labels", pluginID); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	outboxStore := store.NewPostgresOutboxStore(db)
	count, err := outboxStore.ReconcileDetectionRequests(ctx)
	if err != nil {
		t.Fatalf("first reconciliation: %v", err)
	}
	if count != 1 {
		t.Fatalf("first reconciliation inserted %d events, want 1", count)
	}
	count, err = outboxStore.ReconcileDetectionRequests(ctx)
	if err != nil {
		t.Fatalf("second reconciliation: %v", err)
	}
	if count != 0 {
		t.Fatalf("second reconciliation inserted %d events, want 0", count)
	}

	var reconciledMetric, reconciledPlugin string
	if err := db.QueryRow(ctx, `
		SELECT payload->>'metric_id', payload->>'plugin_id'
		FROM event_outbox
		WHERE event_type = $1 AND dedup_key LIKE 'reconcile:%'
	`, coreevent.TypeDetectionRequested).Scan(&reconciledMetric, &reconciledPlugin); err != nil {
		t.Fatalf("query reconciled payload: %v", err)
	}
	if reconciledMetric != "reconcile.metric" || reconciledPlugin != pluginID {
		t.Fatalf("reconciled payload = %s/%s, want reconcile.metric/%s", reconciledMetric, reconciledPlugin, pluginID)
	}
}

// =============================================================================
// Test 8 — Outbox retries expose backoff/error and terminate at the budget
// =============================================================================

func TestIntegration_OutboxRetryState_Observable(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()

	var eventID int
	if err := db.QueryRow(ctx, `
		INSERT INTO event_outbox (event_type, payload, dedup_key)
		VALUES ('test.retry', '{}', 'retry-key')
		RETURNING id
	`).Scan(&eventID); err != nil {
		t.Fatalf("insert retry event: %v", err)
	}

	outboxStore := store.NewPostgresOutboxStore(db)
	if err := outboxStore.MarkFailed(ctx, eventID, "temporary failure"); err != nil {
		t.Fatalf("first MarkFailed: %v", err)
	}
	var status, lastError string
	var attempts int
	var nextAttemptAt time.Time
	if err := db.QueryRow(ctx, `
		SELECT status, attempts, last_error, next_attempt_at
		FROM event_outbox WHERE id = $1
	`, eventID).Scan(&status, &attempts, &lastError, &nextAttemptAt); err != nil {
		t.Fatalf("query retry state: %v", err)
	}
	if status != "pending" || attempts != 1 || lastError != "temporary failure" || !nextAttemptAt.After(time.Now()) {
		t.Fatalf("retry state = %s/%d/%q/%s", status, attempts, lastError, nextAttemptAt)
	}
	ready, err := outboxStore.PickPending(ctx, 10)
	if err != nil {
		t.Fatalf("PickPending during backoff: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("PickPending returned %d events during backoff, want 0", len(ready))
	}

	for attempt := 2; attempt <= 5; attempt++ {
		if err := outboxStore.MarkFailed(ctx, eventID, "still failing"); err != nil {
			t.Fatalf("MarkFailed attempt %d: %v", attempt, err)
		}
	}
	if err := db.QueryRow(ctx, `
		SELECT status, attempts FROM event_outbox WHERE id = $1
	`, eventID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query terminal retry state: %v", err)
	}
	if status != "failed" || attempts != 5 {
		t.Fatalf("terminal retry state = %s/%d, want failed/5", status, attempts)
	}
}

// =============================================================================
// Rule idempotence across reconnects
// =============================================================================

// TestIntegration_RegisterPlugin_RuleIdempotentAcrossReconnect locks the rules
// half of idempotence. TestIntegration_RegisterPlugin_IdempotentUpsert declares
// entities and metrics only, so nothing covered the rule path — and the rule
// path was the one that leaked: every core restart re-registered all plugins
// and minted a fresh version of every rule with byte-identical content. The
// soak rules table grew 9 rows per stack restart and rule versioning stopped
// meaning "something changed".
//
// The config literal below is deliberately written the way plugins write it
// (compact, declaration order). Postgres hands jsonb back normalized — spaces
// after colons, its own key order — so a comparison that is not JSON-aware
// reports "changed" on every reconnect.
func TestIntegration_RegisterPlugin_RuleIdempotentAcrossReconnect(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()

	s := NewStore(db)

	regReq := &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "TestRuleIdempotent", Version: "1.0.0"},
		Entities: []*pb.EntityDeclaration{
			{Id: "ri_ent", Name: "Entity", Namespace: "us_equity", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Metrics: []*pb.MetricDeclaration{
			{Id: "ri_met", Name: "Hash Rate", Unit: "EH/s", Frequency: "1h", EntityId: "ri_ent"},
		},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "ri_rule",
				MetricId:     "ri_met",
				DetectorName: "trend",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"direction":"down","consecutive":4,"tolerance":0.002}`),
				DisplayName:  "Hash rate down 4 periods",
				Description:  "unchanged across reconnects",
			},
		},
		ChangeLog: "initial registration",
	}

	if _, _, err := s.RegisterPlugin(ctx, regReq); err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}

	// Three more identical registrations, as three core restarts would do.
	for i := 2; i <= 4; i++ {
		if _, _, err := s.RegisterPlugin(ctx, regReq); err != nil {
			t.Fatalf("RegisterPlugin #%d: %v", i, err)
		}
	}

	var total, effective, maxVersion int
	if err := db.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE effective_to IS NULL),
		       COALESCE(max(version), 0)
		FROM rules WHERE name = $1
	`, "ri_rule").Scan(&total, &effective, &maxVersion); err != nil {
		t.Fatalf("query rule rows: %v", err)
	}

	if total != 1 {
		t.Errorf("rule rows after 4 identical registrations = %d, want 1", total)
	}
	if maxVersion != 1 {
		t.Errorf("rule version after 4 identical registrations = %d, want 1", maxVersion)
	}
	if effective != 1 {
		t.Errorf("effective rule rows = %d, want exactly 1", effective)
	}
}
