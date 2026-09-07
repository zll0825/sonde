let currentSelectedAlertId = null;
let currentAlertFilter = 'active';
let currentAlertPage = 1;
let currentAlertLimit = 10;
let totalAlertCount = 0;
let totalAlertPages = 1;

async function loadAlerts(filter, page) {
    if (filter !== undefined && filter !== null) {
        if (filter !== currentAlertFilter) {
            currentAlertPage = 1;
        }
        currentAlertFilter = filter;
    }
    if (page !== undefined && page !== null) {
        currentAlertPage = page;
    }
    try {
        const queryParams = new URLSearchParams({
            status: currentAlertFilter || 'active',
            page: String(currentAlertPage),
            limit: String(currentAlertLimit),
            envelope: 'true'
        });
        const resp = await fetch(API_BASE + '/api/alerts?' + queryParams.toString());
        const data = await resp.json();

        let alerts = [];
        if (data && Array.isArray(data.items)) {
            alerts = data.items;
            totalAlertCount = data.total ?? 0;
            currentAlertPage = data.page ?? 1;
            currentAlertLimit = data.page_size ?? 10;
            totalAlertPages = data.total_pages ?? 1;
        } else if (Array.isArray(data)) {
            alerts = data;
            totalAlertCount = alerts.length;
            totalAlertPages = 1;
        }
        alertsList = alerts;
        renderAlerts(alerts);
        renderAlertPagination();
        hasFetched = true;
        isOnline = true;
        document.getElementById('status-pill').textContent = t('status.online');
    } catch (err) {
        hasFetched = true;
        isOnline = false;
        document.getElementById('status-pill').textContent = t('status.offline');
        document.getElementById('alerts-list').innerHTML =
            '<div class="empty">' + esc(t('alerts.error_api')) + '</div>';
        const pagination = document.getElementById('alerts-pagination');
        if (pagination) pagination.style.display = 'none';
    }
}

function renderAlerts(alerts) {
    const list = document.getElementById('alerts-list');
    if (!alerts || alerts.length === 0) {
        const rel = statusData && statusData.latest_data_at
            ? relativeTime(statusData.latest_data_at)
            : null;
        list.innerHTML = rel
            ? '<div class="empty">' + esc(t('alerts.empty_silent', { time: rel })) + '</div>'
            : '<div class="empty">' + esc(t('alerts.empty_silent_simple')) + '</div>';
        return;
    }
    list.innerHTML = alerts.map(a => {
        const sev = SEVERITIES.includes(a.severity) ? a.severity : 'info';
        const st = a.status || 'active';
        const statusBadge = '<span class="alert-status-badge status-' + esc(st) + '">' + esc(t('alerts.status_' + st)) + '</span>';
        let verdictBadge = '';
        if (a.latest_verdict) {
            verdictBadge = '<span class="verdict-badge verdict-' + esc(a.latest_verdict) + '">' + esc(t('alerts.verdict_' + a.latest_verdict)) + '</span>';
        }
        return '<div class="alert-item ' + sev + '" data-alert-id="' + esc(a.id) + '">' +
            '<div style="display:flex; justify-content:space-between; align-items:center;">' +
            '<div class="alert-title">' + esc(a.title) + '</div>' +
            '<div style="display:flex; align-items:center;">' + verdictBadge + statusBadge + '</div>' +
            '</div>' +
            (a.summary ? '<div class="alert-summary">' + esc(a.summary) + '</div>' : '') +
            '<div class="alert-meta" title="' + esc(a.metric_id || '') + '">' + esc(metricDisplayName(a.metric_id)) + ' &middot; ' + esc(a.severity) + ' &middot; ' + new Date(a.triggered_at).toLocaleString() + '</div>' +
            '</div>';
    }).join('');
    list.querySelectorAll('.alert-item').forEach(el =>
        el.addEventListener('click', () => showAlertDetail(el.dataset.alertId)));
}

