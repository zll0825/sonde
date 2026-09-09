// ── Ontology tab (P1#13 — candidate discovery/review) ─────────────────
// ══════════════════════════════════════════════════════════════════════

let ontologyLoaded = false;

async function loadOntology() {
    const list = document.getElementById('ontology-list');
    if (!ontologyLoaded) {
        ontologyLoaded = true;
        try {
            const resp = await fetch(API_BASE + '/api/ontology/relations/');
            const rels = await resp.json();
            cachedOntology = rels;
            renderOntologyTable(rels);
        } catch (err) {
            list.innerHTML = '<div class="empty">' + esc(t('alerts.error_api')) + '</div>';
        }
    }
}

function renderOntologyTable(rels) {
    const list = document.getElementById('ontology-list');
    if (!rels || rels.length === 0) {
        list.innerHTML = '<div class="empty">' + esc(t('ontology.empty_relations')) + '</div>';
        return;
    }
    list.innerHTML = '';
    const table = document.createElement('table');
    table.className = 'data-table';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('ontology.th_rel_id'),
        t('ontology.th_source'),
        t('ontology.th_target'),
        t('ontology.th_type'),
        t('ontology.th_direction'),
        t('ontology.th_created')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        hRow.appendChild(th);
    });
    thead.appendChild(hRow);
    table.appendChild(thead);
    const tbody = document.createElement('tbody');
    for (const r of rels) {
        const tr = document.createElement('tr');
        const cells = [
            (r.relation_id || '').substring(0, 12),
            r.source_id || '—',
            r.target_id || '—',
            r.relation_type || '—',
            r.direction || '—',
            r.created_at ? new Date(r.created_at).toLocaleString() : '—'
        ];
        for (const c of cells) {
            const td = document.createElement('td');
            td.textContent = c;
            tr.appendChild(td);
        }
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    list.appendChild(table);
}

async function discoverCandidates() {
    const container = document.getElementById('ontology-candidates-list');
    const summary = document.getElementById('ontology-candidates-summary');
    const btn = document.getElementById('btn-discover-candidates');
    btn.disabled = true;
    btn.textContent = t('ontology.btn_running');
    container.innerHTML = '<div class="empty">' + esc(t('ontology.discovering')) + '</div>';
    try {
        let candidates = null;
        let updatedAt = null;
        try {
            const res = await fetch(API_BASE + '/api/ontology/discover', { method: 'POST', headers: mutationHeaders(), body: JSON.stringify({}) });
            if (res.ok) {
                const data = await res.json();
                candidates = data.candidates || data;
                updatedAt = data.updated_at || data.discovered_at;
            }
        } catch (e) { /* ignore */ }

        if (!candidates) {
            const res2 = await fetch(API_BASE + '/api/ontology/candidates?pending=true');
            if (res2.ok) {
                const data2 = await res2.json();
                candidates = data2.candidates || data2;
                updatedAt = data2.updated_at || updatedAt;
            }
        }

        if (!candidates) {
            const res3 = await fetch(API_BASE + '/api/ontology/candidates');
            if (res3.ok) {
                const data3 = await res3.json();
                candidates = data3.candidates || (Array.isArray(data3) ? data3 : null);
                updatedAt = data3.updated_at || updatedAt;
            }
        }

        cachedCandidates = candidates || [];
        cachedCandidatesUpdatedAt = updatedAt;
        renderCandidates(cachedCandidates, cachedCandidatesUpdatedAt);
    } catch (err) {
        container.innerHTML = '<div class="empty">' + esc(t('common.error', { err: err.message })) + '</div>';
    } finally {
        btn.disabled = false;
        btn.textContent = t('ontology.btn_discover');
    }
}

