// ── Tab switching ────────────────────────────────────────────────────
document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
        document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
        document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
        btn.classList.add('active');
        const tabId = btn.dataset.tab + '-tab';
        document.getElementById(tabId).classList.add('active');
        if (btn.dataset.tab === 'clusters') loadClusters();
        if (btn.dataset.tab === 'ontology') loadOntology();
        if (btn.dataset.tab === 'rules') loadRules();
    });
});

// ── Wire up button handlers & polling ────────────────────────────────
document.getElementById('btn-load-research').addEventListener('click', loadResearch);
document.getElementById('research-alert-id').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') loadResearch();
});
document.getElementById('btn-refresh-clusters').addEventListener('click', loadClusters);
document.getElementById('btn-discover-candidates').addEventListener('click', discoverCandidates);
document.getElementById('btn-refresh-rules').addEventListener('click', loadRules);
document.getElementById('sb-plugins').addEventListener('click', togglePluginDetail);

// Initialize i18n
initLanguage();

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
