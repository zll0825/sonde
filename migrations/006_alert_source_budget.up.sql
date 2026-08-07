ALTER TABLE observations
    ADD COLUMN IF NOT EXISTS source_class TEXT NOT NULL DEFAULT 'unknown';

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'observations_source_class_check'
    ) THEN
        ALTER TABLE observations
            ADD CONSTRAINT observations_source_class_check
            CHECK (source_class IN ('real', 'mock', 'test', 'unknown'));
    END IF;
END $$;

ALTER TABLE alerts
    ADD COLUMN IF NOT EXISTS source_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_class TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS dedup_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_deduplicated_at TIMESTAMPTZ;

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'alerts_source_class_check'
    ) THEN
        ALTER TABLE alerts
            ADD CONSTRAINT alerts_source_class_check
            CHECK (source_class IN ('real', 'mock', 'test', 'unknown'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'alerts_dedup_count_check'
    ) THEN
        ALTER TABLE alerts
            ADD CONSTRAINT alerts_dedup_count_check
            CHECK (dedup_count >= 0);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_alerts_source_class_triggered
    ON alerts(source_class, triggered_at DESC);

COMMENT ON COLUMN observations.source_class IS '来源类别：real / mock / test / unknown；由每条 provider snapshot 显式声明';
COMMENT ON COLUMN alerts.source_provider IS '触发该告警的最新观测的数据提供方（冻结值）';
COMMENT ON COLUMN alerts.source_class IS '触发该告警的最新观测来源类别（冻结值）';
COMMENT ON COLUMN alerts.dedup_count IS 'active 告警被后续相同触发折叠的累计次数';
COMMENT ON COLUMN alerts.last_deduplicated_at IS '最近一次 active 告警去重发生时间';