async function showAlertDetail(alertID) {
    currentSelectedAlertId = alertID;
    const detail = document.getElementById('alert-detail');
    detail.classList.add('active');

    const currentAlert = alertsList && alertsList.find(a => a.id === alertID);
    renderAlertDetailStatus(currentAlert || { id: alertID, status: 'active' });

    try {
        const [respResearch, respFeedback] = await Promise.all([
            fetch(API_BASE + '/api/research/' + encodeURIComponent(alertID)),
            fetch(API_BASE + '/api/research/feedback?alert_id=' + encodeURIComponent(alertID)).catch(() => null)
        ]);
        let feedbacks = [];
        if (respFeedback && respFeedback.ok) {
            feedbacks = await respFeedback.json().catch(() => []);
        }

        renderAlertDetailVerdict(alertID, currentAlert, feedbacks);

        if (!respResearch.ok) {
            document.getElementById('detail-title').textContent = alertID;
            document.getElementById('detail-summary').textContent = t('alerts.research_pending');
            return;
        }
        const rc = await respResearch.json();
        document.getElementById('detail-title').textContent = rc.alert_id || alertID;
        const metricLabel = rc.metric_name || metricDisplayName(rc.metric_id);
        const summaryEl = document.getElementById('detail-summary');
        summaryEl.title = rc.metric_id || '';
        summaryEl.innerHTML = esc(t('alerts.research_summary', {
            name: metricLabel,
            current: rc.current_value ?? 'N/A',
            threshold: rc.threshold ?? 'N/A'
        })) + (rc.metric_id ? ' &middot; <span class="clickable-link" id="alert-link-to-signal">' + esc(t('pulse.view_signal')) + '</span>' : '');

        const targetMetric = (rc.timeline && rc.timeline[0] && rc.timeline[0].metric_uid) || rc.metric_id;
        const signalLink = document.getElementById('alert-link-to-signal');
        if (signalLink && (rc.metric_id || targetMetric)) {
            signalLink.addEventListener('click', () => {
                switchTab('signal', { metricUid: targetMetric || rc.metric_id });
            });
        }

        if (rc.recent_trend && rc.recent_trend.length > 0) {
            renderTrend(rc.recent_trend);
        } else {
            if (trendChart) { trendChart.dispose(); trendChart = null; }
            document.getElementById('trend-chart').innerHTML =
                '<div class="empty" style="height:150px;">' + esc(t('alerts.no_trend')) + '</div>';
        }

        const relList = document.getElementById('relations-list');
        if (rc.relations && rc.relations.length > 0) {
            relList.innerHTML = rc.relations.map(r =>
                '<li>' + esc(r.source_id) + ' &rarr; ' + esc(r.target_id) + ' (' + esc(r.relation_type) + ')</li>'
            ).join('');
        } else {
            relList.innerHTML = '<li>' + esc(t('alerts.none')) + '</li>';
        }
    } catch (err) {
        document.getElementById('detail-title').textContent = alertID;
        document.getElementById('detail-summary').textContent = t('alerts.error_detail');
    }
}

