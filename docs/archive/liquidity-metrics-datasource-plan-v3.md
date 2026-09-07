# 资本流动性指标体系与免费数据源规划 v3

> 任务：`.trellis/tasks/09-07-liquidity-metrics-datasource-plan`
> v1 / v2 / v3 均为 2026-09-07。经两轮独立交叉审核。
> 审核意见：[第一轮（对 v1）](./liquidity-metrics-datasource-plan-review.md) ｜ [第二轮（对 v2，grok-4.6-build）](../.trellis/tasks/09-07-liquidity-metrics-datasource-plan/review-v2-archive.md)
> 存档：`docs/archive/liquidity-metrics-datasource-plan-v1.md`、`-v2.md`
> 网页版：https://claude.ai/code/artifact/ed3d6945-3f20-4fb6-ab52-6fc2fcf3f9a5

## 0. 修订说明

### 第一轮（v1 → v2）

v1 经独立交叉审核提出 9 条问题，v2 采纳 8 条。**但其中「不成立」的那一条裁决是错的**，见下。

| # | 审核意见 | v2 裁决 | **v3 终裁** |
|---|---|---|---|
| 1 | TGA 取的是日初余额 | 成立 | 成立 |
| 2 | FRED 许可被误写成统一开放；遗漏历史限制 | 成立 | 成立，但 v2 的修订仍不完整（见第二轮 P1-4） |
| 3 | M1/M2 剪刀差口径错误 | 成立 | 成立 |
| 4 | **周平均 vs 周三时点不可混用** | ❌ 判为不成立 | ✅ **成立。v2 判错，已撤回** |
| 5 | 研究假设写成确定因果 | 成立 | 成立 |
| 6 | 静默上线缺少对应能力 | 成立 | 成立（第二轮独立复核确认，且确认 research gate 是另一条路径） |
| 7 | 缺真实交易流动性与资金流量 | 成立 | 成立 |
| 8 | 派生引擎不应无条件成为前置 | 成立 | 成立 |
| 9 | 大表是来源目录，不是接入合同 | 成立 | 成立，但 v2 的 §7 字典本身仍有 3 处 P1 |

### 第二轮（v2 → v3）：v2 的错判与后果

**v2 §0 用 FRED 的 `frequency` 字段判定「WRESBAL/WTREGEN 是周三时点」——这是只看了 `frequency` 没看 `title`。** 序列全称（FRED API `fred/series` 实测）：

```
WALCL     Weekly, As of Wednesday   ...Total Assets...: Wednesday Level
WTREGEN   Weekly, Ending Wednesday  ...U.S. Treasury, General Account: Week Average
WRESBAL   Weekly, Ending Wednesday  ...Reserve Balances with Federal Reserve Banks: Week Average
WRBWFRBL  Weekly, As of Wednesday   ...Reserve Balances with Federal Reserve Banks: Wednesday Level
WDTGAL    Weekly, As of Wednesday   ...U.S. Treasury, General Account: Wednesday Level
```

`Ending Wednesday` 不是「周三时点」，而是「截至周三的那一周」。标题里写着 **Week Average**，H.4.1 表头也写着 `Averages of daily figures`。**第一轮审核是对的，v2 的「不成立」裁决已撤回。**

后果比数值差更严重——同一周两个口径**方向相反**（FRED observations 实测）：

| 观测日 | WRESBAL（周平均） | WRBWFRBL（周三时点） | 差额 |
|---|---:|---:|---:|
| 2026-08-26 | 2,924,936 | 2,916,824 | −8,112 |
| 2026-09-02 | 2,894,531 | 2,929,285 | **+34,754** |
| **周变动** | **−30,405（降）** | **+12,461（升）** | 方向相反 |

单位百万美元。一个 `trend` 检测器接错序列，会在同一周给出完全相反的信号。而 v2 恰恰把 `WRESBAL` 写进了「唯一可进入实施的部分」。

第二轮另外查实的三条 P1，均已在 v3 修订：

| # | 问题 | 实测证据 |
|---|---|---|
| P1-2 | `fed.ins.srf_usage` 端点不可复制 | `…/api/rp/repo/propositions/search.json` → **HTTP 400**，空 body（加日期参数仍 400）。可用路径是 `…/api/rp/results/search.json` |
| P1-3 | `fed.ins.rrp` 类别标成「流量」，且 §0 与 §7 主源自相矛盾 | 隔夜未到期余额是**存量**；§0 说优先 NY Fed，§7 却写 FRED `RRPONTSYD`（十亿单位，正是自己警告的 1000 倍陷阱） |
| P1-4 | 许可标签「无版权声明」写粗 | FRED `series/tags` 实测：`RRPONTSYD`／`SOFR`／`EFFR`／`T10Y3M`／`NFCI`／`VIXCLS` 均为 `copyrighted: citation required` |

> 第二轮只点名了 `RRPONTSYD` 与 `VIXCLS`；本轮逐条拉 `series/tags` 后发现 **6 条**标错，见 §3.2。

### v3 自查中新发现的第三个静默陷阱

**TGA 的 `account_type` 字符串在历史上换过三代命名。** v2 的验收条件写「`account_type` 精确匹配 Closing Balance 行，匹配不到即失败」——该断言会让 2022 年之前的全部回填直接失败。实测（FiscalData）：

| 期间 | `account_type` 取值 | 有无日初/日终之分 |
|---|---|---|
| 2005-10-03 → 2021-09 | `Federal Reserve Account` | **无**，单行单值 |
| 2021-10 → 2022-04 | `Treasury General Account (TGA)` | **无**，单行单值 |
| 2022-06 → 至今 | `… (TGA) Opening Balance` / `… (TGA) Closing Balance` | 有 |

过渡点分别落在 2021-09~10 与 2022-04~06 之间。回填必须按代际分支解析，且前两代的单值语义须先确定（是日初还是日终）后才能与第三代拼接。

---

## 1. 现状盘点（修正版）

