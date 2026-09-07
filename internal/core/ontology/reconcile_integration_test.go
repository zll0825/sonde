package ontology

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pb "sonde/pkg/proto/plugin/v1"
)

// 对账的验收面：catalog 是唯一真源，插件不再申报即退役；但退役必须严格限定在
// 该插件自己名下，必须留下历史观测，且不能因为 catalog 构建失败而误伤全部。

func reconcileRequest(pluginName string, metricIDs []string, rules []*pb.RuleSuggestion) *pb.RegisterPluginRequest {
	metrics := make([]*pb.MetricDeclaration, 0, len(metricIDs))
	for _, id := range metricIDs {
		metrics = append(metrics, &pb.MetricDeclaration{
			Id: id, Name: id, Unit: "count", Frequency: "daily", EntityId: pluginName + "_ent",
		})
	}
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: pluginName, Version: "1.0.0"},
		Entities: []*pb.EntityDeclaration{
			{Id: pluginName + "_ent", Name: pluginName, Namespace: "test", EntityType: pb.EntityType_ENTITY_TYPE_ASSET},
		},
		Metrics: metrics,
		Rules:   rules,
	}
}

func suggestRule(name, metricID string) *pb.RuleSuggestion {
	return &pb.RuleSuggestion{
		Name:         name,
		MetricId:     metricID,
		DetectorName: "threshold",
		Severity:     pb.Severity_SEVERITY_WARNING,
		Config:       []byte(`{"operator":"gt","value":1}`),
		Description:  "reconcile fixture",
	}
}

func effectiveMetricIDs(t *testing.T, db *pgxpool.Pool, pluginID string) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
		SELECT id FROM metric_definitions
		WHERE plugin_id = $1 AND effective_to IS NULL ORDER BY id
	`, pluginID)
	if err != nil {
		t.Fatalf("query effective metrics: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func effectiveRuleNames(t *testing.T, db *pgxpool.Pool) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
		SELECT name FROM rules WHERE effective_to IS NULL ORDER BY name
	`)
	if err != nil {
		t.Fatalf("query effective rules: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, name)
	}
	return out
}

func TestIntegration_Reconcile_RetiresUndeclaredMetricsAndRules(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	pluginID, _, err := s.RegisterPlugin(ctx, reconcileRequest("Retire",
		[]string{"rc.keep", "rc.drop"},
		[]*pb.RuleSuggestion{suggestRule("keep_rule", "rc.keep"), suggestRule("drop_rule", "rc.drop")},
	))
	if err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}

	// 从 catalog 里删掉 rc.drop 及其规则，就是「退役」的全部动作。
	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Retire",
		[]string{"rc.keep"},
		[]*pb.RuleSuggestion{suggestRule("keep_rule", "rc.keep")},
	)); err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}

	metrics := effectiveMetricIDs(t, db, pluginID)
	if len(metrics) != 1 || metrics[0] != "rc.keep" {
		t.Fatalf("effective metrics = %v, want only rc.keep", metrics)
	}
	rules := effectiveRuleNames(t, db)
	if len(rules) != 1 || rules[0] != "keep_rule" {
		t.Fatalf("effective rules = %v, want only keep_rule", rules)
	}
}

func TestIntegration_Reconcile_BumpsRegistrationVersion(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	_, v1, err := s.RegisterPlugin(ctx, reconcileRequest("Bump", []string{"bp.a", "bp.b"}, nil))
	if err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	// 退役本身就是一次实质变更，必须 bump，否则下游看不到本体变了。
	_, v2, err := s.RegisterPlugin(ctx, reconcileRequest("Bump", []string{"bp.a"}, nil))
	if err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}
	if v2 <= v1 {
		t.Fatalf("registration_version %d -> %d, want a bump after a retirement", v1, v2)
	}
}

func TestIntegration_Reconcile_DoesNotCrossPluginBoundary(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	otherID, _, err := s.RegisterPlugin(ctx, reconcileRequest("Bystander",
		[]string{"by.metric"}, []*pb.RuleSuggestion{suggestRule("bystander_rule", "by.metric")}))
	if err != nil {
		t.Fatalf("register bystander: %v", err)
	}
	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Actor",
		[]string{"ac.one", "ac.two"}, nil)); err != nil {
		t.Fatalf("register actor: %v", err)
	}

	// Actor 收缩自己的 catalog，Bystander 名下的东西一根汗毛都不能动。
	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Actor", []string{"ac.one"}, nil)); err != nil {
		t.Fatalf("actor re-register: %v", err)
	}

	if got := effectiveMetricIDs(t, db, otherID); len(got) != 1 || got[0] != "by.metric" {
		t.Fatalf("bystander metrics = %v, want [by.metric] untouched", got)
	}
	rules := effectiveRuleNames(t, db)
	if len(rules) != 1 || rules[0] != "bystander_rule" {
		t.Fatalf("effective rules = %v, want bystander_rule untouched", rules)
	}
}

