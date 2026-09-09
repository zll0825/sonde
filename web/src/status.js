// ── Top-Right Status Bar Ticker: Auto-flips every 5s ──────────────────
let tickerIndex = 0;
let tickerTimer = null;
let isTickerPaused = false;

function initStatusTicker() {
    const wrapper = document.getElementById('status-ticker-wrapper');
    if (!wrapper) return;

    // Hover pauses auto-flip so user can read/click comfortably
    wrapper.addEventListener('mouseenter', () => { isTickerPaused = true; });
    wrapper.addEventListener('mouseleave', () => { isTickerPaused = false; });

    // Click cycles immediately to next slide
    wrapper.addEventListener('click', () => {
        flipTickerSlide();
        resetTickerTimer();
    });

    // Directly clickable indicator dots
    const dots = wrapper.querySelectorAll('.ticker-dot');
    dots.forEach(dot => {
        dot.addEventListener('click', (e) => {
            e.stopPropagation();
            const targetIdx = parseInt(dot.dataset.dot, 10);
            if (!isNaN(targetIdx) && targetIdx !== tickerIndex) {
                goToTickerSlide(targetIdx);
                resetTickerTimer();
            }
        });
    });

    startTickerTimer();
}

function resetTickerTimer() {
    if (tickerTimer) clearInterval(tickerTimer);
    startTickerTimer();
}

function startTickerTimer() {
    if (tickerTimer) clearInterval(tickerTimer);
    tickerTimer = setInterval(() => {
        if (!isTickerPaused) {
            flipTickerSlide();
        }
    }, 5000);
}

function flipTickerSlide() {
    const ticker = document.getElementById('status-ticker');
    const slides = ticker ? ticker.querySelectorAll('.ticker-slide') : [];
    if (!slides.length) return;
    const nextIdx = (tickerIndex + 1) % slides.length;
    goToTickerSlide(nextIdx);
}

function goToTickerSlide(nextIdx) {
    const ticker = document.getElementById('status-ticker');
    const slides = ticker ? ticker.querySelectorAll('.ticker-slide') : [];
    const wrapper = document.getElementById('status-ticker-wrapper');
    const dots = wrapper ? wrapper.querySelectorAll('.ticker-dot') : [];
    if (!slides.length || nextIdx === tickerIndex) return;

    const prevIdx = tickerIndex;
    tickerIndex = nextIdx;

    const prevSlide = slides[prevIdx];
    const nextSlide = slides[nextIdx];

    if (prevSlide) {
        prevSlide.classList.remove('active');
        prevSlide.classList.add('slide-up');
        setTimeout(() => {
            prevSlide.classList.remove('slide-up');
        }, 500);
    }

    if (nextSlide) {
        nextSlide.classList.remove('slide-up', 'slide-down');
        nextSlide.classList.add('active');
    }

    dots.forEach((dot, i) => {
        dot.classList.toggle('active', i === tickerIndex);
    });
}

// ── R1/R2: /api/status aggregate ──────────────────────────────────────
async function fetchStatus() {
    try {
        const resp = await fetch(API_BASE + '/api/status');
        if (!resp.ok) throw new Error('status ' + resp.status);
        statusData = await resp.json();
        renderStatus(statusData);
        // Alerts list looks up metric names from statusData. loadAlerts() and
        // fetchStatus() race on first paint; if alerts won, the row still
        // shows the raw metric_id until we re-render here.
        if (Array.isArray(alertsList)) renderAlerts(alertsList);
        hasFetched = true;
        isOnline = true;
        document.getElementById('status-pill').textContent = t('status.online');
    } catch (err) {
        hasFetched = true;
        isOnline = false;
        renderStatusBarError();
    }
}