function renderAlertDetailVerdict(alertID, currentAlert, feedbacks) {
    const verdictBadge = document.getElementById('detail-verdict-badge');
    const verdictBox = document.getElementById('alert-verdict-box');
    if (!verdictBox) return;

    const fbList = Array.isArray(feedbacks) ? feedbacks : [];
    const latest = fbList.length > 0 ? fbList[0] : null;
    const verdict = latest ? latest.verdict : (currentAlert && currentAlert.latest_verdict);

    if (verdictBadge) {
        if (verdict) {
            verdictBadge.textContent = t('alerts.verdict_' + verdict);
            verdictBadge.className = 'verdict-badge verdict-' + verdict;
            verdictBadge.style.display = 'inline-block';
        } else {
            verdictBadge.textContent = t('alerts.verdict_none');
            verdictBadge.className = 'verdict-badge verdict-none';
            verdictBadge.style.display = 'inline-block';
        }
    }

    const currentStatus = (currentAlert && currentAlert.status) || 'active';

    if (latest) {
        verdictBox.style.display = 'block';
        verdictBox.innerHTML = '';

        const card = document.createElement('div');
        card.className = 'verdict-display-card verdict-card-' + latest.verdict;
        card.style.cssText = 'margin: 0.5rem 0 1rem 0; padding: 0.75rem 0.9rem;';

        const topRow = document.createElement('div');
        topRow.style.cssText = 'display:flex; justify-content:space-between; align-items:center; margin-bottom:0.4rem; flex-wrap:wrap; gap:0.4rem;';

        const titleDiv = document.createElement('div');
        titleDiv.style.cssText = 'display:flex; align-items:center; gap:0.4rem;';
        const strongTitle = document.createElement('strong');
        strongTitle.style.cssText = 'font-size:0.84rem; color:#64ffda;';
        strongTitle.textContent = t('alerts.verdict_label') + ':';
        titleDiv.appendChild(strongTitle);

        const badgeSpan = document.createElement('span');
        badgeSpan.className = 'verdict-badge verdict-' + latest.verdict;
        badgeSpan.textContent = t('research.verdict_badge_' + latest.verdict);
        titleDiv.appendChild(badgeSpan);
        topRow.appendChild(titleDiv);

        const timeSpan = document.createElement('span');
        timeSpan.style.cssText = 'font-size:0.72rem; color:#8892b0;';
        timeSpan.textContent = t('alerts.verdict_evaluated_at', { time: new Date(latest.created_at).toLocaleString() });
        topRow.appendChild(timeSpan);
        card.appendChild(topRow);

        if (latest.rationale && latest.rationale.trim()) {
            const ratDiv = document.createElement('div');
            ratDiv.style.cssText = 'font-size:0.8rem; color:#ccd6f6; margin-bottom:0.4rem; background:#162035; padding:0.4rem 0.6rem; border-left:3px solid #64ffda; border-radius:3px; white-space:pre-wrap;';
            ratDiv.textContent = latest.rationale;
            card.appendChild(ratDiv);
        }

        if (latest.verdict === 'irrelevant' && currentStatus !== 'resolved') {
            const suggestBox = document.createElement('div');
            suggestBox.className = 'verdict-suggest-box';
            suggestBox.style.cssText = 'margin-top:0.4rem;';

            const suggestText = document.createElement('span');
            suggestText.style.cssText = 'font-size:0.78rem; color:#f59e0b;';
            suggestText.textContent = t('alerts.verdict_suggest_resolve');
            suggestBox.appendChild(suggestText);

            const btnResolveNow = document.createElement('button');
            btnResolveNow.type = 'button';
            btnResolveNow.className = 'alert-action-btn btn-resolve';
            btnResolveNow.style.cssText = 'padding:0.25rem 0.6rem; font-size:0.75rem; cursor:pointer;';
            btnResolveNow.textContent = t('alerts.verdict_btn_resolve_now');
            btnResolveNow.addEventListener('click', () => updateAlertStatus(alertID, 'resolved'));
            suggestBox.appendChild(btnResolveNow);

            card.appendChild(suggestBox);
        }

        const bottomRow = document.createElement('div');
        bottomRow.style.cssText = 'display:flex; justify-content:flex-end; margin-top:0.3rem;';
        const linkDetail = document.createElement('span');
        linkDetail.className = 'clickable-link';
        linkDetail.style.cssText = 'font-size:0.78rem; color:#64ffda;';
        linkDetail.textContent = t('alerts.verdict_btn_view_research');
        linkDetail.addEventListener('click', () => {
            switchTab('research', { alertId: alertID });
        });
        bottomRow.appendChild(linkDetail);
        card.appendChild(bottomRow);

        verdictBox.appendChild(card);
    } else {
        verdictBox.style.display = 'block';
        verdictBox.innerHTML = '';

        const emptyBox = document.createElement('div');
        emptyBox.style.cssText = 'display:flex; justify-content:space-between; align-items:center; background:#111827; border:1px dashed #2a3450; border-radius:6px; padding:0.5rem 0.75rem; margin:0.5rem 0 1rem 0;';

        const unreviewedText = document.createElement('span');
        unreviewedText.style.cssText = 'font-size:0.78rem; color:#8892b0;';
        unreviewedText.textContent = t('alerts.verdict_none');
        emptyBox.appendChild(unreviewedText);

        const linkGotoEval = document.createElement('span');
        linkGotoEval.className = 'clickable-link';
        linkGotoEval.style.cssText = 'font-size:0.78rem; color:#64ffda;';
        linkGotoEval.textContent = t('alerts.verdict_btn_goto_research');
        linkGotoEval.addEventListener('click', () => {
            switchTab('research', { alertId: alertID });
        });
        emptyBox.appendChild(linkGotoEval);

        verdictBox.appendChild(emptyBox);
    }
}