function renderCandidates(candidates, updatedAt) {
    const container = document.getElementById('ontology-candidates-list');
    const summary = document.getElementById('ontology-candidates-summary');

    if (updatedAt) {
        summary.textContent = t('ontology.updated_at', { time: new Date(updatedAt).toLocaleString() });
    } else {
        summary.textContent = '';
    }

    if (!candidates || candidates.length === 0) {
        container.innerHTML = '<div class="empty">' + esc(t('ontology.empty_candidates')) + '</div>';
        return;
    }

    container.textContent = '';
    const info = document.createElement('p');
    info.style.cssText = 'font-size:0.78rem; color:#8892b0; margin-bottom:0.5rem;';
    info.textContent = t('ontology.candidates_pending_count', { n: candidates.length });
    container.appendChild(info);

    const table = document.createElement('table');
    table.className = 'data-table';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('ontology.th_source'),
        t('ontology.th_target'),
        t('ontology.th_type'),
        t('ontology.th_direction'),
        t('ontology.th_confidence'),
        t('ontology.th_pvalue'),
        t('ontology.th_sample'),
        t('ontology.th_lookback'),
        t('ontology.th_actions')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        hRow.appendChild(th);
    });
    thead.appendChild(hRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const c of candidates) {
        const tr = document.createElement('tr');
        const id = c.id || c.candidate_id || '';

        const tdSrc = document.createElement('td');
        tdSrc.textContent = c.source_id || c.source || '—';
        tr.appendChild(tdSrc);

        const tdTgt = document.createElement('td');
        tdTgt.textContent = c.target_id || c.target || '—';
        tr.appendChild(tdTgt);

        const tdType = document.createElement('td');
        tdType.textContent = c.relation_type || c.relation || '—';
        tr.appendChild(tdType);

        const tdDir = document.createElement('td');
        tdDir.textContent = c.direction || '—';
        tr.appendChild(tdDir);

        const tdConf = document.createElement('td');
        const confVal = c.confidence != null ? Number(c.confidence) : 0;
        const confPct = Math.max(0, Math.min(100, confVal * 100));
        const barWrap = document.createElement('div');
        barWrap.style.cssText = 'width:80px; height:8px; background:#1a2340; border-radius:4px; overflow:hidden;';
        const barFill = document.createElement('div');
        const barColor = confPct >= 70 ? '#2dd4a7' : confPct >= 40 ? '#e8c34a' : '#e85a5a';
        barFill.style.cssText = 'width:' + confPct + '%; height:100%; background:' + barColor + ';';
        barWrap.appendChild(barFill);
        tdConf.appendChild(barWrap);
        tr.appendChild(tdConf);

        const tdPVal = document.createElement('td');
        if (c.p_value != null && typeof c.p_value === 'number') {
            tdPVal.textContent = c.p_value.toExponential(2);
        } else {
            tdPVal.textContent = '—';
        }
        tdPVal.style.fontSize = '0.72rem';
        tr.appendChild(tdPVal);

        const tdSample = document.createElement('td');
        tdSample.textContent = c.sample_size != null ? c.sample_size : (c.n != null ? c.n : '—');
        tr.appendChild(tdSample);

        const tdLookback = document.createElement('td');
        tdLookback.textContent = c.lookback_days != null ? c.lookback_days + 'd' : (c.lookback != null ? c.lookback : '—');
        tr.appendChild(tdLookback);

        const tdActions = document.createElement('td');
        tdActions.style.cssText = 'white-space:nowrap;';

        const btnAccept = document.createElement('button');
        btnAccept.textContent = t('ontology.btn_accept');
        btnAccept.style.cssText = 'background:#2dd4a7; color:#0a0e1a; border:none; padding:0.25rem 0.5rem; border-radius:4px; font-size:0.7rem; font-weight:600; cursor:pointer; margin-right:0.3rem;';

        const btnReject = document.createElement('button');
        btnReject.textContent = t('ontology.btn_reject');
        btnReject.style.cssText = 'background:#e85a5a; color:#fff; border:none; padding:0.25rem 0.5rem; border-radius:4px; font-size:0.7rem; font-weight:600; cursor:pointer;';

        btnAccept.addEventListener('click', () => submitCandidateVerdict(id, 'accept', tr, btnAccept, btnReject));
        btnReject.addEventListener('click', () => openCandidateRejectPanel(id, tr, btnAccept, btnReject));

        tdActions.appendChild(btnAccept);
        tdActions.appendChild(btnReject);
        tr.appendChild(tdActions);

        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    container.appendChild(table);
}

function openCandidateRejectPanel(candidateId, tr, btnAccept, btnReject) {
    const existing = tr.nextElementSibling;
    if (existing && existing.classList.contains('candidate-reject-row')) {
        existing.remove();
        return;
    }
    const rejectTr = document.createElement('tr');
    rejectTr.className = 'candidate-reject-row';
    const rejectTd = document.createElement('td');
    rejectTd.colSpan = 9;
    rejectTd.style.cssText = 'background:#0d1220; padding:0.6rem 1rem;';

    const form = document.createElement('div');
    form.className = 'inline-reject-panel';

    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'inline-reject-input';
    input.placeholder = t('ontology.reject_reason_placeholder');

    const btnConfirm = document.createElement('button');
    btnConfirm.type = 'button';
    btnConfirm.textContent = t('ontology.btn_confirm_reject');
    btnConfirm.style.cssText = 'background:#e85a5a; color:#fff; border:none; padding:0.3rem 0.6rem; border-radius:4px; font-size:0.75rem; font-weight:600; cursor:pointer;';

    const btnCancel = document.createElement('button');
    btnCancel.type = 'button';
    btnCancel.textContent = t('ontology.btn_cancel_reject');
    btnCancel.style.cssText = 'background:#2a3450; color:#8892b0; border:none; padding:0.3rem 0.6rem; border-radius:4px; font-size:0.75rem; cursor:pointer;';
    btnCancel.addEventListener('click', () => rejectTr.remove());

    btnConfirm.addEventListener('click', async () => {
        const reason = input.value.trim();
        btnConfirm.disabled = true;
        btnCancel.disabled = true;
        try {
            await submitCandidateVerdict(candidateId, 'reject', tr, btnAccept, btnReject, reason);
            rejectTr.remove();
        } catch (e) {
            btnConfirm.disabled = false;
            btnCancel.disabled = false;
        }
    });

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') btnConfirm.click();
        if (e.key === 'Escape') rejectTr.remove();
    });

    form.appendChild(input);
    form.appendChild(btnConfirm);
    form.appendChild(btnCancel);
    rejectTd.appendChild(form);
    rejectTr.appendChild(rejectTd);
    tr.after(rejectTr);
    input.focus();
}