function renderStatus(data) {
    const tickerWrap = document.getElementById('status-ticker-wrapper');
    if (tickerWrap) tickerWrap.classList.remove('offline');

    // 1. 指标 (Metrics)
    const metricsCount = (data.metrics || []).length;
    const metricsEl = document.getElementById('sb-metrics');
    if (metricsEl) {
        metricsEl.textContent = t('status.metrics_count', { n: metricsCount });
    }

    // 2. 告警数量 (Alert budget)
    const budget = data.budget || {};
    const today = Number.isFinite(budget.real_today) ? budget.real_today : (budget.today || 0);
    const limit = Number.isFinite(budget.real_limit) ? budget.real_limit : (budget.limit || 10);
    const hasDetailedBudget = Number.isFinite(budget.real_today);
    const budgetEl = document.getElementById('sb-budget');
    if (budgetEl) {
        budgetEl.textContent = hasDetailedBudget
            ? t('status.budget_detailed', {
                today: today,
                limit: limit,
                mock: budget.mock_today || 0,
                test: budget.test_today || 0,
                unknown: budget.unknown_today || 0
            })
            : t('status.budget_simple', { today: today, limit: limit });
        budgetEl.classList.toggle('warn', today >= 8 && today <= limit);
        budgetEl.classList.toggle('danger', budget.real_over_budget === true || today > limit);
    }

    // 3. 最新数据 (Latest data)
    const rel = relativeTime(data.latest_data_at);
    const latestEl = document.getElementById('sb-latest');
    if (latestEl) {
        latestEl.textContent = rel ? t('status.latest_data', { time: rel }) : t('status.no_data');
    }

    // Comprehensive tooltip on top-right ticker
    if (tickerWrap) {
        const mTxt = '📊 ' + t('status.ticker_metrics_title') + ': ' + t('status.metrics_count', { n: metricsCount });
        const bTxt = '🔔 ' + t('status.ticker_budget_title') + ': ' + (budgetEl ? budgetEl.textContent : '—');
        const lTxt = '⏱️ ' + t('status.ticker_latest_title') + ': ' + (rel ? t('status.latest_data', { time: rel }) : t('status.no_data'));
        tickerWrap.title = mTxt + '\n' + bTxt + '\n' + lTxt + '\n\n' + t('status.ticker_tooltip');
    }

    renderPluginsTab(data);
    renderPulseGrid(data.metrics || []);
}

function renderStatusBarError() {
    const tickerWrap = document.getElementById('status-ticker-wrapper');
    if (tickerWrap) tickerWrap.classList.add('offline');

    for (const id of ['sb-metrics', 'sb-budget', 'sb-latest']) {
        const el = document.getElementById(id);
        if (el) el.textContent = t('status.interrupted');
    }

    renderPluginsTabError();
}

function pluginHealthLabel(plugin) {
    if (!plugin.connected) return t('status.plugin_offline');
    return plugin.healthy ? t('status.plugin_healthy') : t('status.plugin_unhealthy');
}

function renderPluginsTab(data) {
    const list = document.getElementById('plugins-list');
    const summary = document.getElementById('plugins-summary');
    if (!list) return;

    data = data || statusData || {};
    const plugins = data.plugins || [];
    const expected = data.expected_plugins || [];
    const views = mergePluginViews(data);

    if (summary) {
        const connected = plugins.filter(p => p.connected).length;
        const degraded = plugins.filter(p => p.connected && !p.healthy).length;
        const notReady = expected.filter(p => p.ready === false).length;
        summary.textContent = t('plugins.summary', {
            total: views.length,
            connected: connected,
            degraded: degraded,
            notReady: notReady
        });
    }

    if (!views.length) {
        list.textContent = '';
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = t('plugins.empty');
        list.appendChild(empty);
        return;
    }

    list.textContent = '';
    for (const view of views) {
        list.appendChild(buildPluginCard(view));
    }
}

function renderPluginsTabError() {
    const list = document.getElementById('plugins-list');
    const summary = document.getElementById('plugins-summary');
    if (summary) summary.textContent = t('status.interrupted');
    if (list && !list.children.length) {
        list.textContent = '';
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = t('status.interrupted');
        list.appendChild(empty);
    }
}

