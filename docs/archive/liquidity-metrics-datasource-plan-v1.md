# 资本流动性指标体系与免费数据源规划

> 任务：`.trellis/tasks/09-07-liquidity-metrics-datasource-plan`
> 撰写日期：2026-09-07 ｜ 所有"可用性"结论均来自当日实测（curl 探测），非文档推断。

## 0. 结论先行

当前 Sonde 的 16 个指标**测的是价格，不是流动性**。资本市场流动性的驱动链条是：

```
央行资产负债表 → 银行准备金 → 回购/融资市场 → 信用利差 → 风险资产定价
    (源头)         (数量)        (价格/摩擦)     (风险偏好)    (结果·当前系统只看到这一层)
```

系统目前只观测最末端的"结果层"，因此**只能事后确认，无法提前预警**。本规划补齐前四层。

三个必须先接受的现实约束（实测得出，不是文献结论）：

1. **RRP 已经枯竭**。2026-09-04 隔夜逆回购接受量仅 **$6.75 亿**（2022 年峰值 $2.5 万亿）。经典"净流动性 = 美联储资产 − TGA − RRP"公式里的 RRP 项已经丧失信息量，边际观测对象必须转向**准备金余额**与 **SRF（常备回购便利）使用量**。
2. **北向资金日频净流入已不存在**。自 2024-08-19 起沪深交易所停止披露，实测东方财富 `RPT_MUTUAL_DEAL_HISTORY` 接口的 `FUND_INFLOW` / `NET_DEAL_AMT` 字段返回 `null`。任何以"北向资金日流入"为核心的中国指标设计都是死路，必须改用季度持股市值变化 + 日频成交总额。
3. **Binance / Bybit 从本机出口 IP 被地域封锁**（451 / 403）。加密衍生品数据必须走 **OKX + Deribit**，二者实测均正常返回。

---

## 1. 现状盘点

| 插件 | 指标 | 数据源 | 层级定位 |
|---|---|---|---|
| macro | `fed.ins.balance_sheet` (WALCL) | FRED | L1 源头 ✅ |
| macro | `us.mkt.ten_year_yield` (DGS10) | FRED | L5 结果 |
| macro | `us.mkt.dollar_index` (DTWEXBGS) | FRED | L3 外溢 |
| macro | `us.mkt.usd_cny` (DEXCHUS) | FRED | L3 外溢 |
| macro | `us.mkt.cpi` / `us.mkt.inflation_yoy` | FRED | 宏观背景 |
| commodities | `oil.energy.wti` / `metal.industrial.copper` / `metal.precious.gold` | FRED + Alpha Vantage | L5 结果 |
| etf | `gld.ass.price` / `volume` / `flow_proxy` | Yahoo Finance | L5 结果 |
| crypto | `btc.ass.price` / `hash_rate` / `tx_count` / `flow_proxy` | CoinGecko + blockchain.com | L5 结果 |

**覆盖度：L1 仅 1 个指标，L2 完全空白，L3 只有汇率，L4 完全空白。**

检测器现有 5 类：`threshold` / `percentile` / `trend` / `moving_average` / `volatility`，全部是**单指标**检测。

---

## 2. 指标体系设计（五层模型）

### L1 央行货币基座 —— 流动性的"水源"

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `fed.ins.reserves` | 银行准备金余额 (WRESBAL) | **最重要的单一流动性指标**。准备金充裕→风险资产有底；跌破"充足准备金"阈值→回购市场立刻抽搐 | 周 |
| `fed.ins.tga` | 财政部一般账户余额 | TGA 上升＝财政部从市场抽水（发债缴款），下降＝放水。发债高峰期是流动性最大扰动项 | **日** |
| `fed.ins.rrp` | 隔夜逆回购规模 | 已枯竭至 $6.75 亿。**观测意义已从"排水量"转为"是否重新累积"**——重新上升意味着流动性再度过剩 | 日 |
| `fed.ins.net_liquidity` | **净流动性**（派生） | `WALCL − TGA − RRP`。与 SPX 的相关性远高于任何单项。**本规划的头号交付物** | 日/周 |
| `fed.ins.srf_usage` | SRF 常备回购便利使用量 | RRP 枯竭后的新边际。非零使用＝有机构在公开市场借不到钱，是准备金稀缺的**硬证据** | 日 |
| `fed.ins.discount_window` | 贴现窗口借款 | 污名化工具，一旦放量即为银行体系急性压力（2023 SVB 时飙升） | 周 |
| `fed.ins.soma_total` | SOMA 组合总额 | QT 实际执行进度，比声明的缩表节奏更真实 | 周 |
| `ecb.ins.balance_sheet` | ECB 总资产 | 全球流动性第二极 | 周 |
| `boj.ins.balance_sheet` | BOJ 总资产 | 日元套息交易的源头，2024-08 套息平仓即由此触发 | 旬 |
| `global.ins.cb_assets` | **全球央行总资产**（派生） | Fed+ECB+BOJ 折美元合计同比。跨市场风险偏好的共同驱动 | 周 |