v1 称"现有 16 个指标测的是价格"——**该表述错误**。逐项核对 `plugins/*/cmd/*/main.go`：

| 类别 | 指标 | 计数 |
|---|---|---|
| 价格 | `gld.ass.price`、`btc.ass.price`、`oil.energy.wti`、`metal.industrial.copper`、`metal.precious.gold`、`us.mkt.ten_year_yield`、`us.mkt.dollar_index`、`us.mkt.usd_cny`、`us.mkt.cpi`、`us.mkt.inflation_yoy` | 10 |
| 数量／存量 | `fed.ins.balance_sheet` (USD)、`gld.ass.volume` (shares)、`gld.ass.flow_proxy` (shares)、`btc.ass.hash_rate` (EH/s)、`btc.ass.tx_count`、`btc.ass.flow_proxy` (%) | 6 |

准确的表述是：**传导链的前四层里，只有 `fed.ins.balance_sheet` 一项落在 L1，L2 与 L4 完全空白**。系统并非只测价格，而是缺少「央行数量 → 融资价格 → 信用补偿」这条链上的中间变量。

同时修正 v1 的另一处不准确表述：`btc.ass.flow_proxy`（活跃地址 7 日变化）**仍在采集**，未退役。已退役的是 `gld.ass.daily_flow`、`eth.ass.daily_flow`（ETF 插件）与 `btc.ass.exchange_balance`（crypto 插件）。BTC ETF 净流入是**新增**指标，不是对某个已退役指标的替换。

---

## 2. 指标体系

### 2.0 指标说明格式

采纳审核第 5 条，每个候选指标不再写"为什么值得看"这种带结论的表述，改为五要素：

- **测量对象**：这个数字客观上在度量什么
- **可能解释**：它变化时的若干种可能读法（不唯一）
- **混杂因素**：会让解释失效的已知情形
- **不能推出**：明确禁止的推论
- **验证方法**：升级为告警规则前必须完成的检验

正文表格因宽度限制只保留「测量对象 / 主要混杂因素」，完整五要素在第一批数据字典（§7）中逐条给出；后续批次在其被排期时补齐。

### 2.1 L1 央行货币基座

| 指标 UID | 测量对象 | 主要混杂因素 | 频率 |
|---|---|---|---|
| `fed.ins.reserves` | 存款机构在美联储的准备金余额 | 准备金"充足"的阈值随监管与支付习惯漂移，无固定水平可比 | 周三 |
| `fed.ins.tga_close` | 财政部一般账户**日终**余额 | 受发债缴款、退税季、债务上限等日历因素支配，非政策信号 | 日 |
| `fed.ins.rrp` | 隔夜逆回购未到期余额（**存量**，非流量） | 已枯竭至 $6.75 亿，接近零值时比例变化无意义；**不可对日值求和** | 日 |
| `fed.ins.net_liquidity` | `WALCL − TGA − RRP` 的简化代理 | **非会计恒等式**；缺失 FIMA repo、其他负债项 | 日/周 |
| `fed.ins.srf_usage` | SRF 当日操作接受额（AM+PM 两窗**求和**） | 需区分操作类型、日内融资需求与技术性因素；**零使用须落 0，不可当缺失** | 日 |
| `fed.ins.discount_window` | 贴现窗口借款余额 | 污名效应导致低报；季末窗口粉饰 | 周 |
| `fed.ins.soma_total` | SOMA 组合面值总额 | 到期不续 vs 主动出售不可区分 | 周 |
| `ecb.ins.balance_sheet` | ECB 总资产（欧元） | 需汇率换算才能与 Fed 合并 | 周 |
| `boj.ins.balance_sheet` | BOJ 总资产 | **月频、单位 1 亿日元**，需汇率换算；v1 误写为旬频 | 月 |
| `global.ins.cb_assets` | 三大央行资产折美元合计 | 汇率变动本身即造成合计值波动，须区分汇率效应与真实扩表 | 月（受 BOJ 约束） |

> v1 称全球央行总资产可做到周频——**不成立**。JPNASSETS 为月频，合成指标频率受最低频输入约束，只能是月频。

### 2.2 L2 融资市场与信用

| 指标 UID | 测量对象 | 主要混杂因素 | 频率 |
|---|---|---|---|
| `us.mkt.sofr` | 担保隔夜融资利率中位数 | 月末/季末回购利率季节性跳升，非压力 | 日（T+1 08:00 ET 发布） |
| `us.mkt.iorb` | 准备金余额利率 | **7 日前瞻序列**，join 时须前向匹配 | 日 |
| `us.mkt.effr` | 联邦基金有效利率 | 交易量已很薄，代表性下降 | 日 |
| `us.mkt.sofr_iorb_spread` | SOFR 减 IORB，单位 bp | 与准备金稀缺**相关但不等价**；月末效应需剔除 | 日 |
| `us.mkt.sofr_tail` | SOFR 99 分位减中位数 | 尾部本身长期为正，须看相对自身分布的偏离 | 日 |
| `us.mkt.term_spread_10y3m` | 10Y 减 3M 国债利差 | 期限溢价与增长预期混合，单看无法分解 | 日 |
| `us.mkt.real_yield_10y` | 10Y TIPS 实际收益率 | 通胀风险溢价与流动性溢价混在其中 | 日 |
| `us.mkt.nfci` | 芝加哥联储金融状况指数 | 周五口径，含 105 个变量，与本系统其他指标高度共线 | 周五 |
| `us.mkt.ofr_fsi` | OFR 金融压力指数 | **滞后两个工作日**；v1 误写为当日 | 日（T+2） |
| `us.mkt.breakeven_5y5y` | 5Y5Y 远期盈亏平衡通胀率 | 含通胀风险溢价，非纯预期 | 日 |

**受许可限制、移出第一批的指标**（详见 §3.2）：

