-- 014: 规则中文显示名。name 仍是 slug，且 UNIQUE(name, metric_id, detector_name, version)
-- 不动。display_name 可空、不进唯一键、不参与版本链比较；空值时前端回退到 name。

ALTER TABLE rules ADD COLUMN IF NOT EXISTS display_name TEXT;

COMMENT ON COLUMN rules.display_name IS
    '规则中文显示名；空则前端回退到 name（slug）。不进唯一键，不参与版本链比较';