function renderAlertDetailStatus(alertItem) {
    const badge = document.getElementById('detail-status-badge');
    const actionsBar = document.getElementById('alert-actions-bar');
    if (!badge || !actionsBar) return;

    const st = alertItem.status || 'active';
    badge.textContent = t('alerts.status_' + st);
    badge.className = 'alert-status-badge status-' + st;
    badge.style.display = 'inline-block';

    actionsBar.innerHTML = '';
    actionsBar.style.display = 'flex';

    if (st === 'active') {
        const btnAck = document.createElement('button');
        btnAck.type = 'button';
        btnAck.className = 'alert-action-btn btn-ack';
        btnAck.textContent = t('alerts.action_ack');
        btnAck.addEventListener('click', () => updateAlertStatus(alertItem.id, 'acknowledged'));
        actionsBar.appendChild(btnAck);

        const btnResolve = document.createElement('button');
        btnResolve.type = 'button';
        btnResolve.className = 'alert-action-btn btn-resolve';
        btnResolve.textContent = t('alerts.action_resolve');
        btnResolve.addEventListener('click', () => updateAlertStatus(alertItem.id, 'resolved'));
        actionsBar.appendChild(btnResolve);

        const btnSilence = document.createElement('button');
        btnSilence.type = 'button';
        btnSilence.className = 'alert-action-btn btn-silence';
        btnSilence.textContent = t('alerts.action_silence');
        btnSilence.addEventListener('click', () => updateAlertStatus(alertItem.id, 'silenced'));
        actionsBar.appendChild(btnSilence);
    } else if (st === 'acknowledged') {
        const btnResolve = document.createElement('button');
        btnResolve.type = 'button';
        btnResolve.className = 'alert-action-btn btn-resolve';
        btnResolve.textContent = t('alerts.action_resolve');
        btnResolve.addEventListener('click', () => updateAlertStatus(alertItem.id, 'resolved'));
        actionsBar.appendChild(btnResolve);

        const btnReactivate = document.createElement('button');
        btnReactivate.type = 'button';
        btnReactivate.className = 'alert-action-btn btn-reactivate';
        btnReactivate.textContent = t('alerts.action_reactivate');
        btnReactivate.addEventListener('click', () => updateAlertStatus(alertItem.id, 'active'));
        actionsBar.appendChild(btnReactivate);

        const btnSilence = document.createElement('button');
        btnSilence.type = 'button';
        btnSilence.className = 'alert-action-btn btn-silence';
        btnSilence.textContent = t('alerts.action_silence');
        btnSilence.addEventListener('click', () => updateAlertStatus(alertItem.id, 'silenced'));
        actionsBar.appendChild(btnSilence);
    } else {
        const btnReactivate = document.createElement('button');
        btnReactivate.type = 'button';
        btnReactivate.className = 'alert-action-btn btn-reactivate';
        btnReactivate.textContent = t('alerts.action_reactivate');
        btnReactivate.addEventListener('click', () => updateAlertStatus(alertItem.id, 'active'));
        actionsBar.appendChild(btnReactivate);
    }
}