| 指标 | 序列 | 限制 |
|---|---|---|
| `us.mkt.hy_spread` | BAMLH0A0HYM2 | 历史仅自 2023-09-05；禁止未经许可复制分发 |
| `us.mkt.ig_spread` | BAMLC0A0CM | 同上 |
| `us.mkt.em_spread` | BAMLEMCBPIOAS | 同上 |

> 三年历史使**分位数类检测不可用**（无法覆盖任何一轮完整信用周期），且许可条款使前端公开展示存在法律风险。替代路径：以 `HYG`／`LQD` 相对 `IEF`／`TLT` 的价格比自建利差代理（Yahoo，需自行验证与真实 OAS 的相关性），或仅作内部研究不对外展示。**该替代方案未经验证，属候选项。**

### 2.3 L3 全球美元与跨境

| 指标 UID | 测量对象 | 主要混杂因素 | 频率 |
|---|---|---|---|
| `us.mkt.dollar_index` ✅已有 | 广义贸易加权美元指数 | 权重按贸易额，非融资敞口 | 日 |
| `us.mkt.usd_cny` ✅已有 | 在岸人民币中间价 | 受中间价管理，非自由价格 | 日 |
| `us.mkt.usd_jpy` | 美元/日元 | 套息规模无法从汇率反推 | 日 |
| `us.mkt.usd_cnh` | 离岸人民币 | 离岸池子薄，波动含流动性噪音 | 日 |

> 交叉货币基差无免费源（Bloomberg/Refinitiv 独占）。v1 建议用「DXY + EM 利差 + USDJPY 共振」代理——该代理**未经验证**，且 EM 利差现受 ICE 许可限制，降级为待研究项。

### 2.4 L4 市场微观结构与仓位

| 指标 UID | 测量对象 | 主要混杂因素 | 频率 |
|---|---|---|---|
| `us.mkt.vix` | 30 天期权隐含波动率指数 | CBOE 版权（Reprinted with permission），展示需标注 | 日 |
| `us.mkt.vix_term_structure` | VIX3M / VIX 比值 | 倒挂常见于财报与事件日，非必然压力 | 日 |
| `us.mkt.breadth_proxy` | RSP / SPY 价格比 | 含两只 ETF 的费率与再平衡差异，非纯宽度 | 日 |
| `us.mkt.margin_debt` | FINRA 融资余额 | 月频、滞后约 3 周；分母（市值）同步增长时比例未必上升 | 月 |
| `cot.*.noncomm_net` | CFTC 非商业净持仓 | **周二持仓、周五发布**，回测时不可提前到周二使用；"非商业"不等于投机 | 周 |

### 2.5 交易流动性与资金流量（采纳审核第 7 条新增）

v1 用 VIX、持仓、市场宽度代替"流动性"，但这些是**风险与仓位代理**，不度量买卖价差、深度、成交冲击或申赎。补充以下方向，均标注为**待核验**：

| 方向 | 候选免费源 | 边界 |
|---|---|---|
| 银行存款/信贷/现金资产结构 | 美联储 H.8 及 FRED 对应系列 | H.8 当前表可访问；机器可读端点待核验 |
| 实体融资供需 | 美联储 SLOOS；人民银行社融与分部门贷款 | 季频/月频，本次未逐项探测 |
| 跨境证券投资与银行资金流 | Treasury TIC；外汇局结售汇与涉外收付款 | TIC 页面可访问；低频且滞后，不能替代 EPFR |
| 国债发行/到期/财政现金 | Treasury 拍卖与财政数据 | 用于解释 TGA 扰动；接口待核验 |
| 买卖价差、深度、成交冲击 | 交易所公开盘口；由日线 OHLCV 计算 Amihud 等代理 | 代理不等于真实盘口流动性，须显式标注 |
| ETF 申赎与货基规模 | 发行人份额/NAV、ICI 公开统计 | **ETF 二级成交额 ≠ 净流入**；许可与份额口径待核验 |

> 这直接影响现有 `gld.ass.flow_proxy`（3 个月日均成交量）与 `btc.ass.flow_proxy`（活跃地址 7 日变化）的命名——两者都是**活跃度代理，不是资金流**，建议在后续迭代中改名以免误导。

### 2.6 中国子体系

| 指标 UID | 测量对象 | 主要混杂因素 | 频率 |
|---|---|---|---|
| `cn.mkt.m1_yoy` / `cn.mkt.m2_yoy` | M1 / M2 **同比增速**（货币供应量，非基础货币） | **2025-01 起 M1 纳入个人活期存款与非银支付备付金**，新旧口径不可拼接 | 月 |
| `cn.mkt.m1_m2_gap` | **M1 同比 − M2 同比，单位百分点** | 口径断裂使 2025 年前后的历史分位数不可比；须用官方可比增速 | 月 |
| `cn.mkt.social_financing` | 社会融资规模增量 | 含地方债发行节奏，非纯实体需求 | 月 |
| `cn.mkt.dr007` | 银行间存款类机构 7 天质押回购利率 | 是**市场利率**；应与央行 7 天逆回购操作利率作利差才是政策松紧 | 日 |
| `cn.mkt.dr007_omo_spread` | DR007 减 7 天逆回购操作利率 | 缴税、跨季扰动 | 日 |
| `cn.mkt.lpr_1y` / `lpr_5y` | 贷款市场报价利率（**报价**，非市场成交） | 调整离散、频率低 | 月 |
| `cn.mkt.reserve_ratio` | 存款准备金率 | 定向降准使加权平均值与公布值不一致 | 不定期 |
| `cn.mkt.margin_balance` | 两融余额 | 分母效应；标的池扩容会抬升余额 | 日 |
| `cn.mkt.cgb_10y` | 中债 10Y 国债到期收益率 | — | 日 |
| `cn.mkt.pmi_mfg` | 制造业 PMI | 扩散指数，不含强度 | 月 |
| `cn.mkt.northbound_turnover` | 陆股通成交总额 | **无买卖方向**，不能推出净流入 | 日 |
| `cn.mkt.northbound_holding` | 陆股通持股市值 | **含价格效应**，市值变化 ≠ 资金流入 | 季 |

