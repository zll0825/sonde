-- 015: 规则与告警的 observe-only 模式。mode 不进 UNIQUE 键、不参与
-- detector 公式；缺省 live，现有行靠 DEFAULT 变成 live，无需 backfill。
-- 触发时把当时 rules.mode 快照到 alerts.mode，日后升 live 不改写历史。

ALTER TABLE rules
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'live';

ALTER TABLE alerts
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'live';

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'rules_mode_check'
    ) THEN
        ALTER TABLE rules
            ADD CONSTRAINT rules_mode_check
            CHECK (mode IN ('live', 'observe'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'alerts_mode_check'
    ) THEN
        ALTER TABLE alerts
            ADD CONSTRAINT alerts_mode_check
            CHECK (mode IN ('live', 'observe'));
    END IF;
END $$;

COMMENT ON COLUMN rules.mode IS
    '规则生命周期：live 计预算/通知；observe 只落 alerts。不进唯一键，不参与版本链比较';
COMMENT ON COLUMN alerts.mode IS
    '触发时从规则拷贝的 mode 快照；observe 不计预算、不写 outbox';