function buildPluginCard(view) {
    const live = view.live || {};
    const exp = view.expected || {};
    const pluginID = live.id || exp.id || view.id;
    const nameStr = live.name || exp.name || pluginID;
    const ready = exp.ready !== false;

    const card = document.createElement('div');
    card.className = 'plugin-card';

    // Header: title + status badge
    const header = document.createElement('div');
    header.className = 'plugin-card-header';

    const titleBox = document.createElement('div');
    titleBox.className = 'plugin-card-title';

    const nameEl = document.createElement('span');
    nameEl.className = 'plugin-name';
    nameEl.textContent = nameStr;
    titleBox.appendChild(nameEl);

    const idEl = document.createElement('span');
    idEl.className = 'plugin-id-sub';
    idEl.textContent = pluginID;
    titleBox.appendChild(idEl);
    header.appendChild(titleBox);

    const badge = document.createElement('span');
    badge.className = 'plugin-status-badge';
    if (!ready) {
        badge.classList.add('not-ready');
        badge.textContent = '● ' + t('status.plugin_not_ready');
    } else if (!live.connected) {
        badge.classList.add('offline');
        badge.textContent = '● ' + t('status.plugin_offline');
    } else if (!live.healthy) {
        badge.classList.add('degraded');
        badge.textContent = '● ' + t('status.plugin_unhealthy');
    } else {
        badge.classList.add('healthy');
        badge.textContent = '● ' + t('status.plugin_healthy');
    }
    header.appendChild(badge);
    card.appendChild(header);

    // Meta grid: last collect, count, duration, consecutive errors
    const metaGrid = document.createElement('div');
    metaGrid.className = 'plugin-meta-grid';

    // 1. Last collect
    const colLast = document.createElement('div');
    colLast.className = 'plugin-meta-item';
    const lblLast = document.createElement('span');
    lblLast.className = 'plugin-meta-label';
    lblLast.textContent = t('plugins.last_collect');
    const valLast = document.createElement('span');
    valLast.className = 'plugin-meta-val';
    valLast.textContent = live.last_collect_at ? relativeTime(live.last_collect_at) : '—';
    colLast.appendChild(lblLast);
    colLast.appendChild(valLast);
    metaGrid.appendChild(colLast);

    // 2. Count
    const colCount = document.createElement('div');
    colCount.className = 'plugin-meta-item';
    const lblCount = document.createElement('span');
    lblCount.className = 'plugin-meta-label';
    lblCount.textContent = t('plugins.collect_count');
    const valCount = document.createElement('span');
    valCount.className = 'plugin-meta-val';
    valCount.textContent = t('plugins.val_count', { n: live.last_collect_count != null ? live.last_collect_count : 0 });
    colCount.appendChild(lblCount);
    colCount.appendChild(valCount);
    metaGrid.appendChild(colCount);

    // 3. Duration
    const colDur = document.createElement('div');
    colDur.className = 'plugin-meta-item';
    const lblDur = document.createElement('span');
    lblDur.className = 'plugin-meta-label';
    lblDur.textContent = t('plugins.duration');
    const valDur = document.createElement('span');
    valDur.className = 'plugin-meta-val';
    valDur.textContent = t('plugins.val_duration', { n: live.last_collect_duration_ms != null ? live.last_collect_duration_ms : 0 });
    colDur.appendChild(lblDur);
    colDur.appendChild(valDur);
    metaGrid.appendChild(colDur);

    // 4. Consecutive errors
    const colErr = document.createElement('div');
    colErr.className = 'plugin-meta-item';
    const lblErr = document.createElement('span');
    lblErr.className = 'plugin-meta-label';
    lblErr.textContent = t('plugins.consecutive_errors');
    const valErr = document.createElement('span');
    valErr.className = 'plugin-meta-val';
    valErr.textContent = t('plugins.val_errors', { n: live.consecutive_errors || 0 });
    if (live.consecutive_errors > 0) valErr.style.color = '#e85a5a';
    colErr.appendChild(lblErr);
    colErr.appendChild(valErr);
    metaGrid.appendChild(colErr);

    card.appendChild(metaGrid);

    // Missing secrets warning
    if (!ready) {
        const missing = Object.keys(exp.secrets_present || {}).filter(k => !exp.secrets_present[k]);
        const warnBox = document.createElement('div');
        warnBox.className = 'plugin-alert-box warn';
        warnBox.textContent = t('status.missing_secrets', { keys: missing.join(', ') || '—' });
        card.appendChild(warnBox);
    }

    // Error alert box
    if (live.last_collect_error) {
        const errorBox = document.createElement('div');
        errorBox.className = 'plugin-alert-box error';
        errorBox.textContent = t('status.plugin_error', {
            err: live.last_collect_error,
            count: live.consecutive_errors || 0,
            duration: live.last_collect_duration_ms || 0
        });
        card.appendChild(errorBox);
    }

    // Operational actions
    const actions = document.createElement('div');
    actions.className = 'plugin-card-actions';

    const syncBtn = document.createElement('button');
    syncBtn.type = 'button';
    syncBtn.className = 'btn-plugin-action';
    syncBtn.textContent = '⚡ ' + t('status.btn_sync');
    syncBtn.disabled = !live.connected;
    syncBtn.title = live.connected ? '' : t('status.sync_need_online');
    syncBtn.addEventListener('click', () => postControl('sync', pluginID, syncBtn));
    actions.appendChild(syncBtn);

    const backfillBtn = document.createElement('button');
    backfillBtn.type = 'button';
    backfillBtn.className = 'btn-plugin-action';
    const canBackfill = !!(exp.capabilities && exp.capabilities.windowed_backfill);
    backfillBtn.textContent = '⏱️ ' + t('status.btn_backfill');
    backfillBtn.disabled = !canBackfill;
    backfillBtn.title = canBackfill ? '' : t('status.backfill_disabled');
    backfillBtn.addEventListener('click', () => openBackfillMenu(pluginID, actions, backfillBtn));
    actions.appendChild(backfillBtn);

    card.appendChild(actions);
    return card;
}

