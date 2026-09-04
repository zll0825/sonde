// ── Research tab (P1#11) ─────────────────────────────────────────────
// ══════════════════════════════════════════════════════════════════════

let researchChart = null;
let cachedResearchFeedbacks = [];

async function fetchAlertFeedbacks(alertID) {
    if (!alertID) return [];
    try {
        const resp = await fetch(API_BASE + '/api/research/feedback?alert_id=' + encodeURIComponent(alertID));
        if (!resp.ok) return [];
        return await resp.json();
    } catch {
        return [];
    }
}

async function loadResearch(explicitAlertId) {
    const alertIDInput = document.getElementById('research-alert-id');
    const alertID = (explicitAlertId || (alertIDInput && alertIDInput.value) || '').trim();
    if (!alertID) {
        showToast(t('research.prompt_enter_id'), 'warn');
        return;
    }
    if (alertIDInput) alertIDInput.value = alertID;
    const detail = document.getElementById('research-detail');
    const btn = document.getElementById('btn-load-research');
    if (btn) {
        btn.disabled = true;
        btn.textContent = t('research.loading_btn');
    }
    detail.innerHTML = '<div class="empty">' + esc(t('research.loading_data')) + '</div>';
    try {
        const [respResearch, feedbacks] = await Promise.all([
            fetch(API_BASE + '/api/research/' + encodeURIComponent(alertID)),
            fetchAlertFeedbacks(alertID)
        ]);
        if (!respResearch.ok) throw new Error('HTTP ' + respResearch.status);
        const rc = await respResearch.json();
        cachedResearch = rc;
        cachedResearchFeedbacks = Array.isArray(feedbacks) ? feedbacks : [];
        renderResearchDetail(rc, cachedResearchFeedbacks);
    } catch (err) {
        detail.innerHTML = '<div class="empty">' + esc(t('common.error', { err: err.message })) + '</div>';
    } finally {
        if (btn) {
            btn.disabled = false;
            btn.textContent = t('research.btn_load');
        }
    }
}