### L2 融资市场与信用 —— 流动性的"管道压力"

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `us.mkt.sofr` | SOFR 担保隔夜融资利率 | 回购市场基准价 | 日 |
| `us.mkt.iorb` | 准备金余额利率 | SOFR 的政策锚 | 日 |
| `us.mkt.sofr_iorb_spread` | **SOFR − IORB 利差**（派生） | **准备金稀缺度最灵敏的温度计**。持续为正且走阔＝准备金不足，早于任何价格信号 | 日 |
| `us.mkt.sofr_tail` | **SOFR 99分位 − SOFR**（派生） | 回购市场尾部摩擦。NY Fed API 直接给出 `percentPercentile99`，中位数正常但尾部飙升＝个别机构已经借不到钱 | 日 |
| `us.mkt.hy_spread` | 高收益债利差 (BAMLH0A0HYM2) | **风险偏好的黄金标准**，比 VIX 更少噪音、更难被操纵 | 日 |
| `us.mkt.ig_spread` | 投资级利差 (BAMLC0A0CM) | 与 HY 利差的比值可区分"系统性风险"与"垃圾债个别风险" | 日 |
| `us.mkt.term_spread_10y2y` | 10Y−2Y 期限利差 (T10Y2Y) | 曲线形态 | 日 |
| `us.mkt.term_spread_10y3m` | 10Y−3M 期限利差 (T10Y3M) | 衰退预测力优于 10Y2Y（NY Fed 官方模型采用） | 日 |
| `us.mkt.term_premium` | ACM 10Y 期限溢价 | 区分"收益率上升是因为增长预期"还是"因为风险补偿"——对判断股债双杀至关重要 | 日 |
| `us.mkt.real_yield_10y` | 10Y TIPS 实际利率 (DFII10) | **黄金与 BTC 的估值锚**。现有系统有金价却没有实际利率，等于有果无因 | 日 |
| `us.mkt.nfci` | 芝加哥联储金融状况指数 | 105 个变量的综合金融状况 | 周 |
| `us.mkt.ofr_fsi` | OFR 金融压力指数 | **日频**综合压力指数，比周频 NFCI 更及时 | 日 |
| `us.mkt.inflation_breakeven_5y5y` | 5Y5Y 远期通胀预期 (T5YIFR) | 通胀预期脱锚是政策转向的前提 | 日 |

### L3 全球美元与跨境 —— 流动性的"外溢"

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `us.mkt.dollar_index` | 美元指数 ✅已有 | 全球美元融资松紧的总价格 | 日 |
| `us.mkt.usd_jpy` | 美元/日元 (DEXJPUS) | 套息交易规模的代理，急速升值＝平仓潮 | 日 |
| `us.mkt.em_spread` | 新兴市场公司债利差 (BAMLEMCBPIOAS) | 美元流动性收紧首先打击 EM | 日 |
| `us.mkt.usd_cnh` | 离岸人民币 | 与在岸 CNY 的价差反映资本外流压力 | 日 |

> 交叉货币基差（XCCY basis）是判断离岸美元荒的最优指标，但**无免费数据源**（Bloomberg/Refinitiv 独占）。用「DXY 急升 + EM 利差走阔 + USDJPY 大幅波动」三者共振作代理。

