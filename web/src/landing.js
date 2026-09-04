// ══════════════════════════════════════════════════════════════════════
// Sonde - Landing Page Interactive Script & i18n
// ══════════════════════════════════════════════════════════════════════

const LANDING_I18N = {
  zh: {
    'brand.title': 'Sonde',
    'nav.pillars': '核心支柱',
    'nav.pipeline': '架构链路',
    'nav.showcase': '功能矩阵',
    'nav.quickstart': '快速上手',
    'nav.launch': '进入工作台 ⚡',
    
    // Hero
    'hero.badge_loading': '正在探测系统运行状态...',
    'hero.badge_template': '系统运行中 · {plugins} 采集插件在线 · {metrics} 真实指标监控 · 今日告警: {today}/{limit}',
    'hero.badge_fallback': '系统就绪 · 4 采集插件 · 16 真实指标全监控 · 严格噪音预算 ≤10/天',
    'hero.title': '跨市场资本异动与宏观流动性观测雷达',
    'hero.subtitle': '专为高敏锐度投研与量化交易构建：100% 接入真实跨市场数据源，三维自适应检测器，严格噪音预算治理（≤10条/天），以及全自动关联研判组装。',
    'hero.cta_primary': '启动 Capital Radar 工作台 ⚡',
    'hero.cta_secondary': '查看部署与开源代码 🚀',
    
    // Ticker
    'ticker.btc_title': 'BTC 现货 & 算力',
    'ticker.btc_provider': 'CoinGecko / Mempool',
    'ticker.btc_sub': '活跃地址 & 链上交易活跃度',
    'ticker.gold_title': '现货黄金 XAU/USD',
    'ticker.gold_provider': 'Alpha Vantage',
    'ticker.gold_sub': '避险资本核心锚定资产',
    'ticker.yield_title': '美债 10Y 收益率',
    'ticker.yield_provider': 'FRED DGS10',
    'ticker.yield_sub': '全球无风险基准利率定价',
    'ticker.oil_title': 'WTI 原油现货',
    'ticker.oil_provider': 'FRED DCOILWTICO',
    'ticker.oil_sub': '大宗商品与通胀预期先导',
    
    // Pillars Section
    'pillars.tag': 'CORE PILLARS',
    'pillars.heading': '为什么选择 Sonde？',
    'pillars.desc': '拒绝充满玩具式 Mock 数据与告警风暴的传统监控，打造具备真实投资指导意义的异动观测基础设施。',
    
    'pillar1.title': '100% 真实跨市场数据源',
    'pillar1.desc': '系统全面淘汰合成 Mock 指标，真实接入包括 Yahoo Finance、FRED、CoinGecko、Alpha Vantage、Mempool 及 Blockchain.com 等权威机构源。',
    'pillar1.item1': '覆盖 ETF、宏观利率、加密货币与大宗商品',
    'pillar1.item2': '16 项关键核心指标，杜绝假基线',
    'pillar1.item3': '双重配额与频率感知自动调度保护',
    
    'pillar2.title': '抗疲劳噪音预算治理',
    'pillar2.desc': '拒绝动辄几百条无意义通知的告警轰炸。系统引入工程化「噪音预算」，全域真实告警严格校准至 ≤10 条/天。',
    'pillar2.item1': '阈值、历史分位数与异动趋势三大检测器',
    'pillar2.item2': '时序质量评分（QualityScore）前置把关',
    'pillar2.item3': 'Outbox 事务投递，直达 Telegram 与 Webhook',
    
    'pillar3.title': '关联聚类与研判组装',
    'pillar3.desc': '单点异动不足以决策。引擎自动将同时发生的跨资产异动聚类为宏观/加密事件，并调用研判装配器生成深度决策快照。',
    'pillar3.item1': '多维信号交叉验证与历史裁决回溯',
    'pillar3.item2': '高置信度事件快照卡片（Research Snapshot）',
    'pillar3.item3': '双向反馈机制，持续校准检测模型',
    
    // Pipeline Section
    'pipeline.tag': 'PIPELINE ARCHITECTURE',
    'pipeline.heading': '毫秒到日频的全链路闭环流水线',
    'pipeline.desc': '从数据源流式摄取、数据清洗校验、多算法并发规则检测，到事件聚类与前端可视化，清晰透明。',
    
    'step1.name': '1. Plugin 采集',
    'step1.sub': 'gRPC 双向流式',
    'step2.name': '2. 质量评估',
    'step2.sub': 'QualityScore 矩阵',
    'step3.name': '3. 规则检测',
    'step3.sub': '三类自适应探测器',
    'step4.name': '4. 告警治理',
    'step4.sub': '≤10条/天 预算 Outbox',
    'step5.name': '5. 研判组装',
    'step5.sub': '关联聚类与事件快照',
    'step6.name': '6. Capital Radar',
    'step6.sub': '多维工作台与通知',
    
    // Showcase Section
    'showcase.tag': 'SYSTEM SHOWCASE',
    'showcase.heading': '专为资本市场操作员设计的工作台',
    'showcase.desc': '开箱即用的专业面板，直观洞悉异常脉搏、流动性压力与策略信号。',
    
    'showcase1.title': '实时告警流与多维穿透',
    'showcase1.desc': '直观展示 Critical / Warning 等级的异常事件。包含基线对比、偏离度计算与即时 ECharts 走势图回溯。',
    'showcase1.link': '在工作台中查看 Alerts →',
    
    'showcase2.title': '宏观与加密关联事件聚类',
    'showcase2.desc': '聚合多源异常形成事件上下文，如「美债收益率攀升 + 黄金异动 + 加密链上活跃度突破」，避免碎片化信息。',
    'showcase2.link': '在工作台中查看 Clusters →',
    
    'showcase3.title': '全域指标信号质量与新鲜度矩阵',
    'showcase3.desc': '实时监测全部 16 项指标的延迟、健康度和缺失率。服务端动态判级，保证投研依据的真实与鲜活。',
    'showcase3.link': '在工作台中查看 Signal Quality →',
    
    'showcase4.title': '规则热更新与历史时间窗口回填',
    'showcase4.desc': '无需重启服务即可在线调整检测阈值；具备 7天/30天/90天 真实历史数据安全回填能力，零污染验证新策略。',
    'showcase4.link': '在工作台中查看 Rules →',
    
    // Quickstart
    'quickstart.tag': 'QUICKSTART',
    'quickstart.heading': '极客友好的极简部署',
    'quickstart.desc': '单 Go 二进制与 TimescaleDB 配合，无复杂的 npm 前端构建，两分钟启动属于你自己的资本雷达。',
    'quickstart.copy_btn': '复制全部命令',
    'quickstart.copied': '已复制到剪贴板！',
    
    'spec.runtime': '运行环境',
    'spec.db': '时序数据库',
    'spec.plugins': '内置采集插件',
    'spec.ui': '前端技术栈',
    'spec.runtime_val': 'Go 1.24+ (单二进制)',
    'spec.db_val': 'PostgreSQL / TimescaleDB',
    'spec.plugins_val': 'ETF / Macro / Crypto / Commodities',
    'spec.ui_val': 'Vanilla HTML5 / CSS3 / ES6',
    
    // Footer
    'footer.desc': '开源、自托管的跨市场资本异动与宏观流动性观测系统。',
    'footer.col1_title': '系统导航',
    'footer.col2_title': '核心文档',
    'footer.col3_title': '开源生态',
    'footer.link_alerts': '告警控制台',
    'footer.link_clusters': '事件聚类',
    'footer.link_research': '研判报告',
    'footer.link_doc_arch': '中文系统架构图',
    'footer.link_doc_prd': 'PRD 产品规格文档',
    'footer.link_doc_adr': 'ADR 架构决策记录',
    'footer.copyright': '© 2026 Sonde. Open-source Capital Intelligence System.'
  },
  en: {
    'brand.title': 'Sonde',
    'nav.pillars': 'Core Pillars',
    'nav.pipeline': 'Pipeline',
    'nav.showcase': 'Showcase',
    'nav.quickstart': 'Quickstart',
    'nav.launch': 'Launch Radar ⚡',
    
    // Hero
    'hero.badge_loading': 'Probing system live telemetry...',
    'hero.badge_template': 'System Online · {plugins} Active Plugins · {metrics} Real Indicators · Alerts Today: {today}/{limit}',
    'hero.badge_fallback': 'System Ready · 4 Ingestion Plugins · 16 Real Indicators · Strict Budget ≤10/Day',
    'hero.title': 'Macro & Multi-Asset Anomaly Radar for Serious Capital',
    'hero.subtitle': 'Engineered for macro analysts and quant traders: 100% authentic cross-market data, multi-detector engine, strict noise budget (≤10 alerts/day), and autonomous research synthesis.',
    'hero.cta_primary': 'Launch Capital Radar ⚡',
    'hero.cta_secondary': 'Explore Docs & GitHub 🚀',
    
    // Ticker
    'ticker.btc_title': 'BTC Spot & Hashrate',
    'ticker.btc_provider': 'CoinGecko / Mempool',
    'ticker.btc_sub': 'Active Addresses & On-chain Txs',
    'ticker.gold_title': 'Spot Gold XAU/USD',
    'ticker.gold_provider': 'Alpha Vantage',
    'ticker.gold_sub': 'Safe-haven Capital Anchor',
    'ticker.yield_title': 'US 10Y Treasury Yield',
    'ticker.yield_provider': 'FRED DGS10',
    'ticker.yield_sub': 'Global Risk-free Pricing Anchor',
    'ticker.oil_title': 'WTI Crude Oil',
    'ticker.oil_provider': 'FRED DCOILWTICO',
    'ticker.oil_sub': 'Commodities & Inflation Proxy',
    
    // Pillars Section
    'pillars.tag': 'CORE PILLARS',
    'pillars.heading': 'Why Sonde?',
    'pillars.desc': 'Say goodbye to toy synthetic mock data and devastating alert storms. An enterprise-grade anomaly detection infrastructure built for real capital.',
    
    'pillar1.title': '100% Authentic Market Data',
    'pillar1.desc': 'Zero synthetic mock data in production. Native integration with Yahoo Finance, FRED, CoinGecko, Alpha Vantage, Mempool, and Blockchain.com.',
    'pillar1.item1': 'Multi-asset: ETF, Macro, Crypto, Commodities',
    'pillar1.item2': '16 genuine indicators, eliminating fake baselines',
    'pillar1.item3': 'Quota-aware and frequency-aware scheduling',
    
    'pillar2.title': 'Noise Budget Engineering',
    'pillar2.desc': 'Never get drowned by thousands of useless notifications. We enforce an institutional "Noise Budget" calibrating real alerts to ≤10/day.',
    'pillar2.item1': 'Threshold, Percentile, and Trend detectors',
    'pillar2.item2': 'QualityScore gating and coverage matrix',
    'pillar2.item3': 'Transactional Outbox push to Telegram & Webhook',
    
    'pillar3.title': 'Cluster & Research Synthesis',
    'pillar3.desc': 'Isolated alerts lack context. Our engine automatically groups concurrent cross-asset anomalies into macro/crypto events with structured research verdicts.',
    'pillar3.item1': 'Cross-signal validation & historical verdicts',
    'pillar3.item2': 'High-confidence Research Snapshot cards',
    'pillar3.item3': 'Operator feedback loop for continuous calibration',
    
    // Pipeline Section
    'pipeline.tag': 'PIPELINE ARCHITECTURE',
    'pipeline.heading': 'End-to-End Closed-Loop Pipeline',
    'pipeline.desc': 'From streaming ingestion, quality scoring, concurrent rule evaluation, to clustering synthesis and radar visualization.',
    
    'step1.name': '1. Plugin Ingest',
    'step1.sub': 'Bidirectional gRPC Stream',
    'step2.name': '2. Quality Gate',
    'step2.sub': 'QualityScore Matrix',
    'step3.name': '3. Rule Engine',
    'step3.sub': 'Triple Adaptive Detectors',
    'step4.name': '4. Noise Budget',
    'step4.sub': '≤10 Alerts/Day Outbox',
    'step5.name': '5. Research Assembly',
    'step5.sub': 'Clusters & Snapshots',
    'step6.name': '6. Capital Radar',
    'step6.sub': 'Workbench & Notifications',
    
    // Showcase Section
    'showcase.tag': 'SYSTEM SHOWCASE',
    'showcase.heading': 'Built for Capital Market Operators',
    'showcase.desc': 'An out-of-the-box professional console to track anomaly pulses, liquidity stress, and cross-market signals.',
    
    'showcase1.title': 'Live Alert Stream & Deep Dive',
    'showcase1.desc': 'Real-time feed for Critical/Warning anomalies with baseline comparisons, deviation stats, and interactive ECharts trend inspection.',
    'showcase1.link': 'View Alerts Console →',
    
    'showcase2.title': 'Macro & Crypto Correlation Clusters',
    'showcase2.desc': 'Synthesizes co-occurring anomalies into situational intelligence, such as "Treasury spike + Gold breakout + Bitcoin mempool surge".',
    'showcase2.link': 'View Event Clusters →',
    
    'showcase3.title': 'Signal Quality & Freshness Matrix',
    'showcase3.desc': 'Monitors telemetry latency, health grades, and coverage across all 16 metrics. Server-computed freshness ensures absolute authenticity.',
    'showcase3.link': 'View Signal Quality →',
    
    'showcase4.title': 'Hot Rules & Historical Backfills',
    'showcase4.desc': 'Tune detector thresholds on the fly without restarts. Safe 7d/30d/90d authentic history backfill for backtesting new rules.',
    'showcase4.link': 'View Rule Management →',
    
    // Quickstart
    'quickstart.tag': 'QUICKSTART',
    'quickstart.heading': 'Geek-Friendly Minimal Deployment',
    'quickstart.desc': 'Single Go binary with TimescaleDB. No complex frontend npm build pipelines. Launch your own Sonde in 2 minutes.',
    'quickstart.copy_btn': 'Copy All Commands',
    'quickstart.copied': 'Copied to clipboard!',
    
    'spec.runtime': 'Runtime',
    'spec.db': 'Time-Series DB',
    'spec.plugins': 'Built-in Plugins',
    'spec.ui': 'Frontend Stack',
    'spec.runtime_val': 'Go 1.24+ (Single Binary)',
    'spec.db_val': 'PostgreSQL / TimescaleDB',
    'spec.plugins_val': 'ETF / Macro / Crypto / Commodities',
    'spec.ui_val': 'Vanilla HTML5 / CSS3 / ES6',
    
    // Footer
    'footer.desc': 'Self-hosted, open-source anomaly detection and macro liquidity radar.',
    'footer.col1_title': 'Navigation',
    'footer.col2_title': 'Documentation',
    'footer.col3_title': 'Ecosystem',
    'footer.link_alerts': 'Alerts Feed',
    'footer.link_clusters': 'Event Clusters',
    'footer.link_research': 'Research Reports',
    'footer.link_doc_arch': 'Architecture (Mermaid)',
    'footer.link_doc_prd': 'PRD Specification',
    'footer.link_doc_adr': 'Architecture Decisions (ADR)',
    'footer.copyright': '© 2026 Sonde. Open-source Capital Intelligence System.'
  }
};

