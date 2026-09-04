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
        t('rules.th_scope'),
        t('rules.th_enabled'),
        t('rules.th_threshold'),
        t('rules.th_source'),
        t('rules.th_target'),
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
        tdName.textContent = rule.name || rule.slug || rule.rule_name || '—';
        tr.appendChild(tdName);

        const tdScope = document.createElement('td');
        tdScope.textContent = rule.scope || '—';
        tr.appendChild(tdScope);

        const tdEnabled = document.createElement('td');
        const enabled = rule.enabled !== false;
        const dot = document.createElement('span');
        dot.style.cssText = 'display:inline-block; width:10px; height:10px; border-radius:50%; background:' + (enabled ? '#2dd4a7' : '#e85a5a') + ';';
        tdEnabled.appendChild(dot);
        tr.appendChild(tdEnabled);

        const tdConf = document.createElement('td');
        tdConf.textContent = rule.confidence_threshold != null ? Number(rule.confidence_threshold).toFixed(2) : '—';
        tr.appendChild(tdConf);

        const tdSrc = document.createElement('td');
        tdSrc.textContent = rule.source_entity || rule.source || '—';
        tr.appendChild(tdSrc);

        const tdTgt = document.createElement('td');
        tdTgt.textContent = rule.target_entity || rule.target || '—';
        tr.appendChild(tdTgt);

        const tdActions = document.createElement('td');
        tdActions.style.cssText = 'white-space:nowrap;';

        const toggleLabel = document.createElement('label');
        toggleLabel.style.cssText = 'display:inline-flex; align-items:center; gap:0.3rem; cursor:pointer; margin-right:0.5rem; font-size:0.75rem;';
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
        tdActions.appendChild(toggleLabel);

        const btnHistory = document.createElement('button');
        btnHistory.textContent = t('rules.btn_history');
        btnHistory.style.cssText = 'background:#2a3450; color:#64ffda; border:1px solid #64ffda; padding:0.2rem 0.5rem; border-radius:4px; font-size:0.72rem; cursor:pointer;';
        btnHistory.addEventListener('click', () => toggleRuleHistory(id, tr, btnHistory));
        tdActions.appendChild(btnHistory);

        tr.appendChild(tdActions);
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    list.appendChild(table);
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
    historyTd.colSpan = 8;
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

    const table = document.createElement('table');
    table.className = 'data-table';
    table.style.fontSize = '0.75rem';
    const thead = document.createElement('thead');
    const hRow = document.createElement('tr');
    [
        t('rules.th_hist_time'),
        t('rules.th_hist_action'),
        t('rules.th_hist_actor'),
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
        const ts = document.createElement('td');
        ts.textContent = e.timestamp || e.created_at || e.time ? new Date(e.timestamp || e.created_at || e.time).toLocaleString() : '—';
        tr.appendChild(ts);
        const action = document.createElement('td');
        action.textContent = e.field || e.action || e.event || '—';
        tr.appendChild(action);
        const actor = document.createElement('td');
        actor.textContent = e.actor || e.user || '—';
        tr.appendChild(actor);
        const details = document.createElement('td');
        const oldValue = e.old_value !== undefined ? JSON.stringify(e.old_value) : '';
        const newValue = e.new_value !== undefined ? JSON.stringify(e.new_value) : '';
        details.textContent = oldValue || newValue ? oldValue + ' → ' + newValue : (e.details || e.changes || e.note || '—');
        details.style.maxWidth = '300px';
        details.style.overflow = 'hidden';
        details.style.textOverflow = 'ellipsis';
        tr.appendChild(details);
        const restoreTd = document.createElement('td');
        if (e.version) {
            const btnRestore = document.createElement('button');
            btnRestore.textContent = t('rules.btn_restore');
            btnRestore.style.cssText = 'background:#2a3450; color:#64ffda; border:1px solid #64ffda; padding:0.15rem 0.4rem; border-radius:4px; font-size:0.7rem; cursor:pointer;';
            btnRestore.addEventListener('click', () => restoreRule(ruleId, e.version, btnRestore, oldValue, newValue));
            restoreTd.appendChild(btnRestore);
        }
        tr.appendChild(restoreTd);
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    container.appendChild(table);
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