async function updateAlertStatus(alertID, newStatus) {
    try {
        const resp = await fetch(API_BASE + '/api/alerts/' + encodeURIComponent(alertID), {
            method: 'PATCH',
            headers: mutationHeaders(),
            body: JSON.stringify({ status: newStatus })
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
        showToast(t('toast.alert_status_updated', { status: t('alerts.status_' + newStatus) }), 'success');
        if (alertsList) {
            const found = alertsList.find(a => a.id === alertID);
            if (found) found.status = newStatus;
        }
        renderAlertDetailStatus({ id: alertID, status: newStatus });
        await loadAlerts();
    } catch (err) {
        showToast(t('common.error', { err: err.message }), 'error');
    }
}

function initAlertFilters() {
    const bar = document.getElementById('alert-filter-bar');
    if (!bar) return;
    bar.querySelectorAll('.alert-filter-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            bar.querySelectorAll('.alert-filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            loadAlerts(btn.dataset.filter, 1);
        });
    });
}

function renderAlertPagination() {
    const pagination = document.getElementById('alerts-pagination');
    if (!pagination) return;
    if (totalAlertCount > 0) {
        pagination.style.display = 'flex';
    } else {
        pagination.style.display = 'none';
        return;
    }

    const btnPrev = document.getElementById('btn-alert-prev');
    const btnNext = document.getElementById('btn-alert-next');
    const pageInfo = document.getElementById('alert-page-info');
    const totalInfo = document.getElementById('alert-total-info');
    const sizeSelect = document.getElementById('alert-page-size');

    if (btnPrev) {
        btnPrev.disabled = currentAlertPage <= 1;
    }
    if (btnNext) {
        btnNext.disabled = currentAlertPage >= totalAlertPages;
    }
    if (pageInfo) {
        pageInfo.textContent = t('alerts.page_info', { page: currentAlertPage, total: totalAlertPages });
    }
    if (totalInfo) {
        totalInfo.textContent = t('alerts.total_items', { total: totalAlertCount });
    }
    if (sizeSelect && String(sizeSelect.value) !== String(currentAlertLimit)) {
        sizeSelect.value = String(currentAlertLimit);
    }
}

function initAlertPagination() {
    const btnPrev = document.getElementById('btn-alert-prev');
    const btnNext = document.getElementById('btn-alert-next');
    const sizeSelect = document.getElementById('alert-page-size');

    if (btnPrev) {
        btnPrev.addEventListener('click', () => {
            if (currentAlertPage > 1) {
                loadAlerts(null, currentAlertPage - 1);
            }
        });
    }
    if (btnNext) {
        btnNext.addEventListener('click', () => {
            if (currentAlertPage < totalAlertPages) {
                loadAlerts(null, currentAlertPage + 1);
            }
        });
    }
    if (sizeSelect) {
        sizeSelect.addEventListener('change', (e) => {
            const newLimit = parseInt(e.target.value, 10);
            if (newLimit && newLimit > 0) {
                currentAlertLimit = newLimit;
                currentAlertPage = 1;
                loadAlerts();
            }
        });
    }
}

