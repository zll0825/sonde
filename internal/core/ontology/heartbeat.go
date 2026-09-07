package ontology

import (
	"context"
	"encoding/json"
	"fmt"

	pb "sonde/pkg/proto/plugin/v1"
)

// RecordHeartbeat always refreshes liveness. Legacy and runtime-only status
// payloads preserve the latest collection outcome; a recorded collection owns
// the durable health verdict and collection fields.
func (s *Store) RecordHeartbeat(ctx context.Context, pluginID string, status *pb.PluginStatus) error {
	// runtime 在两条分支上都要落库：密钥就绪只有插件进程知道，而只上报
	// runtime、没有采集结果的心跳走的正是上面那条精简分支。
	runtimeJSON, err := marshalPluginRuntime(status)
	if err != nil {
		return fmt.Errorf("marshal runtime for %s: %w", pluginID, err)
	}

	if !hasCollectionStatus(status) {
		_, err := s.db.Exec(ctx, `
			UPDATE plugins
			SET last_heartbeat = NOW(), state = 'running', updated_at = NOW(),
			    runtime = COALESCE($2::jsonb, runtime)
			WHERE id = $1
		`, pluginID, runtimeJSON)
		if err != nil {
			return fmt.Errorf("record liveness heartbeat for %s: %w", pluginID, err)
		}
		return nil
	}

	var lastCollectAt any
	if status.GetLastCollectAt() > 0 {
		lastCollectAt = status.GetLastCollectAt()
	}
	_, err = s.db.Exec(ctx, `
		UPDATE plugins
		SET healthy = ($6 = ''),
		    last_heartbeat = NOW(),
		    last_collect_at = CASE WHEN $2::bigint IS NULL THEN last_collect_at ELSE to_timestamp($2) END,
		    last_collect_duration_ms = $3,
		    last_collect_count = $4,
		    last_collect_error = NULLIF($6, ''),
		    consecutive_errors = $5,
		    state = 'running', updated_at = NOW(),
		    runtime = COALESCE($7::jsonb, runtime)
		WHERE id = $1
	`, pluginID, lastCollectAt, status.GetLastCollectDurationMs(), status.GetLastCollectCount(),
		status.GetConsecutiveErrors(), status.GetLastCollectError(), runtimeJSON)
	if err != nil {
		return fmt.Errorf("record collection heartbeat for %s: %w", pluginID, err)
	}
	return nil
}

func hasCollectionStatus(status *pb.PluginStatus) bool {
	return status != nil && (status.GetLastCollectAt() != 0 ||
		status.GetLastCollectDurationMs() != 0 || status.GetLastCollectCount() != 0 ||
		status.GetLastCollectError() != "" || status.GetConsecutiveErrors() != 0)
}

// marshalPluginRuntime serializes the heartbeat runtime map, or returns nil so
// the SQL COALESCE keeps whatever is already stored.
//
// 一次不带 runtime 的心跳不代表插件失去了这些属性，只代表这一拍没报。
// 覆盖成空对象会让首页在两拍之间闪烁。
func marshalPluginRuntime(status *pb.PluginStatus) ([]byte, error) {
	runtime := status.GetRuntime()
	if len(runtime) == 0 {
		return nil, nil
	}
	return json.Marshal(runtime)
}