async function submitCandidateVerdict(candidateId, verdict, row, btnAccept, btnReject, reason = '') {
    btnAccept.disabled = true;
    btnReject.disabled = true;
    try {
        const resp = await fetch(API_BASE + '/api/ontology/candidates/' + encodeURIComponent(candidateId) + '/' + verdict, {
            method: 'POST',
            headers: mutationHeaders(),
            body: JSON.stringify(reason ? { reason } : {})
        });
        if (!resp.ok) {
            if (resp.status === 401 || resp.status === 403) {
                showToast(t('token.need_token_toast'), 'error');
                const btnToken = document.getElementById('btn-token-status');
                if (btnToken) btnToken.click();
                btnAccept.disabled = false;
                btnReject.disabled = false;
                return;
            }
            const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
            throw new Error(err.error || ('HTTP ' + resp.status));
        }
        showToast(verdict === 'accept' ? t('toast.candidate_accepted') : t('toast.candidate_rejected'), 'success');
        row.style.transition = 'opacity 0.3s';
        row.style.opacity = '0';
        setTimeout(() => row.remove(), 300);
    } catch (err) {
        showToast(t('common.error', { err: err.message }), 'error');
        btnAccept.disabled = false;
        btnReject.disabled = false;
        throw err;
    }
}

document.getElementById('ontology-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const sourceID = document.getElementById('ont-source-id').value.trim();
    const targetID = document.getElementById('ont-target-id').value.trim();
    const relationType = document.getElementById('ont-relation-type').value;
    const direction = document.getElementById('ont-direction').value;
    try {
        const resp = await fetch(API_BASE + '/api/ontology/relations/', {
            method: 'POST',
            headers: mutationHeaders(),
            body: JSON.stringify({
                source_id: sourceID,
                target_id: targetID,
                relation_type: relationType,
                direction
            })
        });
        if (!resp.ok) {
            if (resp.status === 401 || resp.status === 403) {
                showToast(t('token.need_token_toast'), 'error');
                const btnToken = document.getElementById('btn-token-status');
                if (btnToken) btnToken.click();
                return;
            }
            const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
            showToast(t('common.error', { err: err.error || 'unknown' }), 'error');
            return;
        }
        showToast(sourceID + ' → ' + targetID + ' (' + relationType + ')', 'success');
        document.getElementById('ontology-form').reset();
        ontologyLoaded = false;
        await loadOntology();
    } catch (err) {
        showToast(t('common.network_error', { err: err.message }), 'error');
    }
});

// ══════════════════════════════════════════════════════════════════════
// ── Rules tab (P1#14) ────────────────────────────────────────────────
// ══════════════════════════════════════════════════════════════════════

async function loadRules() {
    const list = document.getElementById('rules-list');
    const summary = document.getElementById('rules-summary');
    const btn = document.getElementById('btn-refresh-rules');
    btn.disabled = true;
    btn.textContent = t('rules.loading');
    list.innerHTML = '<div class="empty">' + esc(t('rules.loading')) + '</div>';
    try {
        const resp = await fetch(API_BASE + '/api/rules');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const rules = await resp.json();
        const arr = Array.isArray(rules) ? rules : (rules.rules || []);
        cachedRules = arr;
        renderRules(arr);
    } catch (err) {
        list.innerHTML = '<div class="empty">' + esc(t('common.error', { err: err.message })) + '</div>';
        summary.textContent = '';
    } finally {
        btn.disabled = false;
        btn.textContent = t('rules.refresh');
    }
}

