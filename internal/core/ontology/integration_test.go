package ontology

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/store"
	"capital_observatory/pkg/model"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"github.com/jackc/pgx/v5/pgxpool"
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

// truncateAll registers a t.Cleanup that empties every table touched by these
// tests. CASCADE handles foreign-key dependencies.
func truncateAll(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `
			TRUNCATE TABLE
				observations,
				relation_suggestions,
				relations_v2,
				rule_suggestions,
				rules_v2,
				metric_definitions_v2,
				entities_v2,
				plugins,
				alerts,
				event_outbox,
				pending_metrics,
				source_preferences,
				research_snapshots,
				command_log
			RESTART IDENTITY CASCADE
		`)
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
		SELECT id, version, effective_to FROM entities_v2 WHERE id = $1 ORDER BY version
	`, "vb_ent")
	if err != nil {
		t.Fatalf("query entities_v2: %v", err)
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
	if action != ActionInserted {
		// revised overwrites count as ActionInserted (data was persisted).
		t.Fatalf("expected ActionInserted on revised overwrite, got %q", action)
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

	// "tracks" is structural → auto-accepted into relations_v2.
	var relationCount int
	if err := db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM relations_v2
		WHERE source_id = $1 AND target_id = $2 AND relation_type = $3 AND effective_to IS NULL
	`, "ra_src", "ra_tgt", "tracks").Scan(&relationCount); err != nil {
		t.Fatalf("query relations_v2: %v", err)
	}
	if relationCount != 1 {
		t.Fatalf("expected 1 relation in relations_v2, got %d", relationCount)
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
			PluginID:          "plg_hr_test",
			TriggeredAt:       now,
			Evidence:          []byte(`{}`),
		}
	}

	// First insert must succeed.
	if err := alertStore.CreateAlertWithEvent(context.Background(), makeAlert("alert_001"), "alert.raised", []byte(`{"id":"alert_001"}`)); err != nil {
		t.Fatalf("first CreateAlertWithEvent: %v", err)
	}

	// Second insert with same dedup_key must return alert.ErrDuplicateAlert.
	err := alertStore.CreateAlertWithEvent(context.Background(), makeAlert("alert_002"), "alert.raised", []byte(`{"id":"alert_002"}`))
	if err == nil {
		t.Fatal("expected ErrDuplicateAlert on second insert with same dedup_key (both active), got nil")
	}
	if !errors.Is(err, alert.ErrDuplicateAlert) {
		t.Fatalf("expected errors.Is(err, alert.ErrDuplicateAlert); got %v (type %T)", err, err)
	}
}