function renderTrend(points) {
    const el = document.getElementById('trend-chart');
    if (trendChart) {
        trendChart.dispose();
        trendChart = null;
    }
    el.innerHTML = '';
    trendChart = echarts.init(el);
    trendChart.setOption({
        backgroundColor: 'transparent',
        grid: { top: 20, right: 20, bottom: 30, left: 50 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a3450' } },
            axisLabel: { color: '#8892b0', fontSize: 10 }
        },
        yAxis: {
            type: 'value',
            axisLine: { lineStyle: { color: '#2a3450' } },
            axisLabel: { color: '#8892b0', fontSize: 10 },
            splitLine: { lineStyle: { color: '#1a2340' } }
        },
        series: [{
            type: 'line',
            data: points.map(p => [new Date(p.time), p.value]),
            smooth: true,
            lineStyle: { color: '#64ffda', width: 2 },
            areaStyle: { color: 'rgba(100,255,218,0.1)' },
            symbol: 'none'
        }]
    });
}

// ══════════════════════════════════════════════════════════════════════
// ── Clusters tab (P1#12) ─────────────────────────────────────────────
// ══════════════════════════════════════════════════════════════════════

async function loadClusters() {
    const list = document.getElementById('clusters-list');
    const btn = document.getElementById('btn-refresh-clusters');
    if (btn) btn.disabled = true;
    list.innerHTML = '<div class="empty">' + esc(t('clusters.loading')) + '</div>';
    try {
        const resp = await fetch(API_BASE + '/api/clusters/');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const clusters = await resp.json();
        cachedClusters = clusters;
        renderClusters(clusters);
    } catch (err) {
        list.innerHTML = '<div class="empty">' + esc(t('common.error', { err: err.message })) + '</div>';
    } finally {
        if (btn) btn.disabled = false;
    }
}

function renderClusters(clusters) {
    const list = document.getElementById('clusters-list');
    if (!clusters || clusters.length === 0) {
        list.innerHTML = '<div class="empty">' + esc(t('clusters.empty_none')) + '</div>';
        return;
    }

    list.textContent = '';
    const table = document.createElement('table');
    table.className = 'data-table';
    const thead = document.createElement('thead');
    const headerRow = document.createElement('tr');
    [
        t('clusters.th_id'),
        t('clusters.th_entity'),
        t('clusters.th_size'),
        t('clusters.th_priority'),
        t('clusters.th_coalesced'),
        t('clusters.th_actions')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        headerRow.appendChild(th);
    });
    thead.appendChild(headerRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const c of clusters) {
        const tr = document.createElement('tr');

        const tdId = document.createElement('td');
        tdId.textContent = (c.cluster_id || c.id || '').substring(0, 8);
        tr.appendChild(tdId);

        const tdEntity = document.createElement('td');
        tdEntity.textContent = c.primary_entity || '—';
        tr.appendChild(tdEntity);

        const tdSize = document.createElement('td');
        tdSize.textContent = c.alerts ? c.alerts.length : (c.member_count != null ? c.member_count : '0');
        tr.appendChild(tdSize);

        const tdPrio = document.createElement('td');
        tdPrio.textContent = c.priority != null ? Number(c.priority).toFixed(1) : '—';
        tr.appendChild(tdPrio);

        const tdCoal = document.createElement('td');
        tdCoal.textContent = c.coalesced ? t('clusters.yes') : t('clusters.no');
        tr.appendChild(tdCoal);

        const tdAction = document.createElement('td');
        const expandBtn = document.createElement('button');
        expandBtn.textContent = t('clusters.expand');
        expandBtn.style.cssText = 'background:#2a3450; color:#64ffda; border:1px solid #64ffda; padding:0.25rem 0.6rem; border-radius:4px; font-size:0.75rem; cursor:pointer;';
        expandBtn.addEventListener('click', () => toggleClusterDetail(c, tr, expandBtn));
        tdAction.appendChild(expandBtn);
        tr.appendChild(tdAction);

        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    list.appendChild(table);
}