function renderRules(rules) {
    const list = document.getElementById('rules-list');
    const summary = document.getElementById('rules-summary');

    const active = rules.filter(r => r.enabled !== false).length;
    const disabled = rules.length - active;
    summary.textContent = t('rules.summary', { active, disabled });

    if (!rules || rules.length === 0) {
        list.innerHTML = '<div class="empty">' + esc(t('rules.empty')) + '</div>';
        return;
    }

    list.textContent = '';
    const table = document.createElement('table');
    table.className = 'data-table';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('rules.th_id'),
        t('rules.th_name'),
        t('rules.th_metric'),
        t('rules.th_detector'),
        t('rules.th_severity'),
        t('rules.th_config'),
        t('rules.th_enabled'),
        t('rules.th_version'),
        t('rules.th_actions')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        hRow.appendChild(th);
    });
    thead.appendChild(hRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const rule of rules) {
        const tr = document.createElement('tr');
        const id = rule.id || rule.rule_id || '';

        const tdId = document.createElement('td');
        tdId.textContent = String(id).substring(0, 10);
        tr.appendChild(tdId);

        const tdName = document.createElement('td');
        tdName.title = rule.name || '';
        const nameSpan = document.createElement('span');
        nameSpan.textContent = ruleDisplayName(rule);
        tdName.appendChild(nameSpan);
        if (String(rule.mode || 'live') === 'observe') {
            const badge = document.createElement('span');
            badge.className = 'mode-badge mode-observe';
            badge.textContent = t('rules.mode_observe');
            tdName.appendChild(badge);
        }
        tr.appendChild(tdName);

        const tdMetric = document.createElement('td');
        tdMetric.textContent = metricDisplayName(rule.metric_id);
        tdMetric.title = rule.metric_id || '';
        tr.appendChild(tdMetric);

        const tdDetector = document.createElement('td');
        tdDetector.textContent = rule.detector_name || '—';
        tr.appendChild(tdDetector);

        const tdSeverity = document.createElement('td');
        const sev = String(rule.severity || 'info').toLowerCase();
        const sevBadge = document.createElement('span');
        sevBadge.className = 'severity-badge severity-' + sev;
        sevBadge.textContent = (rule.severity || 'info').toUpperCase();
        tdSeverity.appendChild(sevBadge);
        tr.appendChild(tdSeverity);

        const tdConfig = document.createElement('td');
        tdConfig.style.cssText = 'font-family:monospace; font-size:0.75rem; color:#64ffda; max-width:240px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;';
        tdConfig.textContent = formatRuleConfig(rule.config);
        tdConfig.title = typeof rule.config === 'object' ? JSON.stringify(rule.config, null, 2) : String(rule.config);
        tr.appendChild(tdConfig);

        const tdEnabled = document.createElement('td');
        const enabled = rule.enabled !== false;
        const toggleLabel = document.createElement('label');
        toggleLabel.style.cssText = 'display:inline-flex; align-items:center; gap:0.3rem; cursor:pointer; font-size:0.75rem;';
        const toggleInput = document.createElement('input');
        toggleInput.type = 'checkbox';
        toggleInput.checked = enabled;
        const toggleTxt = document.createElement('span');
        toggleTxt.textContent = enabled ? t('rules.on') : t('rules.off');
        toggleInput.addEventListener('change', () => {
            toggleRule(id, toggleInput.checked, toggleInput, toggleTxt);
        });
        toggleLabel.appendChild(toggleInput);
        toggleLabel.appendChild(toggleTxt);
        tdEnabled.appendChild(toggleLabel);
        tr.appendChild(tdEnabled);

        const tdVersion = document.createElement('td');
        tdVersion.style.cssText = 'color:#8892b0; font-size:0.75rem;';
        tdVersion.textContent = 'v' + (rule.version || 1);
        tr.appendChild(tdVersion);

        const tdActions = document.createElement('td');
        tdActions.style.cssText = 'white-space:nowrap; display:flex; gap:0.3rem; align-items:center;';

        const btnEdit = document.createElement('button');
        btnEdit.type = 'button';
        btnEdit.textContent = t('rules.btn_edit');
        btnEdit.style.cssText = 'background:#1a2744; color:#64ffda; border:1px solid #64ffda; padding:0.2rem 0.5rem; border-radius:4px; font-size:0.72rem; cursor:pointer;';
        btnEdit.addEventListener('click', () => toggleRuleEdit(rule, tr, btnEdit));
        tdActions.appendChild(btnEdit);

        const btnHistory = document.createElement('button');
        btnHistory.type = 'button';
        btnHistory.textContent = t('rules.btn_history');
        btnHistory.style.cssText = 'background:#2a3450; color:#e0e6f0; border:1px solid #3b4d75; padding:0.2rem 0.5rem; border-radius:4px; font-size:0.72rem; cursor:pointer;';
        btnHistory.addEventListener('click', () => toggleRuleHistory(id, tr, btnHistory));
        tdActions.appendChild(btnHistory);

        tr.appendChild(tdActions);
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);

    const wrapper = document.createElement('div');
    wrapper.className = 'table-responsive';
    wrapper.appendChild(table);
    list.appendChild(wrapper);
}