// ── Pipeline Detailed Technical Explanations ────────────────────────
const PIPELINE_DETAILS = {
  zh: [
    { title: '1. Plugin gRPC 流式采集', text: '4个独立插件（ETF/Macro/Crypto/Commodities）通过长连接双向 gRPC 流向 Core 汇报 MetricSnapshot。支持断线重连与速率保护。', tag: 'gRPC Protocol' },
    { title: '2. 质量评估与覆盖矩阵', text: 'Core 收到快照后执行 QualityScore 校验，记录覆盖矩阵（Coverage Matrix），评估数据延迟与字段完备度，防止脏数据入库。', tag: 'TimescaleDB M2' },
    { title: '3. 三大频率感知规则检测器', text: 'Threshold（绝对阈值）、Percentile（90天历史分位数极值）、Trend（动量趋势突破），感知不同指标的发布频率（日/周/月）。', tag: 'Detection Engine' },
    { title: '4. 告警治理与噪音预算', text: '告警引擎自动去重并执行自动 resolve。Outbox 事务队列结合每日 ≤10 条的真实噪音预算，超限即报警，可靠推送 Telegram/Webhook。', tag: 'Noise Budget' },
    { title: '5. 投研组装与事件研判', text: '基于关联关系本体（Ontology）将孤立告警自动聚类为宏观事件，生成结构化 Research Snapshot 报告与历史置信度裁决。', tag: 'Research Assembly' },
    { title: '6. Capital Radar 决策终端', text: '高性能原生 Web 仪表盘 + 移动端自适应，配合命令通路（Command Log）支持实时的按需同步与历史回填。', tag: 'Operator Console' }
  ],
  en: [
    { title: '1. Plugin gRPC Ingest', text: '4 independent plugins stream MetricSnapshots to Core via gRPC streams. Features fail-fast on missing keys and rate-limit guardrails.', tag: 'gRPC Protocol' },
    { title: '2. Quality Gate & Coverage Matrix', text: 'Core grades every snapshot with QualityScore, writes coverage matrices, and blocks dirty observations from poisoning detectors.', tag: 'TimescaleDB M2' },
    { title: '3. Triple Adaptive Detectors', text: 'Threshold (absolute bounds), Percentile (rolling 90d extremes), and Trend (momentum breakouts) with frequency-aware lookbacks.', tag: 'Detection Engine' },
    { title: '4. Alert Engine & Noise Budget', text: 'Outbox queue with automatic deduplication, auto-resolution, and a strict ≤10/day noise budget delivering to Telegram & Webhooks.', tag: 'Noise Budget' },
    { title: '5. Research Assembly & Verdicts', text: 'Correlates concurrent signals into macro/crypto event clusters, assembling structured research cards and recording operator feedback.', tag: 'Research Assembly' },
    { title: '6. Capital Radar Console', text: 'High-performance vanilla web interface with command dispatcher for instant manual sync and historical backfills.', tag: 'Operator Console' }
  ]
};