function renderResearchDetail(rc, feedbacks) {
    const detail = document.getElementById('research-detail');
    const sections = [];

    const detailCard = document.createElement('div');
    detailCard.className = 'card';
    detailCard.style.cssText = 'margin-bottom:1.5rem;';

    const dTitle = document.createElement('h3');
    dTitle.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.95rem;';
    dTitle.textContent = rc.alert_id || '—';
    detailCard.appendChild(dTitle);

    if (rc.metric_name) {
        const pMetric = document.createElement('p');
        pMetric.style.cssText = 'font-size:0.82rem; color:#a8b2d1;';
        pMetric.textContent = t('research.metric_label', { name: rc.metric_name });
        detailCard.appendChild(pMetric);
    }
    if (rc.metric_id && rc.metric_id !== rc.metric_name) {
        const pMid = document.createElement('p');
        pMid.style.cssText = 'font-size:0.78rem; color:#8892b0;';
        pMid.textContent = t('research.metric_id_label', { id: rc.metric_id });
        detailCard.appendChild(pMid);
    }
    if (rc.research_question) {
        const pQ = document.createElement('p');
        pQ.style.cssText = 'font-size:0.82rem; color:#e0e6f0; margin-top:0.5rem;';
        pQ.textContent = rc.research_question;
        detailCard.appendChild(pQ);
    }
    const severitySpan = document.createElement('span');
    severitySpan.className = 'status-pill';
    severitySpan.style.cssText = 'margin-top:0.5rem; display:inline-block;';
    severitySpan.textContent = rc.severity || '—';
    detailCard.appendChild(severitySpan);

    sections.push(detailCard);

    // Current Verdict & Rationale Card
    const fbList = feedbacks !== undefined ? (Array.isArray(feedbacks) ? feedbacks : []) : (cachedResearchFeedbacks || []);
    const verdictDisplayCard = document.createElement('div');
    verdictDisplayCard.className = 'card verdict-display-card';

    if (fbList.length > 0) {
        const latest = fbList[0];
        verdictDisplayCard.classList.add('verdict-card-' + latest.verdict);

        const hCurrentVerdict = document.createElement('h3');
        hCurrentVerdict.style.cssText = 'color:#64ffda; margin-bottom:0.75rem; font-size:0.92rem; display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:0.5rem;';

        const hTitleWrap = document.createElement('div');
        hTitleWrap.style.cssText = 'display:flex; align-items:center; gap:0.5rem;';

        const titleSpan = document.createElement('span');
        titleSpan.textContent = t('research.current_verdict_title');
        hTitleWrap.appendChild(titleSpan);

        const vBadge = document.createElement('span');
        vBadge.className = 'verdict-badge verdict-' + latest.verdict;
        vBadge.textContent = t('research.verdict_badge_' + latest.verdict);
        hTitleWrap.appendChild(vBadge);
        hCurrentVerdict.appendChild(hTitleWrap);

        const timeSpan = document.createElement('span');
        timeSpan.style.cssText = 'font-size:0.75rem; color:#8892b0; font-weight:normal;';
        timeSpan.textContent = t('research.eval_meta', {
            time: new Date(latest.created_at).toLocaleString(),
            user: latest.user_id || 'anonymous'
        });
        hCurrentVerdict.appendChild(timeSpan);
        verdictDisplayCard.appendChild(hCurrentVerdict);

        // Rationale quote
        const pRat = document.createElement('div');
        pRat.style.cssText = 'background:#162035; border-left:3px solid #64ffda; padding:0.6rem 0.8rem; border-radius:4px; font-size:0.84rem; color:#e0e6f0; margin-bottom:0.75rem; white-space:pre-wrap;';
        if (latest.rationale && latest.rationale.trim()) {
            pRat.textContent = latest.rationale;
        } else {
            pRat.style.color = '#8892b0';
            pRat.style.fontStyle = 'italic';
            pRat.textContent = t('research.no_rationale');
        }
        verdictDisplayCard.appendChild(pRat);

        // Action tip
        const tipBox = document.createElement('div');
        tipBox.style.cssText = 'display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:0.5rem; font-size:0.78rem; color:#a8b2d1;';
        const tipText = document.createElement('span');
        if (latest.verdict === 'worth_researching') {
            tipText.textContent = t('research.worth_tip');
        } else if (latest.verdict === 'irrelevant') {
            tipText.textContent = t('research.irrelevant_tip');
        } else {
            tipText.textContent = t('research.duplicate_tip');
        }
        tipBox.appendChild(tipText);

        if (latest.verdict === 'irrelevant') {
            const btnGotoResolve = document.createElement('button');
            btnGotoResolve.type = 'button';
            btnGotoResolve.className = 'alert-page-btn';
            btnGotoResolve.style.cssText = 'background:rgba(245, 158, 11, 0.2); border-color:#f59e0b; color:#f59e0b; font-weight:600; cursor:pointer;';
            btnGotoResolve.textContent = t('research.btn_goto_resolve');
            btnGotoResolve.addEventListener('click', () => {
                switchTab('alerts', { alertId: rc.alert_id });
            });
            tipBox.appendChild(btnGotoResolve);
        }
        verdictDisplayCard.appendChild(tipBox);

        // History list if > 1
        if (fbList.length > 1) {
            const histTitle = document.createElement('h4');
            histTitle.style.cssText = 'color:#8892b0; margin-top:1rem; margin-bottom:0.4rem; font-size:0.8rem;';
            histTitle.textContent = t('research.verdict_history_title', { n: fbList.length });
            verdictDisplayCard.appendChild(histTitle);

            const histWrap = document.createElement('div');
            histWrap.style.cssText = 'background:#0f1523; border:1px solid #1a2340; border-radius:6px; padding:0.4rem 0.8rem;';
            for (let i = 0; i < fbList.length; i++) {
                const item = fbList[i];
                const itemDiv = document.createElement('div');
                itemDiv.className = 'verdict-history-item';

                const leftDiv = document.createElement('div');
                leftDiv.style.cssText = 'display:flex; align-items:center; gap:0.5rem; flex-wrap:wrap;';

                const seq = document.createElement('span');
                seq.style.cssText = 'color:#64ffda; font-size:0.75rem; font-weight:600;';
                seq.textContent = '#' + (fbList.length - i);
                leftDiv.appendChild(seq);

                const itemBadge = document.createElement('span');
                itemBadge.className = 'verdict-badge verdict-' + item.verdict;
                itemBadge.textContent = t('research.verdict_badge_' + item.verdict);
                leftDiv.appendChild(itemBadge);

                if (item.rationale) {
                    const rSpan = document.createElement('span');
                    rSpan.style.cssText = 'color:#ccd6f6; font-size:0.78rem;';
                    rSpan.textContent = '— ' + item.rationale;
                    leftDiv.appendChild(rSpan);
                }
                itemDiv.appendChild(leftDiv);

                const rightTime = document.createElement('span');
                rightTime.style.cssText = 'color:#6b7280; font-size:0.72rem; white-space:nowrap; margin-left:0.5rem;';
                rightTime.textContent = new Date(item.created_at).toLocaleString();
                itemDiv.appendChild(rightTime);

                histWrap.appendChild(itemDiv);
            }
            verdictDisplayCard.appendChild(histWrap);
        }
    } else {
        const hEmptyVerdict = document.createElement('h3');
        hEmptyVerdict.style.cssText = 'color:#8892b0; font-size:0.88rem; margin-bottom:0.25rem;';
        hEmptyVerdict.textContent = t('research.current_verdict_title');
        verdictDisplayCard.appendChild(hEmptyVerdict);

        const pEmpty = document.createElement('p');
        pEmpty.style.cssText = 'font-size:0.8rem; color:#6b7280; margin:0;';
        pEmpty.textContent = t('research.verdict_none');
        verdictDisplayCard.appendChild(pEmpty);
    }
    sections.push(verdictDisplayCard);

    // Timeline chart
    if (rc.recent_trend && rc.recent_trend.length > 0) {
        const timelineCard = document.createElement('div');
        timelineCard.className = 'card';
        timelineCard.style.cssText = 'margin-bottom:1.5rem;';
        const hTimeline = document.createElement('h3');
        hTimeline.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
        hTimeline.textContent = t('research.timeline_title');
        timelineCard.appendChild(hTimeline);
        const chartDiv = document.createElement('div');
        chartDiv.style.cssText = 'width:100%; height:250px;';
        timelineCard.appendChild(chartDiv);
        sections.push(timelineCard);
        setTimeout(() => renderResearchTrend(rc.recent_trend, chartDiv), 0);
    }

    // Overlays
    if (rc.overlays && rc.overlays.length > 0) {
        const overlayCard = document.createElement('div');
        overlayCard.className = 'card';
        overlayCard.style.cssText = 'margin-bottom:1.5rem;';
        const hOverlay = document.createElement('h3');
        hOverlay.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
        hOverlay.textContent = t('research.overlays_title', { n: rc.overlays.length });
        overlayCard.appendChild(hOverlay);

        const overlayGrid = document.createElement('div');
        overlayGrid.style.cssText = 'display:grid; grid-template-columns:repeat(auto-fill,minmax(180px,1fr)); gap:0.75rem;';

        for (const ov of rc.overlays) {
            const ovCard = document.createElement('div');
            ovCard.style.cssText = 'background:#0d1220; border:1px solid #1a2340; border-radius:8px; padding:0.6rem;';

            const ovTitle = document.createElement('div');
            ovTitle.style.cssText = 'font-size:0.78rem; font-weight:600; color:#e0e6f0;';
            ovTitle.textContent = ov.entity_name || ov.entity_id || '—';
            ovCard.appendChild(ovTitle);

            const ovSub = document.createElement('div');
            ovSub.style.cssText = 'font-size:0.7rem; color:#8892b0; margin-bottom:0.3rem;';
            const r = ov.correlation != null ? Number(ov.correlation).toFixed(3) : '—';
            const n = ov.sample_size != null ? ov.sample_size : '—';
            ovSub.textContent = 'r=' + r + ' · n=' + n;
            ovCard.appendChild(ovSub);

            if (ov.series && ov.series.length > 0) {
                ovCard.appendChild(buildResearchSparkline(ov.series));
            }

            overlayGrid.appendChild(ovCard);
        }
        overlayCard.appendChild(overlayGrid);
        sections.push(overlayCard);
    }

    // Relations
    if (rc.relations && rc.relations.length > 0) {
        const relCard = document.createElement('div');
        relCard.className = 'card';
        relCard.style.cssText = 'margin-bottom:1.5rem;';
        const hRel = document.createElement('h3');
        hRel.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
        hRel.textContent = t('research.relations_title');
        relCard.appendChild(hRel);
        const ulRel = document.createElement('ul');
        ulRel.style.cssText = 'list-style:none; font-size:0.8rem;';
        for (const r of rc.relations) {
            const li = document.createElement('li');
            li.style.cssText = 'padding:0.2rem 0;';
            li.textContent = (r.source_id || r.source || '—') + ' → ' + (r.target_id || r.target || '—') + ' · ' + (r.relation_type || '—');
            ulRel.appendChild(li);
        }
        relCard.appendChild(ulRel);
        sections.push(relCard);
    }

    // Provenance
    if (rc.provenance) {
        const provCard = document.createElement('div');
        provCard.className = 'card';
        provCard.style.cssText = 'margin-bottom:1.5rem;';
        const hProv = document.createElement('h3');
        hProv.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
        hProv.textContent = t('research.provenance_title');
        provCard.appendChild(hProv);
        const pProv = document.createElement('p');
        pProv.style.cssText = 'font-size:0.8rem; color:#a8b2d1; white-space:pre-wrap;';
        pProv.textContent = typeof rc.provenance === 'string' ? rc.provenance : (rc.provenance.evidence || rc_provenanceSummary(rc.provenance) || JSON.stringify(rc.provenance));
        provCard.appendChild(pProv);
        sections.push(provCard);
    }

    // Historical analogs
    if (rc.historical_analogs && rc.historical_analogs.length > 0) {
        const analogCard = document.createElement('div');
        analogCard.className = 'card';
        analogCard.style.cssText = 'margin-bottom:1.5rem;';
        const hAnalog = document.createElement('h3');
        hAnalog.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
        hAnalog.textContent = t('research.analogs_title');
        analogCard.appendChild(hAnalog);
        const ulAnalog = document.createElement('ul');
        ulAnalog.style.cssText = 'list-style:none; font-size:0.8rem;';
        for (const a of rc.historical_analogs) {
            const li = document.createElement('li');
            li.style.cssText = 'padding:0.25rem 0;';
            const year = a.year || a.date || '';
            const event = a.title || a.event || a.name || '';
            const relevance = a.relevance != null ? Number(a.relevance).toFixed(2) : '';
            li.textContent = (year ? year + ' — ' : '') + event + (relevance ? ' · ' + t('research.relevance', { r: relevance }) : '') + (a.source ? ' · ' + a.source : '');
            ulAnalog.appendChild(li);
        }
        analogCard.appendChild(ulAnalog);
        sections.push(analogCard);
    }

    // Verdict buttons + rationale
    const verdictCard = document.createElement('div');
    verdictCard.className = 'card';
    verdictCard.style.cssText = 'margin-bottom:1.5rem;';
    const hVerdict = document.createElement('h3');
    hVerdict.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.9rem;';
    hVerdict.textContent = (fbList && fbList.length > 0)
        ? t('research.form_title_update')
        : t('research.feedback_title');
    verdictCard.appendChild(hVerdict);

    const labelRationale = document.createElement('label');
    labelRationale.style.cssText = 'font-size:0.72rem; color:#8892b0;';
    labelRationale.textContent = t('research.rationale_label');
    verdictCard.appendChild(labelRationale);

    const taRationale = document.createElement('textarea');
    taRationale.style.cssText = 'background:#1a2340; border:1px solid #2a3450; color:#e0e6f0; padding:0.45rem 0.6rem; border-radius:6px; font-size:0.82rem; width:100%; box-sizing:border-box; margin-top:0.25rem; min-height:60px; resize:vertical;';
    taRationale.placeholder = t('research.rationale_placeholder');
    verdictCard.appendChild(taRationale);

    const btnRow = document.createElement('div');
    btnRow.style.cssText = 'display:flex; gap:0.5rem; margin-top:0.75rem; flex-wrap:wrap;';

    const btnWorth = document.createElement('button');
    btnWorth.textContent = t('research.btn_worth');
    btnWorth.style.cssText = 'background:#2dd4a7; color:#0a0e1a; border:none; padding:0.5rem 1rem; border-radius:6px; font-size:0.82rem; font-weight:600; cursor:pointer;';

    const btnIrrelevant = document.createElement('button');
    btnIrrelevant.textContent = t('research.btn_irrelevant');
    btnIrrelevant.style.cssText = 'background:#f59e0b; color:#0a0e1a; border:none; padding:0.5rem 1rem; border-radius:6px; font-size:0.82rem; font-weight:600; cursor:pointer;';

    const btnDup = document.createElement('button');
    btnDup.textContent = t('research.btn_duplicate');
    btnDup.style.cssText = 'background:#3b82f6; color:#fff; border:none; padding:0.5rem 1rem; border-radius:6px; font-size:0.82rem; font-weight:600; cursor:pointer;';

    const statusP = document.createElement('p');
    statusP.style.cssText = 'font-size:0.75rem; color:#8892b0; margin-top:0.5rem; display:none;';

    async function submitVerdict(verdict) {
        const rationale = taRationale.value.trim();
        btnWorth.disabled = true;
        btnIrrelevant.disabled = true;
        btnDup.disabled = true;
        statusP.style.display = 'block';
        statusP.textContent = t('research.submitting');
        try {
            const resp = await fetch(API_BASE + '/api/research/' + encodeURIComponent(rc.alert_id) + '/feedback', {
                method: 'POST',
                headers: mutationHeaders(),
                body: JSON.stringify({ verdict, rationale })
            });
            if (!resp.ok) {
                if (resp.status === 401 || resp.status === 403) {
                    showToast(t('token.need_token_toast'), 'error');
                    const btnToken = document.getElementById('btn-token-status');
                    if (btnToken) btnToken.click();
                    statusP.style.display = 'none';
                    return;
                }
                throw new Error('HTTP ' + resp.status);
            }
            const verdictLabel = verdict === 'worth_researching' ? t('research.verdict_worth')
                : (verdict === 'irrelevant' ? t('research.verdict_irrelevant') : t('research.verdict_duplicate'));
            showToast(t('research.submitted', { verdict: verdictLabel }), 'success');
            const newFeedbacks = await fetchAlertFeedbacks(rc.alert_id);
            cachedResearchFeedbacks = newFeedbacks;
            renderResearchDetail(rc, newFeedbacks);
        } catch (err) {
            statusP.textContent = t('common.error', { err: err.message });
            statusP.style.color = '#e85a5a';
        } finally {
            btnWorth.disabled = false;
            btnIrrelevant.disabled = false;
            btnDup.disabled = false;
        }
    }

    btnWorth.addEventListener('click', () => submitVerdict('worth_researching'));
    btnIrrelevant.addEventListener('click', () => submitVerdict('irrelevant'));
    btnDup.addEventListener('click', () => submitVerdict('duplicate'));

    btnRow.appendChild(btnWorth);
    btnRow.appendChild(btnIrrelevant);
    btnRow.appendChild(btnDup);
    verdictCard.appendChild(btnRow);
    verdictCard.appendChild(statusP);
    sections.push(verdictCard);

    detail.textContent = '';
    for (const sec of sections) detail.appendChild(sec);
}