function formatRuleConfig(cfg) {
    if (!cfg) return '—';
    if (typeof cfg === 'string') {
        try {
            cfg = JSON.parse(cfg);
        } catch (e) {
            try {
                // Fallback if backend returned base64-encoded JSON bytes
                cfg = JSON.parse(atob(cfg));
            } catch (e2) {
                return cfg;
            }
        }
    }
    if (typeof cfg !== 'object' || cfg === null) return String(cfg);

    const parts = [];
    if (cfg.operator && cfg.value !== undefined) {
        let op = cfg.operator;
        if (op === 'gt') op = '>';
        else if (op === 'gte') op = '≥';
        else if (op === 'lt') op = '<';
        else if (op === 'lte') op = '≤';
        else if (op === 'eq') op = '=';

        let valStr = cfg.value;
        if (typeof cfg.value === 'number') {
            if (cfg.value >= 1e8) {
                valStr = (cfg.value / 1e6).toLocaleString() + 'M';
            } else if (cfg.value >= 1e4) {
                valStr = cfg.value.toLocaleString();
            }
        }
        parts.push(op + ' ' + valStr);
    } else if (cfg.percentile !== undefined) {
        parts.push(t('rules.cfg_percentile', { p: cfg.percentile }));
    } else if (cfg.direction !== undefined) {
        let dir = cfg.direction;
        if (cfg.direction === 'down') dir = t('rules.cfg_dir_down');
        else if (cfg.direction === 'up') dir = t('rules.cfg_dir_up');
        parts.push(dir);
    } else if (cfg.threshold !== undefined) {
        parts.push(t('rules.cfg_threshold', { v: cfg.threshold }));
    }

    if (cfg.consecutive && cfg.consecutive > 1) {
        parts.push(t('rules.cfg_consecutive', { n: cfg.consecutive }));
    } else if (cfg.consecutive === 1 && parts.length === 1 && cfg.percentile !== undefined) {
        parts.push(t('rules.cfg_consecutive', { n: 1 }));
    }

    if (cfg.window_minutes) {
        parts.push(t('rules.cfg_window', { n: cfg.window_minutes }));
    }
    if (parts.length > 0) return parts.join(' · ');
    return JSON.stringify(cfg);
}

