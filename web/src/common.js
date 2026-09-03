const API_BASE = window.location.origin;
const SEVERITIES = ['critical', 'warning', 'info'];
let trendChart = null;

// Cache for dynamic data to re-render smoothly on language switch
let statusData = null;
let alertsList = null;
let cachedClusters = null;
let cachedRules = null;
let cachedOntology = null;
let cachedCandidates = null;
let cachedCandidatesUpdatedAt = null;
let cachedSignalPoints = null;
let cachedResearch = null;
let hasFetched = false;
let isOnline = false;


const FRESH_CLASSES = ['green', 'yellow', 'red'];

// Client-side relative time using i18n
function relativeTime(iso) {
    if (!iso) return null;
    const ms = Date.now() - Date.parse(iso);
    if (!isFinite(ms) || ms < 0) return t('time.just_now');
    const min = Math.floor(ms / 60000);
    if (min < 1) return t('time.within_1m');
    if (min < 60) return t('time.minutes_ago', { n: min });
    const hr = Math.floor(min / 60);
    if (hr < 24) return t('time.hours_ago', { n: hr });
    return t('time.days_ago', { n: Math.floor(hr / 24) });
}

function formatValue(v) {
    if (typeof v !== 'number' || !isFinite(v)) return '—';
    if (Math.abs(v) >= 1e9) return (v / 1e9).toFixed(2) + 'B';
    return v.toLocaleString('en-US', { maximumFractionDigits: 2 });
}

function esc(value) {
    return String(value).replace(/[&<>"']/g, c => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    })[c]);
}

function mutationHeaders() {
    const headers = { 'Content-Type': 'application/json' };
    const tokenInput = document.getElementById('ont-token');
    const token = tokenInput ? tokenInput.value.trim() : '';
    if (token) headers.Authorization = 'Bearer ' + token;
    return headers;
}