### L4 市场微观结构与仓位 —— 流动性的"承接力"

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `us.mkt.vix` | VIX (VIXCLS) | 隐含波动率基准 | 日 |
| `us.mkt.vix_term_structure` | **VIX3M / VIX 比值**（派生） | **比 VIX 绝对值有用得多**。比值 < 1（倒挂）＝市场定价"急性、近在眼前"的风险，历史上是最可靠的择时信号之一 | 日 |
| `us.mkt.hy_vix_divergence` | **HY 利差 vs VIX 背离**（派生） | 信用市场与股票波动率给出矛盾信号时，信用市场通常是对的 | 日 |
| `us.mkt.breadth_proxy` | **RSP/SPY 比值**（派生） | 等权 vs 市值加权，市场宽度代理。比值持续下行＝上涨只靠少数大票，脆弱 | 日 |
| `us.mkt.margin_debt` | FINRA 融资余额 | 杠杆总量。2026-07 同比 +38.6%，是过热信号 | 月（滞后3周） |
| `cot.spx.noncomm_net` | COT 标普500 非商业净持仓 | 投机仓位极值＝反转风险 | 周（周五） |
| `cot.ust10y.noncomm_net` | COT 10Y 国债非商业净持仓 | 债市拥挤交易 | 周 |
| `cot.gold.noncomm_net` | COT 黄金非商业净持仓 | 现有系统有金价无仓位 | 周 |
| `cot.usd.noncomm_net` | COT 美元指数非商业净持仓 | 美元多头拥挤度 | 周 |

### L5 加密与另类 —— 边际资金的"先行指标"

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `crypto.stablecoin.total_supply` | 稳定币总供应量 | **加密世界的 M2**。供应扩张＝新法币入场，是币价的领先量能指标 | 日 |
| `crypto.stablecoin.supply_change_7d` | 稳定币 7 日净增（派生） | 边际资金流向，比存量更敏感 | 日 |
| `btc.ass.etf_net_flow` | **美国现货 BTC ETF 净流入** | 机构资金的直接观测口。**替代已退役的合成 flow 指标** | 日 |
| `btc.ass.funding_rate` | 永续合约资金费率 | 杠杆多空失衡，极值预示强平 | 8小时 |
| `btc.ass.open_interest` | 永续未平仓合约 | 杠杆总量；OI 高 + 资金费率极端 ＝ 挤压前夜 | 实时 |
| `btc.ass.dvol` | Deribit 隐含波动率指数 | 加密版 VIX | 日 |
| `crypto.mkt.fear_greed` | 恐惧贪婪指数 | 情绪极值反指 | 日 |

### C. 中国侧 —— 独立子体系

| 指标 UID | 名称 | 为什么值得看 | 频率 |
|---|---|---|---|
| `cn.mkt.m1` / `cn.mkt.m2` | M1 / M2 货币供应 | 基础货币量 | 月 |
| `cn.mkt.m1_m2_gap` | **M1−M2 剪刀差**（派生） | **中国最有效的流动性指标**。剪刀差收窄/转正＝资金从定期活化为交易性需求，领先 A 股与房地产 | 月 |
| `cn.mkt.social_financing` | 社会融资规模增量 | 实体信用扩张总闸门 | 月 |
| `cn.mkt.reserve_ratio` | 存款准备金率 | 政策宽松的离散信号 | 不定期 |
| `cn.mkt.margin_balance` | 两融余额 | A 股杠杆资金，散户风险偏好 | 日 |
| `cn.mkt.dr007` | DR007 银行间质押回购利率 | 中国版 SOFR，央行实际政策利率锚 | 日 |
| `cn.mkt.lpr_1y` / `lpr_5y` | 贷款市场报价利率 | 政策利率 | 月 |
| `cn.mkt.cgb_10y` | 中债 10Y 国债收益率 | 与美债利差决定人民币压力 | 日 |
| `cn.mkt.pmi_mfg` | 制造业 PMI | 需求端 | 月 |
| `cn.mkt.northbound_turnover` | 陆股通成交总额 | ⚠️ **只有成交额，无净流入**（2024-08-19 起停披露净额） | 日 |
| `cn.mkt.northbound_holding` | 陆股通持股市值 | 季度频率的外资仓位替代品 | 季 |

---

## 3. 免费数据源大表（2026-09-07 实测）