function openBackfillMenu(pluginID, parentEl, backfillBtn) {
    let existing = parentEl.querySelector('.backfill-menu');
    if (existing) {
        existing.remove();
        return;
    }
    const menu = document.createElement('span');
    menu.className = 'backfill-menu';

    [
        { label: t('status.window_7d'), days: 7 },
        { label: t('status.window_30d'), days: 30 },
        { label: t('status.window_90d'), days: 90 },
    ].forEach(opt => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'backfill-option';
        btn.textContent = opt.label;
        btn.addEventListener('click', () => {
            menu.remove();
            postControl('backfill', pluginID, backfillBtn, { days: opt.days });
        });
        menu.appendChild(btn);
    });

    const btnCancel = document.createElement('button');
    btnCancel.type = 'button';
    btnCancel.className = 'backfill-option btn-cancel';
    btnCancel.textContent = t('status.btn_cancel');
    btnCancel.addEventListener('click', () => menu.remove());
    menu.appendChild(btnCancel);

    parentEl.appendChild(menu);
}

function mergePluginViews(data) {
    const map = {};
    (data.plugins || []).forEach(p => {
        map[p.id || p.name] = { id: p.id, live: p };
    });
    (data.expected_plugins || []).forEach(e => {
        const key = e.id || e.name;
        if (!map[key]) map[key] = { id: key };
        map[key].expected = e;
        if (!map[key].live) map[key].id = e.id;
    });
    return Object.keys(map).map(k => map[k]);
}

async function postControl(kind, pluginID, btn, options = {}) {
    if (!pluginID) return;
    const body = { plugin_id: pluginID };
    if (kind === 'backfill') {
        const days = options.days || 30;
        const end = Math.floor(Date.now() / 1000);
        body.window_start = end - days * 24 * 3600;
        body.window_end = end;
    }
    btn.disabled = true;
    try {
        const resp = await fetch(API_BASE + '/api/control/' + kind, {
            method: 'POST',
            headers: mutationHeaders(),
            body: JSON.stringify(body)
        });
        if (!resp.ok) {
            if (resp.status === 401 || resp.status === 403) {
                showToast(t('token.need_token_toast'), 'error');
                const btnToken = document.getElementById('btn-token-status');
                if (btnToken) btnToken.click();
                return;
            }
            const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
            throw new Error(err.error || ('HTTP ' + resp.status));
        }
        showToast(kind === 'backfill' ? t('status.backfill_ok') : t('status.sync_ok'), 'success');
    } catch (err) {
        showToast(t('common.error', { err: err.message }), 'error');
    } finally {
        if (kind === 'backfill') {
            const exp = ((statusData && statusData.expected_plugins) || []).find(e => e.id === pluginID || e.name === pluginID) || {};
            btn.disabled = !(exp.capabilities && exp.capabilities.windowed_backfill);
        } else {
            const live = ((statusData && statusData.plugins) || []).find(p => p.id === pluginID || p.name === pluginID) || {};
            btn.disabled = !live.connected;
        }
    }
}

