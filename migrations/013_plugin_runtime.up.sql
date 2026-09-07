-- 013: 持久化插件心跳上报的 runtime 元数据。
--
-- 动机：/api/status 此前用 os.Getenv 在 API 进程里判断插件密钥是否就绪，
-- 但密钥只存在于插件容器——macro / commodities 拿着真实密钥正常采集，首页
-- 却一直显示「缺少密钥」。判断必须由持有密钥的插件做出，随心跳带回来，
-- 因此需要一个落点。
--
-- PluginStatus.runtime（proto 字段 7）本来就在传，只是从未被持久化。
-- 这一列同时接住 circuit_state 等既有 runtime 键。

ALTER TABLE plugins ADD COLUMN IF NOT EXISTS runtime JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN plugins.runtime IS
    '插件心跳上报的运行时元数据：circuit_state、secret.<KEY>=present|missing 等';