func TestIntegration_Reconcile_ZeroDeclarationIsRefused(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	pluginID, _, err := s.RegisterPlugin(ctx, reconcileRequest("ZeroDecl",
		[]string{"zd.one", "zd.two"}, []*pb.RuleSuggestion{suggestRule("zd_rule", "zd.one")}))
	if err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}

	// catalog 构建失败最可能的形态：一个指标都没申报。对账若照做，会把该插件
	// 名下全部指标静默退役，告警随之消失而无人察觉。
	empty := reconcileRequest("ZeroDecl", nil, nil)
	if _, _, err := s.RegisterPlugin(ctx, empty); err == nil {
		t.Fatal("zero declared metrics must be refused, not reconciled")
	}

	if got := effectiveMetricIDs(t, db, pluginID); len(got) != 2 {
		t.Fatalf("effective metrics = %v, want both still effective after the refusal", got)
	}
	if got := effectiveRuleNames(t, db); len(got) != 1 {
		t.Fatalf("effective rules = %v, want the rule untouched after the refusal", got)
	}
}

func TestIntegration_Reconcile_NewPluginWithoutMetricsStillRegisters(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()

	// 零申报保护只针对「有存量指标却申报 0 个」。首次注册就没有指标是合法形态
	// ——若干测试夹具与只需要一行 plugins 记录的插件依赖它。
	if _, _, err := NewStore(db).RegisterPlugin(ctx, &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{Name: "MetriclessNewcomer", Version: "1.0.0"},
	}); err != nil {
		t.Fatalf("a brand-new plugin with no metrics must still register: %v", err)
	}
}

func TestIntegration_Reconcile_KeepsManualRules(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("ManualKeep",
		[]string{"mk.metric"}, []*pb.RuleSuggestion{suggestRule("plugin_rule", "mk.metric")})); err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	// 用户手工建的规则，插件从来不会申报它。差集若不排除 source='manual'，
	// 第一次对账就会把它退役掉。
	if _, err := db.Exec(ctx, `
		INSERT INTO rules (name, metric_id, detector_name, severity, config,
			description, enabled, source, is_override, version, effective_from)
		VALUES ('handmade_rule', 'mk.metric', 'threshold', 'warning', '{"operator":"gt","value":9}',
			'user authored', TRUE, 'manual', TRUE, 1, NOW())
	`); err != nil {
		t.Fatalf("insert manual rule: %v", err)
	}

	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("ManualKeep", []string{"mk.metric"}, nil)); err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}

	rules := effectiveRuleNames(t, db)
	if len(rules) != 1 || rules[0] != "handmade_rule" {
		t.Fatalf("effective rules = %v, want the manual rule to survive while plugin_rule retires", rules)
	}
}

func TestIntegration_Reconcile_ObservationsSurviveRetirement(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("HistoryKeep", []string{"hk.metric"}, nil)); err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	var uid string
	if err := db.QueryRow(ctx, `
		SELECT uid FROM metric_definitions WHERE id = 'hk.metric' AND effective_to IS NULL
	`).Scan(&uid); err != nil {
		t.Fatalf("read metric uid: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO observations (
			time, metric_id, metric_uid, value, source_plugin, source_plugin_version,
			source_provider, source_class, source_fetched_at, quality_grade, quality_confidence
		) VALUES ($1, 'hk.metric', $2, 42.0, 'plg_historykeep', '1.0.0',
			'test', 'real', $1, 'delayed', 1.0)
	`, time.Now().UTC(), uid); err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	// 退役该指标：只申报另一个，hk.metric 落入差集。
	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("HistoryKeep", []string{"hk.other"}, nil)); err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}

	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM observations WHERE metric_uid = $1`, uid).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("observations for a retired metric = %d, want 1 (history must stay queryable)", count)
	}
}

func TestIntegration_Reconcile_RetiredMetricStaysRetiredOnReconnect(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Idem", []string{"id.a", "id.b"}, nil)); err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	pluginID, _, err := s.RegisterPlugin(ctx, reconcileRequest("Idem", []string{"id.a"}, nil))
	if err != nil {
		t.Fatalf("second RegisterPlugin: %v", err)
	}
	// 重启重连不得让已退役的指标复活。
	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Idem", []string{"id.a"}, nil)); err != nil {
		t.Fatalf("reconnect RegisterPlugin: %v", err)
	}

	got := effectiveMetricIDs(t, db, pluginID)
	if len(got) != 1 || got[0] != "id.a" {
		t.Fatalf("effective metrics after reconnect = %v, want only id.a", got)
	}
	var versions int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM metric_definitions WHERE id = 'id.b'`).Scan(&versions); err != nil {
		t.Fatalf("count id.b versions: %v", err)
	}
	if versions != 1 {
		t.Fatalf("id.b has %d rows, want the single retired version (no resurrection)", versions)
	}
}

func TestIntegration_Reconcile_RefusalMessageNamesThePlugin(t *testing.T) {
	db := dbConn(t)
	truncateAll(t, db)
	ctx := context.Background()
	s := NewStore(db)

	if _, _, err := s.RegisterPlugin(ctx, reconcileRequest("Named", []string{"nm.one"}, nil)); err != nil {
		t.Fatalf("first RegisterPlugin: %v", err)
	}
	_, _, err := s.RegisterPlugin(ctx, reconcileRequest("Named", nil, nil))
	if err == nil {
		t.Fatal("want refusal")
	}
	if !strings.Contains(err.Error(), "plg_named") {
		t.Fatalf("refusal error = %q, want it to name the plugin so the operator can act", err)
	}
}