### 3.1 实测通过 ✅

| # | 数据源 | 端点 | 需密钥 | 频率 | 延迟 | 配额 | 覆盖指标 | 实测结果 |
|---|---|---|---|---|---|---|---|---|
| 1 | **FRED** | `api.stlouisfed.org/fred/series/observations` | ✅ 免费注册 | 日/周/月 | 1天 | 120 req/min | L1/L2/L3 绝大部分（WRESBAL, WTREGEN, RRPONTSYD, SOFR, IORB, BAMLH0A0HYM2, BAMLC0A0CM, T10Y2Y, T10Y3M, DFII10, VIXCLS, NFCI, T5YIFR, DEXJPUS, BAMLEMCBPIOAS, ECBASSETSW, JPNASSETS…） | 已在用 |
| 2 | **NY Fed Markets API** | `markets.newyorkfed.org/api/...` | ❌ 无需 | 日 | 当日 | 无公开限制 | SOFR 全分位、RRP/SRF 操作明细、SOMA 持仓 | `200`，SOFR=3.66%，RRP 接受量 $6.75亿 |
| 3 | **Treasury FiscalData** | `api.fiscaldata.treasury.gov/.../operating_cash_balance` | ❌ 无需 | **日** | T+1 | 无 | TGA 日频余额（FRED 只有周频） | `200`，2026-09-03 TGA=$944,364M |
| 4 | **OFR** | `financialresearch.gov/financial-stress-index/data/fsi.json` | ❌ 无需 | 日 | 当日 | 无 | 金融压力指数，2000 年至今 | `200`，3.3MB 完整历史 |
| 5 | **CFTC (Socrata)** | `publicreporting.cftc.gov/resource/6dca-aqww.json` | 可选 token | 周 | 周五 15:30 ET | 无 token 时限速 | 全品种 COT 持仓 | `200` |
| 6 | **DefiLlama** | `stablecoins.llama.fi/stablecoincharts/all` | ❌ 无需 | 日 | 当日 | 宽松 | 稳定币总供应及分币种 | `200`，1.2MB 全历史 |
| 7 | **TFTC ETF Flows** | `tftc.io/bitcoin-etf-flows/data.json` | ❌ 无需 | 日 | T+1 | 无 | **BTC 现货 ETF 逐日净流入 + 分基金明细** | `200`，CC BY 4.0，覆盖至 2026-09-04 |
| 8 | **OKX** | `okx.com/api/v5/public/{open-interest,funding-rate}` | ❌ 无需 | 实时/8h | 秒级 | 20 req/2s | 永续 OI、资金费率 | `200`，BTC-USDT-SWAP OI=$20.9亿 |
| 9 | **Deribit** | `deribit.com/api/v2/public/...` | ❌ 无需 | 实时 | 秒级 | 宽松 | DVOL 隐含波动率、期权 | `200` |
| 10 | **Alternative.me** | `api.alternative.me/fng/` | ❌ 无需 | 日 | 当日 | 无 | 加密恐惧贪婪指数 | `200`，当前 71 (Greed) |
| 11 | **CoinGecko** | `api.coingecko.com/api/v3/...` | 可选 Demo key | 日/实时 | 分钟 | **100/min，10k/月** | 币价、市值 | 已在用 |
| 12 | **Yahoo Finance** | `query1.finance.yahoo.com/v8/finance/chart/{sym}` | ❌ 无需 | 日/分钟 | 15分钟 | 非官方，需 UA | ETF 价格/成交量（SPY, RSP, GLD, TLT, HYG…） | `200`（**必须带 User-Agent**） |
| 13 | **ECB Data Portal** | `data-api.ecb.europa.eu/service/data/...` | ❌ 无需 | 周/月 | 数日 | 无 | ECB 资产负债表、欧元区 M3 | `200`，SDMX/CSV |
| 14 | **World Bank** | `api.worldbank.org/v2/...` | ❌ 无需 | 年/季 | 数月 | 无 | 长周期宏观对照 | `200` |
| 15 | **东方财富 datacenter** | `datacenter-web.eastmoney.com/api/data/v1/get` | ❌ 无需 | 日/月 | T+1 | 未公开，需自限速 | M0/M1/M2、CPI、PMI、存准率、两融明细 | `200`（`RPT_ECONOMY_CURRENCY_SUPPLY`、`RPT_ECONOMY_CPI`、`RPT_ECONOMY_PMI`、`RPT_ECONOMY_DEPOSIT_RESERVE`、`RPTA_WEB_RZRQ_GGMX` 均验证通过） |
| 16 | **mempool.space** | `mempool.space/api/v1/...` | ❌ 无需 | 实时 | 秒级 | 宽松 | 链上手续费、区块 | `200`，已在用 |
| 17 | **blockchain.com** | `api.blockchain.info/charts/...` | ❌ 无需 | 日 | 当日 | 宽松 | 交易笔数、活跃地址、算力 | `200`，已在用 |
| 18 | **Alpha Vantage** | `alphavantage.co/query` | ✅ 免费注册 | 日 | 当日 | **25 次/天** | XAUUSD 现货金 | 已在用，配额极紧 |

