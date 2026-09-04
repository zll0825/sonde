// ── Tab switching with deep-linking support ──────────────────────────
function switchTab(tabName, params = {}) {
    const targetBtn = document.querySelector(`.tab-btn[data-tab="${tabName}"]`);
    if (!targetBtn) return;

    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    targetBtn.classList.add('active');

    const tabId = tabName + '-tab';
    const contentEl = document.getElementById(tabId);
    if (contentEl) contentEl.classList.add('active');

    if (tabName === 'clusters') {
        loadClusters();
    } else if (tabName === 'ontology') {
        loadOntology();
    } else if (tabName === 'rules') {
        loadRules();
    } else if (tabName === 'research') {
        if (params.alertId) {
            const input = document.getElementById('research-alert-id');
            if (input) input.value = params.alertId;
            loadResearch();
        }
    } else if (tabName === 'signal') {
        if (params.metricUid) {
            const input = document.getElementById('signal-metric-uid');
            if (input) input.value = params.metricUid;
            loadSignalQuality();
        }
    } else if (tabName === 'alerts') {
        if (params.alertId) {
            showAlertDetail(params.alertId);
        }
    }

    window.scrollTo({ top: 0, behavior: 'smooth' });
}

document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
        switchTab(btn.dataset.tab);
    });
});

// ── Global Header Token controls ─────────────────────────────────────
const btnTokenStatus = document.getElementById('btn-token-status');
const tokenDropdown = document.getElementById('token-dropdown');
const tokenInput = document.getElementById('token-input');
const btnSaveToken = document.getElementById('btn-save-token');
const btnClearToken = document.getElementById('btn-clear-token');

if (btnTokenStatus && tokenDropdown) {
    btnTokenStatus.addEventListener('click', (e) => {
        e.stopPropagation();
        tokenDropdown.classList.toggle('hidden');
        if (!tokenDropdown.classList.contains('hidden') && tokenInput) {
            tokenInput.value = getApiToken();
            tokenInput.focus();
        }
    });

    tokenDropdown.addEventListener('click', (e) => {
        e.stopPropagation();
    });

    document.addEventListener('click', () => {
        if (!tokenDropdown.classList.contains('hidden')) {
            tokenDropdown.classList.add('hidden');
        }
    });
}

if (btnSaveToken && tokenInput) {
    btnSaveToken.addEventListener('click', () => {
        const val = tokenInput.value.trim();
        if (!val) {
            clearApiToken();
            showToast(t('token.cleared'), 'info');
        } else {
            setApiToken(val);
            showToast(t('token.saved'), 'success');
        }
        if (tokenDropdown) tokenDropdown.classList.add('hidden');
    });

    tokenInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
            btnSaveToken.click();
        } else if (e.key === 'Escape') {
            if (tokenDropdown) tokenDropdown.classList.add('hidden');
        }
    });
}

if (btnClearToken) {
    btnClearToken.addEventListener('click', () => {
        clearApiToken();
        if (tokenInput) tokenInput.value = '';
        showToast(t('token.cleared'), 'info');
        if (tokenDropdown) tokenDropdown.classList.add('hidden');
    });
}

// ── Alerts -> Deep Research button ───────────────────────────────────
const btnAlertToResearch = document.getElementById('btn-alert-to-research');
if (btnAlertToResearch) {
    btnAlertToResearch.addEventListener('click', () => {
        if (currentSelectedAlertId) {
            switchTab('research', { alertId: currentSelectedAlertId });
        }
    });
}

// ── Wire up button handlers & polling ────────────────────────────────
document.getElementById('btn-load-research').addEventListener('click', loadResearch);
document.getElementById('research-alert-id').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') loadResearch();
});
document.getElementById('btn-refresh-clusters').addEventListener('click', loadClusters);
document.getElementById('btn-discover-candidates').addEventListener('click', discoverCandidates);
document.getElementById('btn-refresh-rules').addEventListener('click', loadRules);
document.getElementById('sb-plugins').addEventListener('click', togglePluginDetail);

// Initialize i18n and token state
initLanguage();
updateTokenUI();

// Initial fetch
loadAlerts();
fetchStatus();
setInterval(() => { loadAlerts(); fetchStatus(); }, 30000);

// Responsive chart
window.addEventListener('resize', () => {
    if (trendChart) trendChart.resize();
    if (signalChart) signalChart.resize();
    if (researchChart) researchChart.resize();
});