let currentLang = 'zh';
let cachedStatusData = null;

// ── Language Toggle & Translation ────────────────────────────────────
function initLanguage() {
  const saved = localStorage.getItem('sonde_lang');
  if (saved && (saved === 'zh' || saved === 'en')) {
    currentLang = saved;
  } else {
    const navLang = navigator.language || '';
    currentLang = navLang.toLowerCase().startsWith('zh') ? 'zh' : 'en';
  }
  setLanguage(currentLang);
  
  const btnZh = document.getElementById('btn-lang-zh');
  const btnEn = document.getElementById('btn-lang-en');
  if (btnZh) btnZh.addEventListener('click', () => setLanguage('zh'));
  if (btnEn) btnEn.addEventListener('click', () => setLanguage('en'));
}

function setLanguage(lang) {
  currentLang = lang;
  localStorage.setItem('sonde_lang', lang);
  
  // Toggle button styles
  const btnZh = document.getElementById('btn-lang-zh');
  const btnEn = document.getElementById('btn-lang-en');
  if (btnZh && btnEn) {
    btnZh.classList.toggle('active', lang === 'zh');
    btnEn.classList.toggle('active', lang === 'en');
  }
  
  // Translate static text nodes with data-i18n
  const dict = LANDING_I18N[lang] || LANDING_I18N.zh;
  document.querySelectorAll('[data-i18n]').forEach(el => {
    const key = el.getAttribute('data-i18n');
    if (dict[key]) {
      el.textContent = dict[key];
    }
  });
  
  // Update live status capsule text
  renderStatusCapsule(cachedStatusData);
  
  // Update active pipeline step detail
  updatePipelineDetail(activePipelineIndex);
}

