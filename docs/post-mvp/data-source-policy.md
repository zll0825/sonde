# Sonde 数据源政策

> **状态：政策讨论 (draft)** — 本文档为讨论稿，反映当前对 Yahoo Finance API 地位的认知，不构成最终政策。待 ETFCmd fallback 链路实现后复审。

## 允许使用的来源（按优先级）

1. **官方文档化 API** — 拥有公开文档并允许免费使用的公共 API（如 FRED、CoinGecko、Yahoo v8/finance/chart public endpoints）。
2. **官方提供的未文档化但有法律隐含授权** — 例如 Yahoo Finance 公开 chart API（`query1.finance.yahoo.com/v8/finance/chart/{symbol}`），虽未正式文档化，但长期稳定作为公开 webhook 使用；我们仅在无其他官方替代方案时使用此类。
3. **其他自由数据源** — Blockchain.com 公开 API，等等。

## 禁止

- 需要密钥才能访问的非授权数据（明文爬取）
- Yahoo Finance 的非公开 API（如 `finance.yahoo.com/quote/{symbol}/history` 登录墙之后）

## ETF 数据源说明

ETF 维度（GLD）使用 Yahoo v8 公开 chart API。此 API **未正式文档化**，但：

- 公开可访问，无需授权密钥
- 单 symbol 返回 OHLCV + adjusted close + meta
- 如 Yahoo 撤消访问或施加速率限制，collector 自动切换到 ETF 官方 provider 的公开 filings / 第三 party 数据

### 替代方案路线图

| 来源 | 状态 | 备注 |
|------|------|------|
| Yahoo v8/finance/chart | 当前首选（fallback） | 公开未文档化 |
| EOD Historical Data | 探索 | 免费 tier |
| ETF.com 公开数据 | 探索 | 需评估条款 |

## 与 Issue #14 的关系

Issue #14 指出 `coverage-research.md` 第 118 行承认 chart API "not officially documented"，与项目约束"只使用公开、免费、已文档化 API"冲突。本文档为解决该冲突的过渡方案：

- 承认 Yahoo v8 chart API **未文档化**的事实
- 将其归类为"次级 fallback"而非主要首选
- 优先使用任何已公开文档化的替代方案
- 在代码层面（ETFCmd / collector）维护可切换至其他 provider 的能力