function toggleRuleEdit(rule, row, btn) {
    const existing = row.nextElementSibling;
    if (existing && existing.classList.contains('rule-edit-row')) {
        existing.remove();
        btn.textContent = t('rules.btn_edit');
        return;
    }

    let cfg = rule.config || {};
    if (typeof cfg === 'string') {
        try {
            cfg = JSON.parse(cfg);
        } catch (e) {
            try {
                cfg = JSON.parse(atob(cfg));
            } catch (e2) {
                cfg = {};
            }
        }
    }

    const editTr = document.createElement('tr');
    editTr.className = 'rule-edit-row';
    const editTd = document.createElement('td');
    editTd.colSpan = 9;
    editTd.style.cssText = 'background:#0d1220; padding:1rem;';

    const panel = document.createElement('div');
    panel.className = 'rule-edit-panel';

    const title = document.createElement('div');
    title.className = 'rule-edit-title';
    title.textContent = t('rules.edit_title') + ': ' + ruleDisplayName(rule);
    title.title = rule.name || '';
    panel.appendChild(title);

    const grid = document.createElement('div');
    grid.className = 'rule-edit-grid';

    // Severity select
    const grpSev = document.createElement('div');
    grpSev.className = 'rule-field-group';
    const lblSev = document.createElement('label');
    lblSev.className = 'rule-field-label';
    lblSev.textContent = t('rules.edit_severity');
    const selSev = document.createElement('select');
    selSev.className = 'rule-field-input';
    ['info', 'warning', 'critical'].forEach(s => {
        const opt = document.createElement('option');
        opt.value = s;
        opt.textContent = s;
        if (rule.severity === s) opt.selected = true;
        selSev.appendChild(opt);
    });
    grpSev.appendChild(lblSev);
    grpSev.appendChild(selSev);
    grid.appendChild(grpSev);

    // Operator select
    const grpOp = document.createElement('div');
    grpOp.className = 'rule-field-group';
    const lblOp = document.createElement('label');
    lblOp.className = 'rule-field-label';
    lblOp.textContent = t('rules.edit_operator');
    const selOp = document.createElement('select');
    selOp.className = 'rule-field-input';
    ['gt', 'gte', 'lt', 'lte', 'eq', 'neq'].forEach(op => {
        const opt = document.createElement('option');
        opt.value = op;
        opt.textContent = op;
        if ((cfg.operator || 'gt') === op) opt.selected = true;
        selOp.appendChild(opt);
    });
    grpOp.appendChild(lblOp);
    grpOp.appendChild(selOp);
    grid.appendChild(grpOp);

    // Value input
    const grpVal = document.createElement('div');
    grpVal.className = 'rule-field-group';
    const lblVal = document.createElement('label');
    lblVal.className = 'rule-field-label';
    lblVal.textContent = t('rules.edit_value');
    const inputVal = document.createElement('input');
    inputVal.type = 'number';
    inputVal.step = 'any';
    inputVal.className = 'rule-field-input';
    inputVal.value = cfg.value !== undefined ? cfg.value : (cfg.threshold !== undefined ? cfg.threshold : '');
    grpVal.appendChild(lblVal);
    grpVal.appendChild(inputVal);
    grid.appendChild(grpVal);

    // Consecutive input
    const grpCons = document.createElement('div');
    grpCons.className = 'rule-field-group';
    const lblCons = document.createElement('label');
    lblCons.className = 'rule-field-label';
    lblCons.textContent = t('rules.edit_consecutive');
    const inputCons = document.createElement('input');
    inputCons.type = 'number';
    inputCons.min = '1';
    inputCons.step = '1';
    inputCons.className = 'rule-field-input';
    inputCons.value = cfg.consecutive !== undefined ? cfg.consecutive : 1;
    grpCons.appendChild(lblCons);
    grpCons.appendChild(inputCons);
    grid.appendChild(grpCons);

    panel.appendChild(grid);

    // Advanced JSON config
    const grpJson = document.createElement('div');
    grpJson.className = 'rule-field-group';
    const lblJson = document.createElement('label');
    lblJson.className = 'rule-field-label';
    lblJson.textContent = t('rules.edit_json');
    const txtJson = document.createElement('textarea');
    txtJson.className = 'rule-json-textarea';
    txtJson.value = JSON.stringify(cfg, null, 2);

    function syncInputsToJson() {
        try {
            let cur = {};
            try { cur = JSON.parse(txtJson.value); } catch (e) { cur = {}; }
            cur.operator = selOp.value;
            const valNum = parseFloat(inputVal.value);
            if (!isNaN(valNum)) cur.value = valNum;
            const consNum = parseInt(inputCons.value, 10);
            if (!isNaN(consNum) && consNum > 0) cur.consecutive = consNum;
            txtJson.value = JSON.stringify(cur, null, 2);
        } catch (e) { /* ignore */ }
    }

    selOp.addEventListener('change', syncInputsToJson);
    inputVal.addEventListener('input', syncInputsToJson);
    inputCons.addEventListener('input', syncInputsToJson);

    grpJson.appendChild(lblJson);
    grpJson.appendChild(txtJson);
    panel.appendChild(grpJson);

    // Actions
    const actBar = document.createElement('div');
    actBar.className = 'rule-edit-actions';

    const btnCancel = document.createElement('button');
    btnCancel.type = 'button';
    btnCancel.className = 'alert-action-btn';
    btnCancel.textContent = t('rules.btn_cancel');
    btnCancel.addEventListener('click', () => {
        editTr.remove();
        btn.textContent = t('rules.btn_edit');
    });
    actBar.appendChild(btnCancel);

    const btnSave = document.createElement('button');
    btnSave.type = 'button';
    btnSave.className = 'alert-action-btn btn-reactivate';
    btnSave.textContent = t('rules.btn_save');
    btnSave.addEventListener('click', async () => {
        let parsedConfig = null;
        try {
            parsedConfig = JSON.parse(txtJson.value);
            if (typeof parsedConfig !== 'object' || parsedConfig === null || Array.isArray(parsedConfig)) {
                throw new Error(t('rules.invalid_json'));
            }
        } catch (e) {
            showToast(t('rules.invalid_json'), 'error');
            return;
        }

        btnSave.disabled = true;
        try {
            const resp = await fetch(API_BASE + '/api/rules/' + encodeURIComponent(rule.id), {
                method: 'PATCH',
                headers: mutationHeaders(),
                body: JSON.stringify({
                    severity: selSev.value,
                    config: parsedConfig
                })
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
            showToast(t('toast.rule_config_saved'), 'success');
            editTr.remove();
            btn.textContent = t('rules.btn_edit');
            await loadRules();
        } catch (err) {
            showToast(t('common.error', { err: err.message }), 'error');
        } finally {
            btnSave.disabled = false;
        }
    });
    actBar.appendChild(btnSave);

    panel.appendChild(actBar);
    editTd.appendChild(panel);
    editTr.appendChild(editTd);
    row.after(editTr);
    btn.textContent = t('rules.btn_hide');
}

async function toggleRule(ruleId, enabled, checkbox, labelSpan) {
    checkbox.disabled = true;
    try {
        const resp = await fetch(API_BASE + '/api/rules/' + encodeURIComponent(ruleId), {
            method: 'PATCH',
            headers: mutationHeaders(),
            body: JSON.stringify({ enabled })
        });
        if (!resp.ok) {
            if (resp.status === 401 || resp.status === 403) {
                showToast(t('token.need_token_toast'), 'error');
                checkbox.checked = !enabled;
                const btnToken = document.getElementById('btn-token-status');
                if (btnToken) btnToken.click();
                return;
            }
            const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
            throw new Error(err.error || ('HTTP ' + resp.status));
        }
        if (labelSpan) labelSpan.textContent = enabled ? t('rules.on') : t('rules.off');
        showToast(enabled ? t('toast.rule_enabled') : t('toast.rule_disabled'), 'success');
    } catch (err) {
        showToast(t('rules.toggle_error', { err: err.message }), 'error');
        checkbox.checked = !enabled;
        if (labelSpan) labelSpan.textContent = !enabled ? t('rules.on') : t('rules.off');
    } finally {
        checkbox.disabled = false;
    }
}

async function toggleRuleHistory(ruleId, row, btn) {
    const existing = row.nextElementSibling;
    if (existing && existing.classList.contains('rule-history-row')) {
        existing.remove();
        btn.textContent = t('rules.btn_history');
        return;
    }

    const historyTr = document.createElement('tr');
    historyTr.className = 'rule-history-row';
    const historyTd = document.createElement('td');
    historyTd.colSpan = 9;
    historyTd.style.cssText = 'background:#0d1220; padding:1rem;';

    const loadingP = document.createElement('p');
    loadingP.style.cssText = 'font-size:0.78rem; color:#8892b0;';
    loadingP.textContent = t('rules.loading_history');
    historyTd.appendChild(loadingP);
    historyTr.appendChild(historyTd);
    row.after(historyTr);
    btn.textContent = t('rules.btn_hide');

    try {
        const resp = await fetch(API_BASE + '/api/rules/' + encodeURIComponent(ruleId) + '/history');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const data = await resp.json();
        const entries = Array.isArray(data) ? data : (data.history || data.entries || []);
        renderRuleHistory(entries, historyTd, ruleId);
    } catch (err) {
        loadingP.textContent = t('rules.history_error', { err: err.message });
        loadingP.style.color = '#e85a5a';
    }
}

function renderRuleHistory(entries, container, ruleId) {
    container.textContent = '';
    const h = document.createElement('h4');
    h.style.cssText = 'color:#64ffda; margin-bottom:0.5rem; font-size:0.85rem;';
    h.textContent = t('rules.history_title', { n: entries.length });
    container.appendChild(h);

    if (!entries || entries.length === 0) {
        const p = document.createElement('p');
        p.style.cssText = 'font-size:0.78rem; color:#4a5570;';
        p.textContent = t('rules.no_history');
        container.appendChild(p);
        return;
    }

    const wrapper = document.createElement('div');
    wrapper.className = 'table-responsive';

    const table = document.createElement('table');
    table.className = 'data-table';
    table.style.fontSize = '0.75rem';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('rules.th_hist_version'),
        t('rules.th_hist_window'),
        t('rules.th_hist_severity'),
        t('rules.th_hist_config'),
        t('rules.th_hist_status'),
        t('rules.th_hist_details'),
        t('rules.btn_restore')
    ].forEach(txt => {
        const th = document.createElement('th');
        th.textContent = txt;
        hRow.appendChild(th);
    });
    thead.appendChild(hRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const e of entries) {
        const tr = document.createElement('tr');
        const isCurrent = !e.effective_to;

        // 1. Version
        const tdVer = document.createElement('td');
        tdVer.style.whiteSpace = 'nowrap';
        const verSpan = document.createElement('span');
        verSpan.style.cssText = 'font-weight:600; color:#e0e6f0;';
        verSpan.textContent = 'v' + (e.version || 1);
        tdVer.appendChild(verSpan);
        if (isCurrent) {
            const curBadge = document.createElement('span');
            curBadge.style.cssText = 'background:rgba(100,255,218,0.15); color:#64ffda; border:1px solid rgba(100,255,218,0.35); padding:0.1rem 0.35rem; border-radius:3px; font-size:0.68rem; margin-left:0.35rem;';
            curBadge.textContent = t('rules.hist_current');
            tdVer.appendChild(curBadge);
        }
        tr.appendChild(tdVer);

        // 2. Effective window
        const tdWindow = document.createElement('td');
        tdWindow.style.cssText = 'color:#8892b0; white-space:nowrap; font-size:0.72rem;';
        const fromTime = e.effective_from || e.created_at;
        const fromStr = fromTime ? new Date(fromTime).toLocaleString() : '—';
        const toStr = e.effective_to ? new Date(e.effective_to).toLocaleString() : t('rules.hist_active');
        tdWindow.textContent = fromStr + ' ~ ' + toStr;
        tr.appendChild(tdWindow);

        // 3. Severity
        const tdSev = document.createElement('td');
        if (e.severity) {
            const sev = String(e.severity).toLowerCase();
            const sevBadge = document.createElement('span');
            sevBadge.className = 'severity-badge severity-' + sev;
            sevBadge.textContent = e.severity.toUpperCase();
            tdSev.appendChild(sevBadge);
        } else {
            tdSev.textContent = '—';
        }
        tr.appendChild(tdSev);

        // 4. Config
        const tdCfg = document.createElement('td');
        tdCfg.style.cssText = 'font-family:monospace; font-size:0.72rem; color:#64ffda; max-width:220px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;';
        const cfgVal = e.config || e.new_value;
        tdCfg.textContent = formatRuleConfig(cfgVal);
        tdCfg.title = typeof cfgVal === 'object' ? JSON.stringify(cfgVal, null, 2) : String(cfgVal || '');
        tr.appendChild(tdCfg);

        // 5. Status
        const tdEnabled = document.createElement('td');
        tdEnabled.style.whiteSpace = 'nowrap';
        tdEnabled.textContent = e.enabled !== false ? t('rules.on') : t('rules.off');
        tdEnabled.style.color = e.enabled !== false ? '#2dd4a7' : '#8892b0';
        tr.appendChild(tdEnabled);

        // 6. Details / Actor
        const tdDetails = document.createElement('td');
        tdDetails.style.maxWidth = '260px';
        tdDetails.style.overflow = 'hidden';
        tdDetails.style.textOverflow = 'ellipsis';
        tdDetails.style.whiteSpace = 'nowrap';
        const actorStr = e.actor || 'system';
        if (e.field === 'restore_version') {
            tdDetails.textContent = t('rules.hist_restore_action') + ' v' + (e.new_value ?? '') + ' (' + actorStr + ')';
        } else if (e.field === 'initial_version') {
            tdDetails.textContent = t('rules.hist_initial') + ' (' + actorStr + ')';
        } else if (e.field === 'version_created') {
            tdDetails.textContent = t('rules.hist_created') + ' (' + actorStr + ')';
        } else if (e.old_value !== undefined && e.new_value !== undefined) {
            tdDetails.textContent = e.field + ': ' + JSON.stringify(e.old_value) + ' → ' + JSON.stringify(e.new_value) + ' (' + actorStr + ')';
        } else {
            tdDetails.textContent = (e.field || '—') + ' (' + actorStr + ')';
        }
        tdDetails.title = tdDetails.textContent;
        tr.appendChild(tdDetails);

        // 7. Actions (Restore)
        const restoreTd = document.createElement('td');
        restoreTd.style.whiteSpace = 'nowrap';
        if (e.version && !isCurrent) {
            const btnRestore = document.createElement('button');
            btnRestore.textContent = t('rules.btn_restore');
            btnRestore.style.cssText = 'background:#1a2744; color:#64ffda; border:1px solid #64ffda; padding:0.18rem 0.5rem; border-radius:4px; font-size:0.7rem; cursor:pointer;';
            btnRestore.addEventListener('click', () => {
                if (confirm(t('rules.confirm_restore', { id: ruleId, version: e.version, from: 'current', to: 'v' + e.version }))) {
                    restoreRule(ruleId, e.version, btnRestore, 'current', 'v' + e.version);
                }
            });
            restoreTd.appendChild(btnRestore);
        } else if (isCurrent) {
            const curTxt = document.createElement('span');
            curTxt.style.cssText = 'color:#64ffda; font-size:0.72rem;';
            curTxt.textContent = '✓ ' + t('rules.hist_current');
            restoreTd.appendChild(curTxt);
        }
        tr.appendChild(restoreTd);

        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrapper.appendChild(table);
    container.appendChild(wrapper);
}

async function restoreRule(ruleId, version, btn, from, to) {
    if (!ruleId || !version) return;
    btn.disabled = true;
    try {
        const resp = await fetch(API_BASE + '/api/rules/' + encodeURIComponent(ruleId) + '/restore/' + encodeURIComponent(version), {
            method: 'POST',
            headers: mutationHeaders()
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
        showToast(t('toast.rule_restored', { version }), 'success');
        await loadRules();
    } catch (err) {
        showToast(t('common.error', { err: err.message }), 'error');
    } finally {
        btn.disabled = false;
    }
}