> v1 的两处错误已修正：M1/M2 被称作"基础货币"（错，应为货币供应量）；剪刀差被写成余额相减（错，M1 含于 M2，差值恒负，不可能"转正"）。
> 采纳审核意见：M2 与社融的**原始统计应优先研究人民银行发布渠道**，不能因国家统计局 easyquery 返回 403 就直接转向非官方接口。东方财富仅作为备源。

---

## 3. 免费数据源（状态五档）

采纳审核第 9 条，废弃 v1 的「可用/不可用」二分，改为：

| 档位 | 含义 |
|---|---|
| **A 接口已验证** | 本次实测返回预期结构的数据 |
| **B 文件可下载** | 无 API 但有稳定文件下载路径 |
| **C 文档已确认、接口待测** | 官方文档描述可用，本次未打通机器读取 |
| **D 当前环境访问失败** | 本机出口 IP 受限，不代表全球不可用 |
| **E 使用条件待核实** | 可访问但许可/配额未澄清 |

### 3.1 A 档：接口已验证

| 数据源 | 端点 | 密钥 | 频率 · 发布时点 | 配额 | 实测（2026-09-07） |
|---|---|---|---|---|---|
| FRED | `api.stlouisfed.org/fred/series[/observations]` | 免费注册 | 逐序列不同，见 §3.2 | 120 req/min | 22 个序列元数据全部取回 |
| NY Fed Markets API | `markets.newyorkfed.org/api/...` | 无 | 日 · 当日 | 无公开限制 | `200`；SOFR=3.66%，含 p1/p25/p75/p99 分位 |
| Treasury FiscalData | `.../accounting/dts/operating_cash_balance` | 无 | 日 · T+1 | 无 | `200`；见下方口径说明 |
| OFR FSI | `financialresearch.gov/.../fsi.json` | 无 | 日 · **T+2 工作日** | 无 | `200`，2000 年至今 |
| CFTC Socrata | `publicreporting.cftc.gov/resource/6dca-aqww.json` | **建议申请 app token** | 周 · 周二持仓/周五发布 | 匿名限速，间歇 403 | 本次两次复测均 `200`；审核方遇 `403` → 结论是**需 token 保证稳定**，非不可用 |
| DefiLlama | `stablecoins.llama.fi/stablecoincharts/all` | 无 | 日 | 宽松 | `200`，1.2MB |
| TFTC ETF Flows | `tftc.io/bitcoin-etf-flows/data.json` | 无 | 日 · T+1 | 无 | `200`，CC BY 4.0，至 2026-09-04 |
| OKX | `okx.com/api/v5/public/{open-interest,funding-rate}` | 无 | 实时 / 8h | 20 req/2s | `200`；**仅 BTC-USDT-SWAP 单交易所单合约**，不是全市场杠杆 |
| Deribit | `deribit.com/api/v2/public/...` | 无 | 实时 | 宽松 | `200` |
| Alternative.me | `api.alternative.me/fng/` | 无 | 日 | 无 | `200`，当前 71 |
| CoinGecko | `api.coingecko.com/api/v3/...` | Demo key 可选 | 日/实时 | 100/min，10k/月 | 已在用 |
| Yahoo Finance | `query1.finance.yahoo.com/v8/finance/chart/{sym}` | 无 | 日/分钟 · 15min 延迟 | 非官方，无 SLA | `200`，**必须带 User-Agent** |
| ECB Data Portal | `data-api.ecb.europa.eu/service/data/...` | 无 | 周/月 | 无 | `200`，SDMX/CSV |
| World Bank | `api.worldbank.org/v2/...` | 无 | 年/季 | 无 | `200` |
| 东方财富 datacenter | `datacenter-web.eastmoney.com/api/data/v1/get` | 无 | 日/月 · T+1 | 未公开 | `200`：`RPT_ECONOMY_CURRENCY_SUPPLY`／`_CPI`／`_PMI`／`_DEPOSIT_RESERVE`／`RPTA_WEB_RZRQ_GGMX` |
| mempool.space | `mempool.space/api/v1/...` | 无 | 实时 | 宽松 | `200`，已在用 |
| blockchain.com | `api.blockchain.info/charts/...` | 无 | 日 | 宽松 | `200`，已在用 |
| Alpha Vantage | `alphavantage.co/query` | 免费注册 | 日 | **25 次/天** | 已在用 |

**TGA 口径（修正 v1 的错误）**

`operating_cash_balance` 端点同一 `record_date` 返回 4 行，必须按 `account_type` 筛选：

| account_type | open_today_bal | close_today_bal |
|---|---:|---|
| Treasury General Account (TGA) Opening Balance | 944,364 | `"null"` |
| Total TGA Deposits (Table II) | 336,240 | `"null"` |
| Total TGA Withdrawals (Table II) (-) | 376,676 | `"null"` |
| **Treasury General Account (TGA) Closing Balance** | **903,928** | `"null"` |

- 单位：百万美元
- **日终余额取 `account_type == 'Treasury General Account (TGA) Closing Balance'` 行的 `open_today_bal`**
- `close_today_bal` 是**字符串 `"null"`**，不是 JSON null，不可按字段名取值
- v1 引用的 `944,364M` 是日初余额，与日终相差 **404.36 亿美元**
- 历史回填须检查表结构变更（`table_nbr` / `sub_table_name` 曾调整）

### 3.2 FRED 逐序列许可与历史（采纳审核第 2 条）

FRED 是分发平台，各序列保留原始提供方的使用条件，**不能统一归为「美国政府公开数据，无署名义务」**。

v2 曾把多条序列标为「无版权声明」——**错误**。FRED 有一组 `cc`（copyright）标签，须逐序列拉 `fred/series/tags` 才能看到，notes 正文里没有。实测结果（2026-09-07）：

