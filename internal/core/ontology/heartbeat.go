package ontology

import (
	"context"
	"fmt"
)

// TouchHeartbeat 将插件心跳落库：healthy=true、last_heartbeat=NOW()。
// core 内存中的 isPluginHealthy 仍是权威判断；这里只是让 /api/status
// （只读数据库）能看到活跃状态。读侧需自行套用时效窗口判断是否真在线，
// 因为进程崩溃不会把 healthy 写回 false。
func (s *Store) TouchHeartbeat(ctx context.Context, pluginID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE plugins
		SET healthy = TRUE, last_heartbeat = NOW(), state = 'running', updated_at = NOW()
		WHERE id = $1
	`, pluginID)
	if err != nil {
		return fmt.Errorf("touch heartbeat for %s: %w", pluginID, err)
	}
	return nil
}

// RecordCollect 记录一次成功的数据推送（用于状态栏的"最后采集"提示）。
// count 是本次真正入库的观测条数。
func (s *Store) RecordCollect(ctx context.Context, pluginID string, count int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE plugins
		SET healthy = TRUE, last_heartbeat = NOW(),
		    last_collect_at = NOW(), last_collect_count = $2,
		    state = 'running', updated_at = NOW()
		WHERE id = $1
	`, pluginID, count)
	if err != nil {
		return fmt.Errorf("record collect for %s: %w", pluginID, err)
	}
	return nil
}
