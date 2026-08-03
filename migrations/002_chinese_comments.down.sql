-- 002 回滚 — 清空中文注释（仅表级；列注释随表注释语义一并视为文档，逐列清空无实际价值）
COMMENT ON TABLE plugins IS NULL;
COMMENT ON TABLE entities IS NULL;
COMMENT ON TABLE metric_definitions IS NULL;
COMMENT ON TABLE relation_suggestions IS NULL;
COMMENT ON TABLE relations IS NULL;
COMMENT ON TABLE rule_suggestions IS NULL;
COMMENT ON TABLE rules IS NULL;
COMMENT ON TABLE observations IS NULL;
COMMENT ON TABLE pending_metrics IS NULL;
COMMENT ON TABLE source_preferences IS NULL;
COMMENT ON TABLE alerts IS NULL;
COMMENT ON TABLE research_snapshots IS NULL;
COMMENT ON TABLE event_outbox IS NULL;
COMMENT ON TABLE command_log IS NULL;