// ── R2: Metric Pulse cards ───────────────────────────────────────────
function renderPulseGrid(metrics) {
    const grid = document.getElementById('pulse-grid');
    grid.textContent = '';
    for (const m of metrics) grid.appendChild(buildPulseCard(m));
}

function buildPulseCard(m) {
    const card = document.createElement('div');
    card.className = 'pulse-card';

    const head = document.createElement('div');
    head.className = 'pulse-head';
    const dot = document.createElement('span');
    const freshness = FRESH_CLASSES.includes(m.freshness) ? m.freshness : 'red';
    dot.className = 'pulse-dot ' + freshness;
    dot.title = 'freshness: ' + (m.freshness || 'unknown');
    const name = document.createElement('span');
    name.className = 'pulse-name';
    name.textContent = m.name || m.metric_id || '—';
    if (m.description) name.title = m.description;
    head.appendChild(dot);
    head.appendChild(name);
    card.appendChild(head);

    const id = document.createElement('div');
    id.className = 'pulse-id';
    id.textContent = [m.metric_id, m.uid].filter(Boolean).join(' · ');
    const targetUid = m.uid || m.metric_id;
    if (targetUid) {
        id.classList.add('clickable-link');
        id.title = t('pulse.view_signal');
        id.addEventListener('click', (e) => {
            e.stopPropagation();
            switchTab('signal', { metricUid: targetUid });
        });
    }
    card.appendChild(id);

    const val = document.createElement('div');
    val.className = 'pulse-value';
    const v = document.createElement('span');
    v.textContent = m.latest_value != null ? formatValue(m.latest_value) : '—';
    val.appendChild(v);
    if (m.unit) {
        const unit = document.createElement('span');
        unit.className = 'pulse-unit';
        unit.textContent = ' ' + m.unit;
        val.appendChild(unit);
    }
    card.appendChild(val);

    const meta = document.createElement('div');
    meta.className = 'pulse-meta';
    meta.textContent = [m.provider, m.grade].filter(Boolean).join(' · ') || '—';
    card.appendChild(meta);

    card.appendChild(buildSparkline(m.series || [], m.latest_value == null));
    return card;
}

function buildSparkline(series, noData) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 100 28');
    svg.setAttribute('preserveAspectRatio', 'none');
    svg.classList.add('pulse-spark');

    if (noData || !series || series.length < 2) {
        const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
        line.setAttribute('x1', '2');
        line.setAttribute('y1', '14');
        line.setAttribute('x2', '98');
        line.setAttribute('y2', '14');
        line.setAttribute('class', 'pulse-spark-placeholder');
        svg.appendChild(line);
        return svg;
    }

    const xs = series.map(p => Date.parse(p.t));
    const ys = series.map(p => p.v);
    const xMin = Math.min(...xs);
    const xMax = Math.max(...xs);
    const yMin = Math.min(...ys);
    const yMax = Math.max(...ys);
    const xSpan = xMax - xMin || 1;
    const ySpan = yMax - yMin;
    const pad = 2;
    const center = 28 / 2;
    const pts = series.map((p, i) => {
        const x = pad + ((xs[i] - xMin) / xSpan) * (100 - 2 * pad);
        const y = ySpan === 0 ? center : pad + (1 - (ys[i] - yMin) / ySpan) * (28 - 2 * pad);
        return x.toFixed(2) + ',' + y.toFixed(2);
    }).join(' ');
    const poly = document.createElementNS('http://www.w3.org/2000/svg', 'polyline');
    poly.setAttribute('points', pts);
    poly.setAttribute('class', 'pulse-spark-line');
    svg.appendChild(poly);
    return svg;
}