| 序列 | 频率 | 单位 | 历史起点 | **FRED cc 标签** |
|---|---|---|---|---|
| WALCL | Weekly, As of Wednesday（**Wednesday Level**） | Millions USD | 2002-12-18 | `public domain: citation requested` |
| **WRBWFRBL** | Weekly, As of Wednesday（**Wednesday Level**） | Millions USD | 2002-12-18 | `public domain: citation requested` |
| WRESBAL | Weekly, Ending Wednesday（**Week Average**） | Millions USD | 2002-12-18 | `public domain: citation requested` |
| WDTGAL | Weekly, As of Wednesday（**Wednesday Level**） | Millions USD | 2002-12-18 | `public domain: citation requested` |
| WTREGEN | Weekly, Ending Wednesday（**Week Average**） | Millions USD | 2002-12-18 | `public domain: citation requested` |
| **RRPONTSYD** | Daily | **Billions** USD ⚠ | 2003-02-07 | **`copyrighted: citation required`** |
| **SOFR** | Daily | Percent | 2018-04-03 | **`copyrighted: citation required`** |
| IORB | **Daily, 7-Day**（前瞻） | Percent | 2021-07-29 | `public domain: citation requested` |
| **EFFR** | Daily | Percent | 2000-07-03 | **`copyrighted: citation required`** |
| **T10Y3M** | Daily | Percent | 1982-01-04 | **`copyrighted: citation required`** |
| DFII10 | Daily | Percent | 2003-01-02 | `public domain: citation requested` |
| **NFCI** | Weekly, Ending **Friday** | Index | 1971-01-08 | **`copyrighted: citation required`** |
| **VIXCLS** | Daily, Close | Index | 1990-01-02 | **`copyrighted: citation required`**（notes 另有 CBOE "Reprinted with permission"） |
| ECBASSETSW | Weekly | **Millions of Euros** | 1999-01-01 | ECB 版权，Reprinted with permission |
| JPNASSETS | **Monthly, End of Period** | **100 Million Yen** | 1998-04-01 | BOJ 版权 |
| **BAMLH0A0HYM2** | Daily, Close | Percent | **2023-09-05** | **`copyrighted: pre-approval required`** |
| **BAMLC0A0CM** | Daily, Close | Percent | **2023-09-05** | **`copyrighted: pre-approval required`** |
| **BAMLEMCBPIOAS** | Daily | Percent | **2023-09-05** | **`copyrighted: pre-approval required`** |

三档含义与处理：

| cc 标签 | 处理 |
|---|---|
| `public domain: citation requested` | 可自由使用，展示时引用 Board of Governors |
| `copyrighted: citation required` | 可使用，**展示必须署名**原始提供方（NY Fed / CBOE / Chicago Fed） |
| `copyrighted: pre-approval required` | **须事先书面许可**。ICE 三条 notes 另有明文：`Reproduction of this data in any form is prohibited except with the prior written permission of ICE Data Indices` |

ICE 三条的额外限制：`Starting in April 2026, this series will only include 3 years of observations.` —— `observation_start` 实测均为 2023-09-05。三年历史覆盖不了任何完整信用周期，**percentile 类检测在这三条上不成立**，已移出第一批。

替代路径：以 `HYG`／`LQD` 相对 `IEF`／`TLT` 的价格比自建利差代理（Yahoo），或仅作内部研究不对外展示。**该替代方案未经验证，属候选项。**

### 3.3 B 档：文件可下载（无 API）

| 数据源 | 用途 | 说明 |
|---|---|---|
| FINRA 融资余额 | `us.mkt.margin_debt` | XLSX 稳定下载，月频滞后约 3 周。v1 归为"不可用"**错误**，正确定性是"需 HTML 定位链接 + XLSX 解析" |

### 3.4 C 档：文档已确认、接口待测

| 数据源 | 用途 | 待办 |
|---|---|---|
| 美联储 H.8 | 银行存款/信贷/现金资产结构 | 当前表可访问，机器端点待核验 |
| Treasury TIC | 跨境证券投资 | 页面可访问，低频且滞后 |
| 美联储 SLOOS | 银行信贷标准 | 季频，本次未探测 |
| Treasury 拍卖数据 | 解释 TGA 扰动 | 接口待核验 |
| NY Fed ACM 期限溢价 | `us.mkt.term_premium` | CSV 地址稳定性待核验 |
| CBOE 免费 CSV | Put/Call、SKEW | 端点待核验 |
| 人民银行发布渠道 | M2、社融**原始**统计 | **优先于东方财富**，须先研究 |
| chinamoney.com.cn | DR007、中债收益率曲线 | 接口形态与反爬待核验 |
| 东财 社融/LPR/中债 | 中国侧补全 | 已排除 6 个 reportName（均 `9501 报表配置不存在`），需抓包 `data.eastmoney.com/cjsj/*` |
| SEC EDGAR | 13F 持仓 | 季频，解析成本高 |

### 3.5 D 档：当前环境访问失败

> 本机出口 IP 受限，**不构成"全球无可用来源"的结论**。

| 数据源 | 实测 | 处理 |
|---|---|---|
| Binance Futures | `451` 地域限制 | 改用 OKX（已验证） |
| Bybit v5 | `403` CloudFront 国家封锁 | 改用 OKX + Deribit |
| Farside Investors | `403` Cloudflare | 改用 TFTC；**注意 TFTC 混合 SoSoValue 与 Farside 整理数据，不是 Farside 镜像** |
| 国家统计局 easyquery | `403` WAF | 优先研究人行渠道，东财仅作备源 |
| shibor.org | 连接超时 `000` | 待重试／换网络环境 |

### 3.6 E 档 / 明确放弃

