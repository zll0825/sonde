ALTER TABLE plugins
    ADD COLUMN last_collect_duration_ms INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN plugins.last_collect_duration_ms IS '最近一次采集耗时（毫秒）';
