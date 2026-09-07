package ontology

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	pb "sonde/pkg/proto/plugin/v1"
)

// 注册对账：让「不再申报」等于「退役」。
//
// 在此之前，upsertMetric 与 acceptRule 只关闭**本次申报了的**那一条的旧版本，
// 没有任何逻辑关闭本次未申报的条目。后果是从插件 catalog 里删掉一个指标或规则，
// 它会永远停在 effective_to IS NULL —— 采集照跑、规则照触发，删除等于空转。
//
// 退役语义：catalog 是唯一真源，插件不再申报即自动关闭 effective_to。
// 关闭只影响定义的有效性，绝不删 observations —— 观测按 metric_uid 存，而 uid
// 在版本间继承（见 upsertMetric 的注释），历史始终可查。

// ruleKey 是规则的身份三元组，与 rules 的唯一键一致。
type ruleKey struct {
	name         string
	metricID     string
	detectorName string
}

// reconcileMetrics 关闭该插件名下本次未申报的指标定义，返回被退役的 id。
func reconcileMetrics(ctx context.Context, tx pgx.Tx, pluginID string, declared []string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		UPDATE metric_definitions SET effective_to = NOW()
		WHERE plugin_id = $1 AND effective_to IS NULL AND id <> ALL($2::text[])
		RETURNING id
	`, pluginID, declared)
	if err != nil {
		return nil, fmt.Errorf("reconcile metrics: %w", err)
	}
	defer rows.Close()

	var retired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan retired metric: %w", err)
		}
		retired = append(retired, id)
	}
	return retired, rows.Err()
}

// reconcileRules 关闭该插件名下本次未申报的规则，返回被退役的规则名。
//
// rules 表没有 plugin_id 列，归属只能从指标推导：一条规则属于拥有其 metric_id
// 的插件。这里刻意不加 effective_to IS NULL —— 指标可能在同一事务里刚被退役，
// 用当前有效版本去筛会把它的规则漏掉，而 id 的插件归属跨版本不变。
//
// 只关闭 source = 'plugin_suggested' 的规则：用户手工建的规则插件从未申报过，
// 若一并纳入差集，第一次对账就会把它们全部退役。
func reconcileRules(ctx context.Context, tx pgx.Tx, pluginID string, declared []ruleKey) ([]string, error) {
	names := make([]string, len(declared))
	metricIDs := make([]string, len(declared))
	detectors := make([]string, len(declared))
	for i, k := range declared {
		names[i], metricIDs[i], detectors[i] = k.name, k.metricID, k.detectorName
	}

	rows, err := tx.Query(ctx, `
		UPDATE rules SET effective_to = NOW(), updated_at = NOW()
		WHERE effective_to IS NULL
		  AND source = 'plugin_suggested'
		  AND metric_id IN (SELECT DISTINCT id FROM metric_definitions WHERE plugin_id = $1)
		  AND NOT EXISTS (
		      SELECT 1 FROM unnest($2::text[], $3::text[], $4::text[]) AS d(name, metric_id, detector_name)
		      WHERE d.name = rules.name
		        AND d.metric_id = rules.metric_id
		        AND d.detector_name = rules.detector_name
		  )
		RETURNING name
	`, pluginID, names, metricIDs, detectors)
	if err != nil {
		return nil, fmt.Errorf("reconcile rules: %w", err)
	}
	defer rows.Close()

	var retired []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan retired rule: %w", err)
		}
		retired = append(retired, name)
	}
	return retired, rows.Err()
}

// hasEffectiveMetrics 报告该插件当前是否还有生效的指标定义。
// 用于零申报保护：判断「本次申报 0 个指标」是新插件的正常形态，还是
// catalog 构建失败导致的全量退役。
func hasEffectiveMetrics(ctx context.Context, tx pgx.Tx, pluginID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM metric_definitions WHERE plugin_id = $1 AND effective_to IS NULL
	)`, pluginID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check effective metrics: %w", err)
	}
	return exists, nil
}

// reconcileRegistration 是对账的唯一入口，由 RegisterPlugin 在注册事务末尾调用。
// 返回被退役的总数，供调用方决定是否 bump registration_version。
func reconcileRegistration(ctx context.Context, tx pgx.Tx, pluginID string, req *pb.RegisterPluginRequest) (int, error) {
	declaredMetrics := make([]string, 0, len(req.GetMetrics()))
	for _, met := range req.GetMetrics() {
		declaredMetrics = append(declaredMetrics, met.GetId())
	}

	// 零申报保护。catalog 构建失败（embed 失效、YAML 少解析了几条）最可能的
	// 形态就是一个指标都没申报，而对账会把该插件名下**全部**指标静默退役，
	// 告警随之消失。代价为零的下界保护：有存量指标却申报 0 个，拒绝对账。
	//
	// 只在「有东西可退役」时报错——新插件首次注册就申报 0 个指标是合法形态
	// （只需要一行 plugins 记录），不该被这条保护拦下。
	if len(declaredMetrics) == 0 {
		hasMetrics, err := hasEffectiveMetrics(ctx, tx, pluginID)
		if err != nil {
			return 0, err
		}
		if hasMetrics {
			return 0, fmt.Errorf("plugin %s declared zero metrics but has effective ones; "+
				"refusing to reconcile (catalog build likely failed)", pluginID)
		}
		return 0, nil
	}

	retiredMetrics, err := reconcileMetrics(ctx, tx, pluginID, declaredMetrics)
	if err != nil {
		return 0, err
	}

	declaredRules := make([]ruleKey, 0, len(req.GetRules()))
	for _, rule := range req.GetRules() {
		// 申报即算数，与评审结果无关：pending_conflict 的规则仍然是插件声明过
		// 的，不能因为这轮没被接受就被对账退役。
		declaredRules = append(declaredRules, ruleKey{
			name:         rule.GetName(),
			metricID:     rule.GetMetricId(),
			detectorName: rule.GetDetectorName(),
		})
	}
	retiredRules, err := reconcileRules(ctx, tx, pluginID, declaredRules)
	if err != nil {
		return 0, err
	}

	// WARN 而非 Debug：用户已接受「无成熟度闸门」的自动退役风险，这份日志是
	// soak 期间发现异常退役的唯一途径，不能被生产日志级别过滤掉。
	if len(retiredMetrics) > 0 {
		log.Warn().Str("plugin_id", pluginID).
			Int("count", len(retiredMetrics)).
			Strs("metric_ids", retiredMetrics).
			Msg("registration reconcile retired metrics no longer declared by the plugin")
	}
	if len(retiredRules) > 0 {
		log.Warn().Str("plugin_id", pluginID).
			Int("count", len(retiredRules)).
			Strs("rule_names", retiredRules).
			Msg("registration reconcile retired rules no longer declared by the plugin")
	}
	return len(retiredMetrics) + len(retiredRules), nil
}