| 数据源 | 结论 |
|---|---|
| iShares AJAX CSV | 返回 HTML，端点形态待重新定位 |
| MOVE 指数 | ICE 专有，无免费源 |
| 交叉货币基差 | Bloomberg/Refinitiv 独占；代理方案未经验证 |
| EPFR / IIF | 商业订阅，**明确放弃**；TIC 可部分替代但频率与口径不同 |
| 陆股通日频净流入 | **数据源本身已停止存在**（2024-08-19），非技术问题 |

---

## 4. 发布时点契约（采纳审核第 4 条）

每个指标必须登记以下时间字段，检测与回测**只能使用信号时点已发布的数据**：

| 字段 | 含义 |
|---|---|
| `observation_period` | 观测所属期间（如 2026-09-03 当日、2026-08 当月） |
| `first_available_at` | 该观测最早可获取的时刻（含时区） |
| `fetched_at` | 本系统实际抓取时刻 |
| `vintage` | 修订版本（FRED ALFRED 可提供） |
| `max_staleness` | 超过此时长未更新即判定采集异常 |

已核实的时点：

| 指标 | 观测期 → 发布 |
|---|---|
| SOFR | 前一工作日交易 → 次日约 08:00 ET |
| OFR FSI | **T-2 工作日** → 每交易日计算 |
| CFTC COT | **周二持仓** → **周五 15:30 ET** 发布（回测不得提前至周二使用） |
| H.4.1 **Wednesday Level** 列（WALCL / WRBWFRBL / WDTGAL） | 周三时点 → 次日周四发布 |
| H.4.1 **Week Average** 列（WRESBAL / WTREGEN） | 截至周三的一周日均 → 次日周四发布。**与上一行不可混用** |
| TGA (FiscalData) | 当日日终 → T+1 |
| JPNASSETS | 月末 → 次月初 |
| FINRA margin | 月末 → 次月第三周 |

周频/月频经 `forward_fill` 得到的日频值**必须标注为估计值**，不可与真实日频观测混同参与分位数统计。

---

## 5. 观察模式（observe-only）—— 采纳审核第 6 条

v1 建议"新指标以 `severity: info` 且不推送上线"。**代码核实证明该建议当前无法执行**：

`cmd/core/main.go` 的 outbox handler 在 `alert.EventTypeAlertTriggered` 分支中：

- 无条件调用 `budgetTracker.Record(ctx, parsed.RuleID, parsed.Title, time.Now())` —— 计入噪音预算
- 无条件调用 `n.Notify(ctx, parsed)` —— 派发通知

全链路没有任何按 severity 过滤的分支；`severity` 仅用于日志字段（`Str("severity", ...)`）、聚类优先级与 Telegram 消息格式化（`internal/core/notifier/telegram.go:86`）。

**修订后的要求（作为第一批的一部分交付）**：

| 方案 | 说明 |
|---|---|
| 方案 A（最小） | 新指标**只采集、不建规则**。零代码改动，但也拿不到误报分布 |
| 方案 B（推荐） | 新增规则级 `mode: observe` 字段：照常检测并落 `alerts` 表，但**不计入噪音预算、不调用 notifier**。需改 outbox handler 与 rule schema |

必须显式定义：是否落告警表、是否计预算、是否触发通知。三者独立，不能靠 severity 一个字段兼任。

**观察期修正**：v1 的"观察两周"仅对日频指标成立。月频指标两周内可能**没有任何新观测**，周频只有约 2 个点。阈值标定须结合历史回放与压力样本，并按频率分别设定最少观测数：

| 频率 | 最少观测数 | 对应时长 |
|---|---|---|
| 日频 | 60 | 约 3 个月 |
| 周频 | 52 | 约 1 年 |
| 月频 | 36 | 约 3 年（受 ICE 三年历史限制的序列不适用） |

---

## 6. 派生指标：能力缺口的准确表述（采纳审核第 8 条）

v1 称"检测器只接受单指标，所以派生引擎是前置条件"——**该推理不成立**。插件侧已经在生产派生量：

- `plugins/crypto` 计算并上报 `btc.ass.flow_proxy`（活跃地址 7 日变化）
- `plugins/etf` 计算并上报 `gld.ass.flow_proxy`（3 个月日均成交量）
- `plugins/macro/bindings.yaml` 用 FRED `units: pc1` 做同比变换得到 `us.mkt.inflation_yoy`

准确的缺口是：**缺少跨来源、跨频率的派生编排与血缘管理**（多个插件的指标做运算、周×日对齐、派生值可追溯到输入 vintage）。

因此派生引擎**不是所有接入的前置条件**，而是一个需要单独评估的设计决策：

| 方案 | 适用 | 成本 |
|---|---|---|
| 插件内计算 | 输入同源同频（如 SOFR 与 SOFR-p99 都来自 NY Fed 同一响应） | 低，立即可用 |
| 通用派生引擎 | 跨插件、跨频率（净流动性 = FRED 周频 + FiscalData 日频 + NY Fed 日频） | 高，需血缘与对齐语义 |

**第一批采取的路径**：原始指标（准备金、SOFR、TGA、RRP、OFR FSI）全部独立接入，不依赖任何新引擎；`sofr_iorb_spread` 与 `sofr_tail` 在插件内计算（输入同源）；`net_liquidity` 因跨三个来源与两种频率，**先以离线脚本产出并验证增量信息，验证通过后再决定引擎方案**。

检测器方面同样修正：`vix_term_structure` 生成后现有 `threshold` 已可检测 `< 1`；只有在需要严格的"首次穿越事件"语义时才需要新增 `cross`。`zscore` 与 `divergence` 的必要性应在第二批具体规则设计时论证，不预先列为必做。

---

## 7. 第一批逐指标数据字典

以下为**唯一可进入实施的部分**。其余指标保留为候选池，在其被排期时补齐同等字典。
v2 的本节含 3 处 P1（准备金序列、SRF 端点、RRP 类别与主源），均已修订。

### 7.1 原始采集指标

所有 FRED 请求形如 `https://api.stlouisfed.org/fred/series/observations?series_id=<ID>&api_key=$FRED_API_KEY&file_type=json`，回填加 `observation_start` / `observation_end`。