function rc_provenanceSummary(prov) {
    if (typeof prov === 'string') return prov;
    if (prov.evidence) return prov.evidence;
    if (prov.sources && prov.sources.length > 0) return prov.sources.join('; ');
    return null;
}

function renderResearchTrend(points, el) {
    if (researchChart) { researchChart.dispose(); researchChart = null; }
    el.innerHTML = '';
    researchChart = echarts.init(el);
    researchChart.setOption({
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
        tooltip: { trigger: 'axis' },
        series: [{
            type: 'line',
            data: points.map(p => [new Date(p.time), p.value]),
            smooth: true,
            lineStyle: { color: '#64ffda', width: 2 },
            areaStyle: { color: 'rgba(100,255,218,0.1)' },
            symbol: 'circle',
            symbolSize: 4
        }]
    });
}

function buildResearchSparkline(series) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 160 32');
    svg.setAttribute('preserveAspectRatio', 'none');
    svg.style.cssText = 'width:100%; height:32px; display:block;';

    if (!series || series.length < 2) {
        const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
        line.setAttribute('x1', '2'); line.setAttribute('y1', '16');
        line.setAttribute('x2', '158'); line.setAttribute('y2', '16');
        line.setAttribute('stroke', '#2a3450'); line.setAttribute('stroke-width', '1');
        line.setAttribute('stroke-dasharray', '4 3');
        svg.appendChild(line);
        return svg;
    }

    const ys = series.map(p => typeof p === 'number' ? p : (p.v != null ? p.v : p.value));
    const yMin = Math.min(...ys);
    const yMax = Math.max(...ys);
    const ySpan = yMax - yMin || 1;
    const pad = 3;
    const pts = ys.map((v, i) => {
        const x = pad + (i / (ys.length - 1)) * (156);
        const y = pad + (1 - (v - yMin) / ySpan) * (32 - 2 * pad);
        return x.toFixed(1) + ',' + y.toFixed(1);
    }).join(' ');

    const poly = document.createElementNS('http://www.w3.org/2000/svg', 'polyline');
    poly.setAttribute('points', pts);
    poly.setAttribute('fill', 'none');
    poly.setAttribute('stroke', '#64ffda');
    poly.setAttribute('stroke-width', '1.5');
    svg.appendChild(poly);
    return svg;
}


