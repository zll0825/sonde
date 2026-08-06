ALTER TABLE event_outbox
    ADD COLUMN IF NOT EXISTS dedup_key TEXT,
    ADD COLUMN IF NOT EXISTS last_error TEXT,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();

CREATE UNIQUE INDEX IF NOT EXISTS idx_event_outbox_dedup
    ON event_outbox(event_type, dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_event_outbox_pending
    ON event_outbox(next_attempt_at, id) WHERE status = 'pending';

COMMENT ON COLUMN event_outbox.dedup_key IS '事件幂等键；同事件类型内唯一';
COMMENT ON COLUMN event_outbox.last_error IS '最近一次派发错误';
COMMENT ON COLUMN event_outbox.next_attempt_at IS '下次允许重试时间（指数退避）';
COMMENT ON COLUMN event_outbox.updated_at IS '最近状态更新时间';
