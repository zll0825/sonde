package macro_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"sonde/internal/e2e"
	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
	"sonde/pkg/provider"
	fredprov "sonde/pkg/provider/fred"
	macro "sonde/plugins/macro"
)

// fixtureAnchor is the Wednesday the FRED fixtures end on. The replay shifts
// every fixture date by whole weeks so this date reads as "this week".
var fixtureAnchor = time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)

const fixtureDir = "testdata/e2e/fred"

// TestE2E_MacroFREDReplay drives the macro plugin's real FRED collector over
// recorded FRED responses into an in-process Core backed by the test database:
// backfill push → ingestion → detection outbox → rule evaluation → alerts →
// research snapshots and notification events, then a latest-value poll that
// must dedup and leave the alert set unchanged.
//
// The fixture shapes (testdata/e2e/fred/README.md) are chosen so exactly three
// of the four catalog rules fire.
func TestE2E_MacroFREDReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test skipped in -short mode")
	}
	db := e2e.RequireDB(t)
	ctx := context.Background()

	reg := loadRegistration(t)
	collector := newReplayCollector(t)
	core := e2e.StartCore(t, db)
	plugin := core.Connect(t, reg)

	// ── Backfill: what a Backfill command collects on a fresh install ──────
	now := time.Now()
	snaps, err := collector.GetSnapshotsForWindow(ctx, now.AddDate(0, 0, -730), now)
	if err != nil {
		t.Fatalf("backfill collection: %v", err)
	}
	wantPoints := countFixturePoints(t)
	if len(snaps) != wantPoints {
		t.Fatalf("backfill collected %d snapshots, fixtures hold %d non-missing points", len(snaps), wantPoints)
	}

	ack := plugin.Push(t, snaps)
	if int(ack.GetInserted()) != wantPoints || ack.GetRejected() != 0 {
		t.Fatalf("backfill push ack: inserted=%d dedup=%d rejected=%d, want inserted=%d rejected=0",
			ack.GetInserted(), ack.GetDeduplicated(), ack.GetRejected(), wantPoints)
	}
	core.DrainOutbox(t)

	// Every declared metric landed, all tagged as real FRED data.
	rows, err := db.Query(ctx, `
		SELECT metric_id, COUNT(*), bool_and(source_class = 'real'), bool_and(source_provider = 'fred')
		  FROM observations GROUP BY metric_id`)
	if err != nil {
		t.Fatalf("query observations: %v", err)
	}
	perMetric := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		var real, fred bool
		if err := rows.Scan(&id, &n, &real, &fred); err != nil {
			t.Fatalf("scan observations: %v", err)
		}
		if !real || !fred {
			t.Errorf("metric %s: observations not all source_class=real / provider=fred", id)
		}
		perMetric[id] = n
	}
	rows.Close()
	for _, m := range reg.GetMetrics() {
		if perMetric[m.GetId()] == 0 {
			t.Errorf("metric %s: no observations ingested", m.GetId())
		}
	}

	// ── Alerts: exactly the rules the fixtures were shaped to trip ─────────
	// yield_spike_percentile stays silent even though DGS10 spikes on its last
	// two days: with consecutive=2 and no min_observations, Core looks back
	// 7.5 days (≈5 business-day points), and two points can only sit strictly
	// above p90 once a window holds ≥12. Flip this expectation when the rule
	// gains a min_observations that widens its lookback.
	wantAlerts := []string{"fed_balance_drop", "inflation_above_target", "usd_index_extreme"}
	if got := activeAlertRules(t, core); !equal(got, wantAlerts) {
		t.Fatalf("active alerts = %v, want %v", got, wantAlerts)
	}

	// Each alert got its research snapshot and exactly one live notification.
	var linked int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM alerts a JOIN research_snapshots r ON r.alert_id = a.id`).Scan(&linked); err != nil {
		t.Fatalf("count research snapshots: %v", err)
	}
	if linked != len(wantAlerts) {
		t.Errorf("research snapshots linked to alerts = %d, want %d", linked, len(wantAlerts))
	}
	var notified []string
	for _, n := range core.Notifications() {
		notified = append(notified, n.Title)
	}
	sort.Strings(notified)
	if !equal(notified, wantAlerts) {
		t.Errorf("notifications = %v, want %v", notified, wantAlerts)
	}

	// ── Latest poll: the hourly collection after backfill ───────────────────
	latest, err := collector.GetSnapshots(ctx)
	if err != nil {
		t.Fatalf("latest poll: %v", err)
	}
	if len(latest) != len(reg.GetMetrics()) {
		t.Fatalf("latest poll returned %d snapshots, want one per metric (%d)", len(latest), len(reg.GetMetrics()))
	}
	ack = plugin.Push(t, latest)
	if ack.GetInserted() != 0 || int(ack.GetDeduplicated()) != len(latest) {
		t.Fatalf("latest poll ack: inserted=%d dedup=%d, want all %d deduplicated",
			ack.GetInserted(), ack.GetDeduplicated(), len(latest))
	}
	core.DrainOutbox(t)
	if got := activeAlertRules(t, core); !equal(got, wantAlerts) {
		t.Errorf("after re-poll active alerts = %v, want unchanged %v", got, wantAlerts)
	}
	if n := len(core.Notifications()); n != len(wantAlerts) {
		t.Errorf("after re-poll notifications = %d, want no new ones beyond %d", n, len(wantAlerts))
	}
}

func loadRegistration(t *testing.T) *pb.RegisterPluginRequest {
	t.Helper()
	raw, err := macro.CatalogFS.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	reg, err := pluginrunner.LoadRegistration(raw)
	if err != nil {
		t.Fatalf("compile manifest: %v", err)
	}
	return reg
}

// newReplayCollector builds the production FRED collector from the plugin's
// own bindings.yaml, swapping only the HTTP transport for the replay and
// relaxing the rate limit (the production 0.03 rps would take minutes).
func newReplayCollector(t *testing.T) *fredprov.Collector {
	t.Helper()
	raw, err := macro.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatalf("read bindings: %v", err)
	}
	bindings, err := fredprov.LoadBindings(raw)
	if err != nil {
		t.Fatalf("load bindings: %v", err)
	}
	lookback, err := fredprov.LatestLookbackFromYAML(raw)
	if err != nil {
		t.Fatalf("bindings lookback: %v", err)
	}
	cfg := provider.FREDConfig()
	cfg.RPS, cfg.Burst, cfg.MaxRetries = 1000, 100, 0
	replay := e2e.NewFREDReplay(t, fixtureDir, fixtureAnchor)
	c, err := fredprov.NewCollector(fredprov.Options{
		APIKey:         "e2e-replay",
		Client:         provider.NewSafeHTTPClientWithHTTPClient(cfg, replay.Client()),
		Bindings:       bindings,
		LatestLookback: lookback,
	})
	if err != nil {
		t.Fatalf("new collector: %v", err)
	}
	return c
}

// countFixturePoints counts non-missing values across the fixtures the
// bindings reference (CPIAUCSL is read twice: levels and pc1).
func countFixturePoints(t *testing.T) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list fixtures: %v (found %d)", err, len(files))
	}
	total := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var body struct {
			Observations []struct {
				Value string `json:"value"`
			} `json:"observations"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode %s: %v", f, err)
		}
		for _, o := range body.Observations {
			if o.Value != "." {
				total++
			}
		}
	}
	return total
}

func activeAlertRules(t *testing.T, core *e2e.Core) []string {
	t.Helper()
	rows, err := core.DB.Query(context.Background(),
		`SELECT title FROM alerts WHERE status = 'active' AND mode = 'live' ORDER BY title`)
	if err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scan alert: %v", err)
		}
		out = append(out, title)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