// ── Signal Quality tab ───────────────────────────────────────────────
let signalChart = null;
async function loadSignalQuality() {
    const metricUID = document.getElementById('signal-metric-uid').value.trim();
    if (!metricUID) { showToast(t('signal.prompt_enter_uid'), 'warn'); return; }
    const list = document.getElementById('signal-quality-list');
    try {
        const resp = await fetch(API_BASE + '/api/signal/quality/' + encodeURIComponent(metricUID));
        if (!resp.ok) {
            list.innerHTML = '<div class="empty">' + esc(t('signal.not_available')) + '</div>';
            return;
        }
        const points = await resp.json();
        cachedSignalPoints = points;
        if (!points || points.length === 0) {
            list.innerHTML = '<div class="empty">' + esc(t('signal.no_observations')) + '</div>';
            return;
        }
        renderSignalTable(points);
        renderSignalChart(points);
    } catch (err) {
        list.innerHTML = '<div class="empty">' + esc(t('alerts.error_api')) + '</div>';
    }
}

function renderSignalTable(points) {
    const list = document.getElementById('signal-quality-list');
    if (!points || points.length === 0) {
        list.innerHTML = '<div class="empty">' + esc(t('signal.no_observations')) + '</div>';
        return;
    }
    list.innerHTML = '';
    const wrapper = document.createElement('div');
    wrapper.className = 'table-responsive';

    const table = document.createElement('table');
    table.className = 'data-table';
    table.style.fontSize = '0.75rem';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('signal.th_time'),
        t('signal.th_value'),
        t('signal.th_source'),
        t('signal.th_grade'),
        t('signal.th_quality')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        hRow.appendChild(th);
    });
    thead.appendChild(hRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const p of points.slice(0, 30)) {
        const tr = document.createElement('tr');

        // 1. Time
        const tdTime = document.createElement('td');
        tdTime.style.whiteSpace = 'nowrap';
        tdTime.textContent = new Date(p.time).toLocaleString();
        tr.appendChild(tdTime);

        // 2. Value
        const tdVal = document.createElement('td');
        tdVal.style.fontFamily = 'monospace';
        tdVal.style.whiteSpace = 'nowrap';
        if (p.value != null) {
            const num = Number(p.value);
            tdVal.textContent = !isNaN(num) && Math.abs(num) >= 1e6 ? num.toLocaleString(undefined, { maximumFractionDigits: 2 }) : String(p.value);
        } else {
            tdVal.textContent = '—';
        }
        tr.appendChild(tdVal);

        // 3. Source Class
        const tdSrc = document.createElement('td');
        const srcClass = String(p.source_class || 'unknown').toLowerCase();
        let srcColor = '#8892b0';
        let srcBg = 'rgba(136,146,176,0.15)';
        if (srcClass === 'real') {
            srcColor = '#64ffda';
            srcBg = 'rgba(100,255,218,0.15)';
        } else if (srcClass === 'mock') {
            srcColor = '#c084fc';
            srcBg = 'rgba(192,132,252,0.15)';
        } else if (srcClass === 'test') {
            srcColor = '#60a5fa';
            srcBg = 'rgba(96,165,250,0.15)';
        }
        tdSrc.innerHTML = `<span style="background:${srcBg}; color:${srcColor}; padding:0.12rem 0.4rem; border-radius:3px; font-size:0.7rem; font-weight:600;">${srcClass.toUpperCase()}</span>`;
        tr.appendChild(tdSrc);

        // 4. Grade
        const tdGrade = document.createElement('td');
        tdGrade.style.color = '#a8b2d1';
        tdGrade.textContent = p.grade || '—';
        tr.appendChild(tdGrade);

        // 5. Quality Score
        const tdScore = document.createElement('td');
        if (p.quality_score != null) {
            const score = Number(p.quality_score);
            let scoreColor = '#ef4444';
            let scoreBg = 'rgba(239,68,68,0.15)';
            if (score >= 75) {
                scoreColor = '#2dd4a7';
                scoreBg = 'rgba(45,212,167,0.15)';
            } else if (score >= 50) {
                scoreColor = '#f59e0b';
                scoreBg = 'rgba(245,158,11,0.15)';
            }
            tdScore.innerHTML = `<span style="background:${scoreBg}; color:${scoreColor}; font-weight:600; padding:0.12rem 0.45rem; border-radius:3px; font-size:0.72rem;">${score.toFixed(1)}</span>`;
        } else {
            tdScore.textContent = '—';
        }
        tr.appendChild(tdScore);

        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrapper.appendChild(table);
    list.appendChild(wrapper);
}

