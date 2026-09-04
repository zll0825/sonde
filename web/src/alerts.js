let currentSelectedAlertId = null;

async function loadAlerts() {
    try {
        const resp = await fetch(API_BASE + '/api/alerts');
        const alerts = await resp.json();
        alertsList = alerts;
        renderAlerts(alerts);
        hasFetched = true;
        isOnline = true;
        document.getElementById('status-pill').textContent = t('status.online');
    } catch (err) {
        hasFetched = true;
        isOnline = false;
        document.getElementById('status-pill').textContent = t('status.offline');
        document.getElementById('alerts-list').innerHTML =
            '<div class="empty">' + esc(t('alerts.error_api')) + '</div>';
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
        return '<div class="alert-item ' + sev + '" data-alert-id="' + esc(a.id) + '">' +
            '<div class="alert-title">' + esc(a.title) + '</div>' +
            (a.summary ? '<div class="alert-summary">' + esc(a.summary) + '</div>' : '') +
            '<div class="alert-meta">' + esc(a.metric_id) + ' &middot; ' + esc(a.severity) + ' &middot; ' + new Date(a.triggered_at).toLocaleString() + '</div>' +
            '</div>';
    }).join('');
    list.querySelectorAll('.alert-item').forEach(el =>
        el.addEventListener('click', () => showAlertDetail(el.dataset.alertId)));
}

async function showAlertDetail(alertID) {
    currentSelectedAlertId = alertID;
    const detail = document.getElementById('alert-detail');
    detail.classList.add('active');

    try {
        const resp = await fetch(API_BASE + '/api/research/' + encodeURIComponent(alertID));
        if (!resp.ok) {
            document.getElementById('detail-title').textContent = alertID;
            document.getElementById('detail-summary').textContent = t('alerts.research_pending');
            return;
        }
        const rc = await resp.json();
        document.getElementById('detail-title').textContent = rc.alert_id || alertID;
        const metricDisplayName = rc.metric_name || rc.metric_id || '—';
        document.getElementById('detail-summary').innerHTML = esc(t('alerts.research_summary', {
            name: metricDisplayName,
            current: rc.current_value ?? 'N/A',
            threshold: rc.threshold ?? 'N/A'
        })) + (rc.metric_id ? ' &middot; <span class="clickable-link" id="alert-link-to-signal">' + esc(t('pulse.view_signal')) + '</span>' : '');

        const signalLink = document.getElementById('alert-link-to-signal');
        if (signalLink && rc.metric_id) {
            signalLink.addEventListener('click', () => {
                switchTab('signal', { metricUid: rc.metric_id });
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
            li.textContent = (m.alert_id || '—') + ' · ' + (m.metric_id || '—') + ' · ' + (m.reason || '—');
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

