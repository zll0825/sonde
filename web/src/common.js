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

// ── Token Management (localStorage persistence) ──────────────────────
const TOKEN_STORAGE_KEY = 'capital_api_token';

function getApiToken() {
    try {
        return localStorage.getItem(TOKEN_STORAGE_KEY) || '';
    } catch (e) {
        return window.__cached_token || '';
    }
}

function setApiToken(token) {
    const trimmed = (token || '').trim();
    try {
        if (trimmed) {
            localStorage.setItem(TOKEN_STORAGE_KEY, trimmed);
        } else {
            localStorage.removeItem(TOKEN_STORAGE_KEY);
        }
    } catch (e) {
        window.__cached_token = trimmed;
    }
    updateTokenUI();
}

function clearApiToken() {
    setApiToken('');
}

function updateTokenUI() {
    const token = getApiToken();
    const btn = document.getElementById('btn-token-status');
    const label = document.getElementById('token-label');
    const input = document.getElementById('token-input');
    if (input && document.activeElement !== input) {
        input.value = token;
    }
    if (btn && label) {
        btn.classList.toggle('has-token', !!token);
        label.textContent = token ? t('header.token_configured') : t('header.token_missing');
    }
}

function mutationHeaders() {
    const headers = { 'Content-Type': 'application/json' };
    const token = getApiToken();
    if (token) {
        headers.Authorization = 'Bearer ' + token;
    }
    return headers;
}

// ── Lightweight Non-blocking Toast Notification ───────────────────────
function showToast(message, type = 'info', duration = 3200) {
    let container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.className = 'toast-container';
        document.body.appendChild(container);
    }

    const toast = document.createElement('div');
    toast.className = 'toast toast-' + type;

    const icon = document.createElement('span');
    icon.className = 'toast-icon';
    icon.textContent = type === 'success' ? '✓' : (type === 'warn' ? '⚠' : (type === 'error' ? '✕' : 'ℹ'));
    toast.appendChild(icon);

    const msg = document.createElement('span');
    msg.className = 'toast-msg';
    msg.textContent = message;
    toast.appendChild(msg);

    container.appendChild(toast);

    requestAnimationFrame(() => {
        toast.classList.add('show');
    });

    const removeTimer = setTimeout(() => {
        toast.classList.remove('show');
        setTimeout(() => toast.remove(), 250);
    }, duration);

    toast.addEventListener('click', () => {
        clearTimeout(removeTimer);
        toast.classList.remove('show');
        setTimeout(() => toast.remove(), 250);
    });
}
