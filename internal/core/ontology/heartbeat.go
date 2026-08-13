package ontology

import (
	"context"
	"fmt"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

// RecordHeartbeat always refreshes liveness. Legacy and runtime-only status
// payloads preserve the latest collection outcome; a recorded collection owns
// the durable health verdict and collection fields.
func (s *Store) RecordHeartbeat(ctx context.Context, pluginID string, status *pb.PluginStatus) error {
	if !hasCollectionStatus(status) {
		_, err := s.db.Exec(ctx, `
			UPDATE plugins
			SET last_heartbeat = NOW(), state = 'running', updated_at = NOW()
			WHERE id = $1
		`, pluginID)
		if err != nil {
			return fmt.Errorf("record liveness heartbeat for %s: %w", pluginID, err)
		}
		return nil
	}

	var lastCollectAt any
	if status.GetLastCollectAt() > 0 {
		lastCollectAt = status.GetLastCollectAt()
	}
	_, err := s.db.Exec(ctx, `
		UPDATE plugins
		SET healthy = ($6 = ''),
		    last_heartbeat = NOW(),
		    last_collect_at = CASE WHEN $2::bigint IS NULL THEN last_collect_at ELSE to_timestamp($2) END,
		    last_collect_duration_ms = $3,
		    last_collect_count = $4,
		    last_collect_error = NULLIF($6, ''),
		    consecutive_errors = $5,
		    state = 'running', updated_at = NOW()
		WHERE id = $1
	`, pluginID, lastCollectAt, status.GetLastCollectDurationMs(), status.GetLastCollectCount(),
		status.GetConsecutiveErrors(), status.GetLastCollectError())
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