// ── Real-time Status Capsule ─────────────────────────────────────────
async function fetchLiveStatus() {
  const badgeText = document.getElementById('pulse-badge-text');
  try {
    const res = await fetch('/api/status', { cache: 'no-store' });
    if (!res.ok) throw new Error('status HTTP ' + res.status);
    const data = await res.json();
    cachedStatusData = data;
    renderStatusCapsule(data);
  } catch (err) {
    console.log('[Landing] Running in fallback demo mode:', err.message);
    renderStatusCapsule(null);
  }
}

function renderStatusCapsule(data) {
  const badgeText = document.getElementById('pulse-badge-text');
  if (!badgeText) return;
  const dict = LANDING_I18N[currentLang] || LANDING_I18N.zh;
  
  if (!data) {
    badgeText.textContent = dict['hero.badge_fallback'];
    return;
  }
  
  const plugins = data.plugins || [];
  const healthyCount = plugins.filter(p => p.healthy || p.status === 'healthy').length;
  const metricsCount = (data.metrics && data.metrics.length) ? data.metrics.length : 16;
  const budget = data.budget || {};
  const todayAlerts = budget.real_today !== undefined ? budget.real_today : 0;
  const limitAlerts = budget.real_limit !== undefined ? budget.real_limit : 10;
  
  let tpl = dict['hero.badge_template'];
  tpl = tpl.replace('{plugins}', healthyCount || plugins.length || 4)
           .replace('{metrics}', metricsCount)
           .replace('{today}', todayAlerts)
           .replace('{limit}', limitAlerts);
  badgeText.textContent = tpl;
}