function renderSignalChart(points) {
    const el = document.getElementById('signal-quality-chart');
    if (signalChart) { signalChart.dispose(); signalChart = null; }
    el.innerHTML = '';
    signalChart = echarts.init(el);
    // Sort points chronologically ascending for correct time-series rendering
    const sorted = points.slice().sort((a, b) => new Date(a.time) - new Date(b.time));
    signalChart.setOption({
        backgroundColor: 'transparent',
        tooltip: {
            trigger: 'axis',
            formatter: function(params) {
                if (!params || !params[0]) return '';
                const p = params[0];
                const dateStr = new Date(p.value[0]).toLocaleString();
                const scoreVal = Number(p.value[1]).toFixed(1);
                return `${dateStr}<br/><span style="color:#64ffda;">●</span> ${t('signal.th_quality')}: <b>${scoreVal}</b>`;
            }
        },
        grid: { top: 20, right: 20, bottom: 30, left: 45 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a3450' } },
            axisLabel: { color: '#8892b0', fontSize: 10 }
        },
        yAxis: {
            type: 'value', min: 0, max: 100,
            axisLine: { lineStyle: { color: '#2a3450' } },
            axisLabel: { color: '#8892b0', fontSize: 10 },
            splitLine: { lineStyle: { color: '#1a2340' } }
        },
        series: [{
            name: 'Quality Score',
            type: 'line',
            data: sorted.map(p => [new Date(p.time), p.quality_score]),
            smooth: true,
            lineStyle: { color: '#64ffda', width: 2 },
            areaStyle: { color: 'rgba(100,255,218,0.1)' },
            symbol: 'circle',
            symbolSize: 4
        }]
    });
}

document.addEventListener('DOMContentLoaded', () => {
    const signalInput = document.getElementById('signal-metric-uid');
    if (signalInput) {
        signalInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                loadSignalQuality();
            }
        });
    }
});