function toggleClusterDetail(cluster, row, btn) {
    const existing = row.nextElementSibling;
    if (existing && existing.classList.contains('cluster-detail-row')) {
        existing.remove();
        btn.textContent = t('clusters.expand');
        return;
    }

    const detailTr = document.createElement('tr');
    detailTr.className = 'cluster-detail-row';
    const detailTd = document.createElement('td');
    detailTd.colSpan = 6;
    detailTd.style.cssText = 'background:#0d1220; padding:1rem;';

    const panel = document.createElement('div');
    panel.style.cssText = 'font-size:0.8rem;';

    // Alerts list
    const hAlerts = document.createElement('h4');
    hAlerts.style.cssText = 'color:#64ffda; margin-bottom:0.4rem;';
    const alertIDs = cluster.merged_alert_ids || [];
    hAlerts.textContent = t('clusters.alerts_count', { n: alertIDs.length });
    panel.appendChild(hAlerts);

    if (alertIDs.length > 0) {
        const ulAlerts = document.createElement('ul');
        ulAlerts.style.cssText = 'list-style:none; margin-bottom:0.8rem;';
        for (const alertID of alertIDs) {
            const li = document.createElement('li');
            li.style.cssText = 'padding:0.2rem 0;';
            const chip = document.createElement('span');
            chip.className = 'alert-chip clickable';
            chip.textContent = alertID;
            chip.title = t('clusters.chip_tooltip');
            chip.addEventListener('click', () => switchTab('research', { alertId: alertID }));
            li.appendChild(chip);
            ulAlerts.appendChild(li);
        }
        panel.appendChild(ulAlerts);
    } else {
        const pNone = document.createElement('p');
        pNone.style.cssText = 'color:#4a5570; font-size:0.75rem; margin-bottom:0.8rem;';
        pNone.textContent = t('clusters.no_alerts');
        panel.appendChild(pNone);
    }

    // Merge trail
    const hMerge = document.createElement('h4');
    hMerge.style.cssText = 'color:#64ffda; margin-bottom:0.4rem;';
    hMerge.textContent = t('clusters.merge_trail');
    panel.appendChild(hMerge);

    const mergeEntries = cluster.merge_trail && Array.isArray(cluster.merge_trail.entries)
        ? cluster.merge_trail.entries : [];
    if (mergeEntries.length > 0) {
        const ulMerge = document.createElement('ul');
        ulMerge.style.cssText = 'list-style:none; margin-bottom:0.8rem;';
        for (const m of mergeEntries) {
            const li = document.createElement('li');
            li.style.cssText = 'padding:0.2rem 0;';
            li.textContent = (m.alert_id || '—') + ' · ' + metricDisplayName(m.metric_id) + ' · ' + (m.reason || '—');
            li.title = m.metric_id || '';
            ulMerge.appendChild(li);
        }
        panel.appendChild(ulMerge);
    } else {
        const pMerge = document.createElement('p');
        pMerge.style.cssText = 'color:#4a5570; font-size:0.75rem; margin-bottom:0.8rem;';
        pMerge.textContent = t('clusters.merge_none');
        panel.appendChild(pMerge);
    }

    // Raw JSON
    const hJson = document.createElement('h4');
    hJson.style.cssText = 'color:#64ffda; margin-bottom:0.4rem; cursor:pointer;';
    hJson.textContent = t('clusters.raw_json');
    panel.appendChild(hJson);

    const preJson = document.createElement('pre');
    preJson.style.cssText = 'display:none; background:#131a2e; border:1px solid #1a2340; border-radius:6px; padding:0.75rem; max-height:240px; overflow:auto; font-size:0.7rem; color:#a8b2d1; margin-top:0.4rem;';
    preJson.textContent = JSON.stringify(cluster, null, 2);
    panel.appendChild(preJson);

    hJson.addEventListener('click', () => {
        const isOpen = preJson.style.display !== 'none';
        preJson.style.display = isOpen ? 'none' : 'block';
        hJson.textContent = isOpen ? t('clusters.raw_json') : t('clusters.raw_json_open');
    });

    detailTd.appendChild(panel);
    detailTr.appendChild(detailTd);
    row.after(detailTr);
    btn.textContent = t('clusters.collapse');
}