### 3.2 实测失败 / 不可用 ❌

| 数据源 | 目标用途 | 实测结果 | 替代方案 |
|---|---|---|---|
| **Binance Futures** | 永续 OI / 资金费率 | `HTTP 451` 地域限制 | ✅ 改用 **OKX** |
| **Bybit v5** | 永续 OI | `HTTP 403` CloudFront 国家封锁 | ✅ 改用 **OKX + Deribit** |
| **Farside Investors** | BTC/ETH ETF 资金流 | `HTTP 403` Cloudflare 挑战 | ✅ 改用 **TFTC 开放数据集**（CC BY 4.0，同源数据） |
| **国家统计局 easyquery** | 中国 M2/社融原始数据 | `HTTP 403` WAF 拦截 | ⚠️ 改用东方财富转发；社融接口名待定位 |
| **shibor.org** | Shibor 利率 | 连接超时（`http=000`） | ⚠️ 改用 chinamoney.com.cn 或东财，待验证 |
| **FINRA 融资余额** | 美股杠杆 | 无 API，仅网页 XLSX 下载 | ⚠️ 需 HTML 解析下载链接 + XLSX 解析，月频可接受 |
| **iShares AJAX CSV** | ETF 份额变动 | 返回 HTML 而非 CSV | ⚠️ 暂缓；BTC ETF 已由 TFTC 覆盖 |
| **MOVE 指数** | 债券波动率 | ICE 专有，无免费源 | 用 `^TYX`/`^TNX` 已实现波动率代理 |
| **交叉货币基差** | 离岸美元荒 | Bloomberg/Refinitiv 独占 | DXY + EM 利差 + USDJPY 三者共振代理 |
| **EPFR / IIF 跨境资金流** | 全球基金流向 | 纯商业订阅（$万级/年） | 无免费替代，放弃 |
| **陆股通日频净流入** | 外资 A 股流向 | **数据源本身已停止存在**（2024-08-19） | 季度持股市值变化 + 日频成交总额 |

### 3.3 待验证（第二轮）

| 数据源 | 目标 | 待确认 |
|---|---|---|
| 东方财富 社融/LPR/中债 | `cn.mkt.social_financing`、`lpr`、`cgb_10y` | 已排除 6 个 reportName（均返回 `9501 报表配置不存在`），需抓包 `data.eastmoney.com/cjsj/*` 页面定位 |
| chinamoney.com.cn | DR007 / 中债收益率曲线 | 接口形态与反爬强度 |
| CBOE 免费 CSV | Put/Call ratio、SKEW | 端点是否仍开放 |
| NY Fed ACM 期限溢价 | `us.mkt.term_premium` | CSV 下载地址稳定性 |
| SEC EDGAR full-text | 13F 机构持仓变化 | 季频、解析成本高，性价比待评估 |

---

## 4. 架构缺口（这是最重要的工程结论）

**本规划中价值最高的 8 个指标全部是派生指标**（净流动性、SOFR−IORB、SOFR 尾部、VIX 期限结构、M1−M2 剪刀差、RSP/SPY、全球央行总资产、稳定币 7 日净增），而**当前 core 只支持单指标检测**——`detector` 包下的 5 个检测器都只接受一条时间序列。

因此落地本规划的前置条件是新增两个能力：

