ALTER TABLE command_log
    ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_dispatched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error TEXT,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Rows dispatched by an older Core have no recoverable deadline. Make them
-- immediately eligible for the new lease claimant without changing history.
UPDATE command_log
SET lease_expires_at = NOW(), updated_at = NOW()
WHERE status = 'dispatched' AND lease_expires_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_command_log_dispatchable
    ON command_log(status, lease_expires_at, requested_at)
    WHERE status IN ('pending', 'dispatched');

COMMENT ON COLUMN command_log.attempts IS '原子领取命令的累计次数；最多 5 次';
COMMENT ON COLUMN command_log.last_dispatched_at IS '最近一次领取并准备派发的时间';
COMMENT ON COLUMN command_log.lease_expires_at IS 'dispatched 状态的租约到期时间；到期可被重新领取';
COMMENT ON COLUMN command_log.last_error IS '最近一次派发、租约或插件执行错误；完成后仍保留历史';
COMMENT ON COLUMN command_log.updated_at IS '最近一次命令生命周期状态更新时间';