| metric_id | 类别 | 主源 · 完整请求 | 字段与筛选 | 原始单位 → USD/% | 频率 · 发布时点 | 历史起点 | 许可 |
|---|---|---|---|---|---|---|---|
| `fed.ins.balance_sheet` ✅已有 | 存量 | FRED `WALCL` | `value` | 百万USD **×1e6** | 周三时点 · 次日周四 | 2002-12-18 | public domain |
| `fed.ins.reserves` | 存量 | FRED **`WRBWFRBL`**（Wednesday Level，H.4.1 Table 1） | `value` | 百万USD **×1e6** | **周三时点** · 次日周四 | 2002-12-18 | public domain |
| `fed.ins.tga_close` | 存量 | FiscalData `…/v1/accounting/dts/operating_cash_balance` | 见 §7.1.1 三代分支 | 百万USD **×1e6**，且**值是字符串** | 日 · T+1 | **2005-10-03** | 美国政府公开 |
| `fed.ins.rrp` | **存量**（隔夜未到期余额） | **NY Fed** `…/api/rp/results/search.json?startDate=&endDate=` | `repo.operations[]` 中 `operationType=="Reverse Repo"` 的 `totalAmtAccepted` | **已是 USD（×1）** | 日 · 当日 | 依端点 | NY Fed，署名 |
| `fed.ins.rrp` 备源 | 存量 | FRED `RRPONTSYD` | `value` | **十亿USD ×1e9**（与 WALCL 的 ×1e6 差 1000 倍） | 日 | 2003-02-07 | **`copyrighted: citation required`** |
| `fed.ins.srf_usage` | 流量 | **NY Fed** `…/api/rp/results/search.json?startDate=&endDate=` | `operationType=="Repo"` **且** `operationMethod=="Full Allotment"`，**按 `operationDate` 对 AM+PM 两窗求和** | 已是 USD（×1） | 日 · 当日 | 依端点 | NY Fed，署名 |
| `us.mkt.sofr` | 价格 | NY Fed `…/api/rates/secured/sofr/last/1.json`；回填用 `…/api/rates/secured/sofr/search.json?startDate=&endDate=` | `refRates[].percentRate` | % （×1） | 日 · **T+1 08:00 ET** | 2018-04-03 | NY Fed，署名 |
| `us.mkt.sofr_p99` | 价格 | 同上响应 | `refRates[].percentPercentile99` | % （×1） | 日 · T+1 08:00 ET | 2018-04-03 | NY Fed，署名 |
| `us.mkt.iorb` | 价格 | FRED `IORB` | `value` | % （×1） | **Daily, 7-Day（前瞻）** | 2021-07-29 | public domain |
| `us.mkt.effr` | 价格 | FRED `EFFR` | `value` | % （×1） | 日 | 2000-07-03 | **`copyrighted: citation required`** |
| `us.mkt.real_yield_10y` | 价格 | FRED `DFII10` | `value` | % （×1） | 日 | 2003-01-02 | public domain |
| `us.mkt.term_spread_10y3m` | 价格 | FRED `T10Y3M` | `value` | % （×1） | 日 | 1982-01-04 | **`copyrighted: citation required`** |
| `us.mkt.nfci` | 指数 | FRED `NFCI` | `value` | index （×1） | **周五** | 1971-01-08 | **`copyrighted: citation required`** |
| `us.mkt.ofr_fsi` | 指数 | OFR `https://www.financialresearch.gov/financial-stress-index/data/fsi.json` | `OFRFSI.data[]` 为 `[毫秒时间戳, 值]` 二元组 | index （×1） | 日 · **T+2 工作日** | 2000-01-03 | 美国政府公开 |

每个指标还须登记 §4 的五个时间字段（`observation_period` / `first_available_at` / `fetched_at` / `vintage` / `max_staleness`）。`max_staleness` 建议：日频源 3 个自然日、周频源 10 天、月频源 45 天。

#### 7.1.1 TGA 的三代 `account_type` 分支（回填必读）

`account_type` 字符串换过三代命名，**不可写成单一精确匹配**：

| 期间 | `account_type` | 取值 |
|---|---|---|
| 2005-10-03 → 2021-09 | `Federal Reserve Account` | 单行单值，**无日初/日终之分** |
| 2021-10 → 2022-04 | `Treasury General Account (TGA)` | 单行单值，**无日初/日终之分** |
| 2022-06 → 至今 | `Treasury General Account (TGA) Closing Balance` | 取该行的 **`open_today_bal`** |

- 过渡点落在 2021-09~10 与 2022-04~06 之间，实施时须精确定位
- 前两代的单值语义（日初还是日终）**必须先查证**，否则拼接会在过渡点造成阶跃
- `close_today_bal` 恒为**字符串 `"null"`**；`open_today_bal` 也是字符串（如 `"903928"`），解析时不可假定为 number
- 2026-09-03 实测：Opening=944,364 / Closing=**903,928**，差 $404.36 亿

### 7.2 派生指标（第一批仅离线验证，不入告警）

**`us.mkt.sofr_iorb_spread`** — 插件内计算（NY Fed + FRED）

- 测量对象：担保隔夜融资利率相对准备金付息利率的偏离，单位 bp
- 可能解释：准备金供给相对需求趋紧；或月末/季末资产负债表约束；或抵押品供给冲击
- 混杂因素：月末与季末系统性跳升；**IORB 为 7 日前瞻序列**（`observation_end` 跑在 SOFR 之前），须按「SOFR 有效日 ≤ IORB 生效日」前向匹配，不可按日期直接 join
- **不能推出**：利差为正 ≠ 准备金不足；单日跳升 ≠ 政策转向
- 验证方法：剔除月末/季末后，检验对 3 个月后准备金变化与 SRF 使用的领先性；样本外检验必需

**`us.mkt.sofr_tail`** — 插件内计算（NY Fed 同一响应）

