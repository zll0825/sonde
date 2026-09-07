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
    const bar = document.getElementById('status-bar');
    bar.classList.remove('offline');

    const plugins = data.plugins || [];
    const expected = data.expected_plugins || [];
    const connected = plugins.filter(p => p.connected).length;
    const degraded = plugins.filter(p => p.connected && !p.healthy).length;
    const notReady = expected.filter(p => p.ready === false).length;
    const pluginsEl = document.getElementById('sb-plugins');
    const degradedTxt = degraded ? ' · ' + t('status.plugins_degraded', { n: degraded }) : '';
    const notReadyTxt = notReady ? ' · ' + t('status.plugins_not_ready', { n: notReady }) : '';
    pluginsEl.textContent = '⚡ ' + t('status.plugins_online', { n: connected }) + degradedTxt + notReadyTxt + ' ▾';
    pluginsEl.classList.toggle('danger', notReady > 0);
    const tooltipHead = t('status.plugins_control_hint');
    const tooltipBody = plugins.map(p =>
        t('status.plugin_tooltip', {
            name: p.name,
            status: pluginHealthLabel(p),
            time: p.last_collect_at ? relativeTime(p.last_collect_at) : '—',
            count: p.last_collect_count,
            err: p.last_collect_error ? ' · ' + p.last_collect_error : ''
        })
    ).join('\n');
    pluginsEl.title = tooltipHead + '\n\n' + tooltipBody;



    const metricsEl = document.getElementById('sb-metrics');
    metricsEl.textContent = t('status.metrics_count', { n: (data.metrics || []).length });

    const budget = data.budget || {};
    const today = Number.isFinite(budget.real_today) ? budget.real_today : (budget.today || 0);
    const limit = Number.isFinite(budget.real_limit) ? budget.real_limit : (budget.limit || 10);
    const hasDetailedBudget = Number.isFinite(budget.real_today);
    const budgetEl = document.getElementById('sb-budget');
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

    const rel = relativeTime(data.latest_data_at);
    document.getElementById('sb-latest').textContent =
        rel ? t('status.latest_data', { time: rel }) : t('status.no_data');

    renderPluginDetail(data);
    renderPulseGrid(data.metrics || []);
}

function renderStatusBarError() {
    const bar = document.getElementById('status-bar');
    bar.classList.add('offline');
    const pluginsEl = document.getElementById('sb-plugins');
    pluginsEl.textContent = t('status.interrupted');
    pluginsEl.classList.remove('danger');
    document.getElementById('sb-metrics').textContent = '';
    document.getElementById('sb-budget').textContent = '';
    document.getElementById('sb-latest').textContent = '';
}

function pluginHealthLabel(plugin) {
    if (!plugin.connected) return t('status.plugin_offline');
    return plugin.healthy ? t('status.plugin_healthy') : t('status.plugin_unhealthy');
}

function renderPluginDetail(data) {
    const wrap = document.getElementById('plugin-detail');
    if (wrap.classList.contains('hidden')) return;
    wrap.textContent = '';
    const views = mergePluginViews(data || {});
    const ul = document.createElement('ul');
    for (const view of views) {
        const live = view.live || {};
        const exp = view.expected || {};
        const li = document.createElement('li');
        const name = document.createElement('strong');
        name.textContent = live.name || exp.name || view.id;
        li.appendChild(name);
        const ready = exp.ready !== false;
        const meta = document.createElement('span');
        meta.className = (live.healthy && ready) ? '' : 'offline';
        const readyLabel = ready ? pluginHealthLabel(live) : t('status.plugin_not_ready');
        meta.textContent = ' · ' + readyLabel + ' · ' + (live.last_collect_at ? relativeTime(live.last_collect_at) : '—') + ' · ' + (live.last_collect_count || 0);
        li.appendChild(meta);
        if (!ready) {
            const missing = Object.keys(exp.secrets_present || {}).filter(k => !exp.secrets_present[k]);
            const warn = document.createElement('div');
            warn.className = 'offline';
            warn.textContent = t('status.missing_secrets', { keys: missing.join(', ') || '—' });
            li.appendChild(warn);
        }
        if (live.last_collect_error) {
            const error = document.createElement('div');
            error.className = 'offline';
            error.textContent = t('status.plugin_error', {
                err: live.last_collect_error,
                count: live.consecutive_errors || 0,
                duration: live.last_collect_duration_ms || 0
            });
            li.appendChild(error);
        }
        const actions = document.createElement('div');
        actions.style.cssText = 'margin-top:0.35rem; display:flex; gap:0.4rem; flex-wrap:wrap;';
        const pluginID = live.id || exp.id;
        const syncBtn = document.createElement('button');
        syncBtn.textContent = t('status.btn_sync');
        syncBtn.style.cssText = 'background:#2a3450; color:#64ffda; border:1px solid #64ffda; padding:0.15rem 0.45rem; border-radius:4px; font-size:0.72rem; cursor:pointer;';
        syncBtn.disabled = !live.connected;
        syncBtn.title = live.connected ? '' : t('status.sync_need_online');
        syncBtn.addEventListener('click', () => postControl('sync', pluginID, syncBtn));
        actions.appendChild(syncBtn);
        const backfillBtn = document.createElement('button');
        const canBackfill = !!(exp.capabilities && exp.capabilities.windowed_backfill);
        backfillBtn.textContent = t('status.btn_backfill');
        backfillBtn.style.cssText = 'background:#2a3450; color:#64ffda; border:1px solid #64ffda; padding:0.15rem 0.45rem; border-radius:4px; font-size:0.72rem; cursor:pointer;';
        backfillBtn.disabled = !canBackfill;
        backfillBtn.title = canBackfill ? '' : t('status.backfill_disabled');
        backfillBtn.addEventListener('click', () => openBackfillMenu(pluginID, actions, backfillBtn));
        actions.appendChild(backfillBtn);
        li.appendChild(actions);
        ul.appendChild(li);
    }
    wrap.appendChild(ul);
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

function togglePluginDetail() {
    const wrap = document.getElementById('plugin-detail');
    wrap.classList.toggle('hidden');
    if (!wrap.classList.contains('hidden')) renderPluginDetail(statusData || {});
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