### 4.1 派生指标引擎（derived metric engine）

在 core 增加一层：以已入库的 observations 为输入，按声明式表达式计算新指标并回写。

```yaml
# 示意：plugins/macro/derived.yaml
derived:
  - id: fed.ins.net_liquidity
    expr: "fed.ins.balance_sheet - fed.ins.tga - fed.ins.rrp"
    align: forward_fill      # 周频 WALCL 对齐日频 TGA
    unit: USD
  - id: us.mkt.sofr_iorb_spread
    expr: "(us.mkt.sofr - us.mkt.iorb) * 100"
    align: exact
    unit: bp
```

关键设计点：**频率对齐策略**。WALCL 周频、TGA 日频、RRP 日频，混合运算必须显式声明 `forward_fill` / `exact` / `resample`，否则派生值会静默错误。

### 4.2 新增检测器

| 检测器 | 用途 | 现状 |
|---|---|---|
| `zscore` | 相对自身历史分布的标准差偏离，比固定阈值更适合跨市场复用 | 缺失 |
| `divergence` | 两指标背离（如 HY 利差走阔但 VIX 不动） | 缺失 |
| `level_change` | 环比/同比变化幅度超阈值（准备金周降 >$500亿） | 缺失 |
| `cross` | 指标穿越另一指标（VIX3M/VIX 跌破 1） | 缺失 |

---

## 5. 落地顺序建议

分四批，每批都能独立产生价值，且按「投入产出比」排序。

### 第一批：美元净流动性（最高优先级）
只需**现有 FRED key** + 两个无密钥源，即可拿下 L1 全部与 L2 核心。

- 新增 FRED 系列：`WRESBAL`、`WTREGEN`、`RRPONTSYD`、`SOFR`、`IORB`、`BAMLH0A0HYM2`、`BAMLC0A0CM`、`T10Y3M`、`DFII10`、`VIXCLS`、`NFCI`
- 新增 NY Fed 插件：SOFR 分位、RRP/SRF 操作明细
- 新增 Treasury FiscalData：TGA 日频
- 建派生引擎，交付 `fed.ins.net_liquidity` 与 `us.mkt.sofr_iorb_spread`

> 单批投入最小、信息增量最大。做完这批，系统才第一次真正在"观测流动性"。

### 第二批：风险偏好与仓位
- OFR FSI（日频压力）
- CFTC COT 四品种持仓
- Yahoo 扩展：`^VIX3M`、`RSP`、`SPY`、`HYG`、`TLT` → VIX 期限结构、市场宽度
- 新增 `zscore` + `divergence` 检测器

### 第三批：加密边际资金
- DefiLlama 稳定币供应（+ 7日净增派生）
- TFTC BTC ETF 净流入（**替代已退役的合成 flow 指标**）
- OKX 资金费率 + OI
- Deribit DVOL

### 第四批：中国子体系
- 东方财富：M1/M2（→ 剪刀差派生）、CPI、PMI、存准率、两融余额
- 社融 / DR007 / LPR / 中债 10Y —— **需先完成 3.3 的接口定位**
- 明确放弃北向日频净流入，改用季度持股

---

## 6. 风险与注意事项

1. **东方财富接口是非公开接口**，无 SLA、无文档、字段可能无预警变更。必须：独立限速、字段缺失容错、探测失败不影响其他插件（沿用现有"快速失败"约定，但需隔离到插件级）。
2. **配额纪律**。Alpha Vantage 仅 25 次/天，CoinGecko 免费层 10k/月。新增采集器前必须核算总调用量，并沿用现有"回填不参与周期调度"的约定。
3. **告警噪音预算**。当前系统的校准目标是 ≤10 条/天。本规划新增 40+ 指标，若沿用现有阈值风格会瞬间击穿预算。**建议新指标一律先以 `severity: info` 且不推送的方式上线，观察两周分布后再定阈值**。
4. **派生指标的频率对齐**是最容易出静默 bug 的地方，需要专门的单元测试覆盖（周频×日频、月频×日频、缺失值）。
5. **许可与署名**。TFTC 数据集为 CC BY 4.0，**前端展示时必须标注来源**。FRED、NY Fed、Treasury、CFTC、OFR 为美国政府公开数据，无署名义务。