// ── Pipeline Interactive Details ─────────────────────────────────────
let activePipelineIndex = 0;

function initPipeline() {
  const steps = document.querySelectorAll('.pipeline-step');
  steps.forEach((step, idx) => {
    step.addEventListener('click', () => {
      steps.forEach(s => s.classList.remove('active'));
      step.classList.add('active');
      activePipelineIndex = idx;
      updatePipelineDetail(idx);
    });
  });
  updatePipelineDetail(0);
}

function updatePipelineDetail(idx) {
  const details = (PIPELINE_DETAILS[currentLang] || PIPELINE_DETAILS.zh)[idx];
  if (!details) return;
  
  const titleEl = document.getElementById('pipeline-detail-title');
  const textEl = document.getElementById('pipeline-detail-text');
  const tagEl = document.getElementById('pipeline-detail-tag');
  
  if (titleEl) titleEl.textContent = details.title;
  if (textEl) textEl.textContent = details.text;
  if (tagEl) tagEl.textContent = details.tag;
}

// ── Copy Command Snippet ─────────────────────────────────────────────
function initCopyButtons() {
  const btn = document.getElementById('btn-copy-quickstart');
  if (!btn) return;
  
  btn.addEventListener('click', () => {
    const code = `git clone https://github.com/zll0825/sonde.git
cd sonde
export FRED_API_KEY=your_free_fred_key
export ALPHAVANTAGE_API_KEY=your_alpha_key
make dev-up`;
    navigator.clipboard.writeText(code).then(() => {
      const dict = LANDING_I18N[currentLang] || LANDING_I18N.zh;
      btn.textContent = dict['quickstart.copied'] || 'Copied!';
      btn.style.color = '#2dd4a7';
      setTimeout(() => {
        btn.textContent = dict['quickstart.copy_btn'] || 'Copy';
        btn.style.color = '';
      }, 2000);
    });
  });
}

// ── Document Ready ───────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  initLanguage();
  initPipeline();
  initCopyButtons();
  fetchLiveStatus();
  
  // Refresh live status every 30s
  setInterval(fetchLiveStatus, 30000);
});