- 测量对象：SOFR 99 分位与中位数之差
- 可能解释：回购市场尾部摩擦上升
- 混杂因素：尾部长期为正，绝对值无意义，须看相对自身分布的偏离
- **不能推出**：不能推出「个别机构借不到钱」——分位数不披露交易对手身份
- 验证方法：与 SRF 使用量、贴现窗口借款的同期与滞后相关性

**`fed.ins.net_liquidity`** — **离线脚本，不入库、不告警**

- 测量对象：`总资产 − TGA − RRP` 的简化代理
- **不是会计恒等式**：缺失 FIMA repo、其他存款、流通中货币等项
- **口径必须同列**：三项须同为周三时点（`WALCL` + `WDTGAL` + 周三时点 RRP），或同为日频真值源（日频 TGA 走 FiscalData、日频 RRP 走 NY Fed，总资产只有周频，须显式标注为 forward-fill 估计）。**绝不可把 `WRESBAL`/`WTREGEN` 的周平均与 `WALCL` 的周三时点混用**——实测同一周两口径方向可以相反
- 混杂因素：单位必须先统一到 USD，**`RRPONTSYD` 为十亿而 WALCL/WDTGAL 为百万**
- **不能推出**：v1 断言「与 SPX 相关性远高于任何单项」**无任何验证依据，已撤回**
- 验证方法：(1) 用同一 H.4.1 列或日频真值源重建；(2) 与准备金单项、金融状况指数比较，检验是否提供**增量**信息；(3) 样本外检验；(4) 通过后再讨论是否升级为告警指标

### 7.3 第一批验收条件

- [ ] 每个指标的 `observation_period` / `first_available_at` / `fetched_at` / `vintage` / `max_staleness` 均落库
- [ ] **序列口径断言**：`fed.ins.reserves` 绑定 `WRBWFRBL`；若有人改成 `WRESBAL`，测试必须失败（两者语义不同）
- [ ] 单位换算单元测试：**`RRPONTSYD` ×1e9 与 `WALCL` ×1e6 各有独立用例**
- [ ] TGA 采集按三代 `account_type` 分支解析；**当前代匹配不到即失败**，历史代走各自分支；值按字符串解析
- [ ] SRF 采集用 `results/search.json`，按 `operationType` + `operationMethod` 筛选并对当日多窗**求和**；**零使用落 0，不落缺失**
- [ ] RRP 主源为 NY Fed（USD 原值）；FRED 备源路径有 ×1e9 用例
- [ ] SOFR−IORB 的前向匹配有跨越 IORB 生效日边界的用例
- [ ] 全部以 observe-only 模式上线（§5 方案 A 或 B），噪音预算无增量
- [ ] `net_liquidity` 仅存在于离线脚本，未进入 `metrics` 注册
- [ ] 展示层对 `copyrighted: citation required` 的 6 条序列（RRPONTSYD/SOFR/EFFR/T10Y3M/NFCI/VIXCLS）标注原始提供方

## 8. 修订后的落地顺序

采纳审核的「建议修订顺序」：

**第 0 步 · 修正与契约（本文档已完成）**
TGA 口径、FRED 逐序列许可与历史、M1/M2 定义、发布时点契约、第一批数据字典。

**第一批 · 准备金与融资价格**
§7 的 13 个原始指标 + 2 个插件内派生。全部 observe-only。`net_liquidity` 保留为离线待验证项，不入库。

**第二批 · 银行信用 · 跨境 · 交易流动性**
按审核第 3 条要求，不按接口易用程度排序，而是为中美各保留最小观测闭环：美国侧 H.8 + TIC + 拍卖数据；中国侧优先打通人民银行渠道的 M2/社融，以及 DR007 与 7 天逆回购的利差。

**第三批 · 仓位与加密**
CFTC COT（申请 app token）、FINRA margin（XLSX 解析）、DefiLlama、TFTC、OKX、Deribit。加密指标须显式标注单交易所单合约的覆盖范围。

**第四批 · 告警与派生引擎**
在第一至三批有足够历史与实时样本、且离线验证证明增量信息之后，再开启告警规则；通用派生引擎作为独立设计决策评估，与插件内计算方案作成本对比。

---

## 9. 风险清单（修订版）

1. **ICE 许可**是本规划最大的单点法律风险。HY/IG/EM 利差在获得书面许可前，不得在任何对外界面展示。
2. **单位与口径**：净流动性的 1000 倍单位陷阱、TGA 日初/日终、M1 新旧口径断裂——三者都会静默产出"看起来合理"的错值。全部需要断言而非注释。
3. **观察模式尚不存在**，必须先交付方案 A 或 B，否则新指标一上线就击穿噪音预算。
4. **代理不等于本体**：`flow_proxy` 是活跃度不是资金流，成交额不是净流入，季度持股市值变化含价格效应，OKX 单合约 OI 不是全市场杠杆。命名与前端文案都需修正。
5. **东方财富是非公开接口**，无 SLA，需独立限速与插件级故障隔离；且应作为人民银行渠道的备源而非首选。
6. **配额**：Alpha Vantage 25 次/天，CoinGecko 10k/月，CFTC 匿名限流间歇 403（需 app token）。
7. **署名义务**：TFTC 为 CC BY 4.0，必须标注。FRED 的 `cc` 标签须逐序列拉 `series/tags` 才能看到——`RRPONTSYD`／`SOFR`／`EFFR`／`T10Y3M`／`NFCI`／`VIXCLS` 六条均为 `copyrighted: citation required`，展示必须署名原始提供方（v2 曾全部误标为「无版权声明」）。
8. **`flow_proxy` 文案与实现不符**：catalog 写「活跃地址 7 日变化」，`plugins/crypto/internal/collector/coingecko.go` 的 `fetchCoinInfo` 实际用 `GetTransactionHistory` 算的是**交易笔数** 7 日变化。不挡第一批，但需排期改名。
