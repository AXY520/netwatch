window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

function detectProxyState() {
    var ci = (state.summary && state.summary.website_connectivity) || {};
    var globalSites = (ci.global || []).filter(function (s) { return s && s.status; });
    if (globalSites.length === 0) {
        return { mode: 'unknown', label: i18n('unknown_status') };
    }
    var okCount = globalSites.filter(function (s) { return s.status === 'ok'; }).length;
    var total = globalSites.length;
    var glb = (state.egressData && state.egressData.lookups || []).find(function (l) { return l.scope === 'global' && l.ip; });
    var domesticV4 = state.egressData && state.egressData.domestic_ip && state.egressData.domestic_ip.ipv4;
    var inChina = function (entry) {
        if (!entry) return false;
        var c = (entry.country || '') + (entry.region || '') + (entry.location || '');
        return c.indexOf('\u4E2D\u56FD') !== -1 || c.indexOf('China') !== -1 || c.indexOf('CN') !== -1;
    };
    var boxInChina = inChina(domesticV4) || inChina(glb);
    if (okCount === total) {
        if (boxInChina) return { mode: 'proxy', label: i18n('proxy_detected') };
        return { mode: 'direct', label: i18n('global_egress_detected') };
    }
    if (okCount === 0) return { mode: 'direct', label: i18n('no_proxy') };
    return { mode: 'partial', label: i18n('unknown_status') };
}

function renderProxyBanner() {
    var inlineEl = document.getElementById('proxy-inline-status');
    var s = detectProxyState();
    if (inlineEl) inlineEl.textContent = s.label;
}

function refreshProxyDisplay() {
    renderProxyBanner();
    if (state.summary && state.summary.website_connectivity) {
        window.__app.updateConnectivityTable(els.domesticTable, state.summary.website_connectivity.domestic || []);
        window.__app.updateConnectivityTable(els.globalTable, state.summary.website_connectivity.global || []);
    }
}

function renderSummary(summary) {
    state.summary = summary;
    state.refreshInterval = summary.refresh_interval_sec || 10;
    state.settings.refresh_interval_sec = state.refreshInterval;
    state.lastRefreshTime = Date.now();
    if (els.websiteStatus) els.websiteStatus.textContent = '';
    window.__app.updateConnectivityTable(els.domesticTable, (summary.website_connectivity && summary.website_connectivity.domestic) || []);
    window.__app.updateConnectivityTable(els.globalTable, (summary.website_connectivity && summary.website_connectivity.global) || []);
    window.__app.renderNetworkInfo(summary.network_info || {});
    refreshProxyDisplay();
}

async function loadSummary(showOverlay, refresh) {
    if (showOverlay === undefined) showOverlay = false;
    if (refresh === undefined) refresh = false;
    if (showOverlay) els.overlay.style.display = 'flex';
    try {
        var url = refresh ? '/api/v1/probe/run' : '/api/v1/summary';
        var method = refresh ? 'POST' : 'GET';
        var response = await fetch(url, { method: method, cache: 'no-store' });
        if (!response.ok) throw new Error('HTTP ' + response.status);
        var data = await response.json();
        renderSummary(data);
    } catch (error) {
        console.error(error);
        NetwatchShared.showToast(i18n('load_failed') + ': ' + error.message, 'error');
    } finally {
        if (showOverlay) els.overlay.style.display = 'none';
    }
}

async function runFastRefresh(showOverlay) {
    if (showOverlay === undefined) showOverlay = true;
    if (state.fastRefreshing) return;
    state.fastRefreshing = true;
    els.refreshBtn.disabled = true;
    if (showOverlay) els.overlay.style.display = 'flex';
    try {
        var response = await fetch('/api/v1/probe/run', { method: 'POST' });
        var data = await response.json();
        renderSummary(data);
    } catch (error) {
        console.error(error);
        NetwatchShared.showToast(i18n('refresh_failed'), 'error');
    } finally {
        state.fastRefreshing = false;
        if (showOverlay) els.overlay.style.display = 'none';
        els.refreshBtn.disabled = false;
    }
}

async function runWebsiteRefresh() {
    els.websiteRefreshBtn.disabled = true;
    els.websiteStatus.textContent = i18n('checking') + '...';
    try {
        var response = await fetch('/api/v1/connectivity/websites/run', { method: 'POST' });
        if (!response.ok) throw new Error('HTTP ' + response.status);
        var websiteData = await response.json();
        window.__app.updateConnectivityTable(els.domesticTable, websiteData.domestic || []);
        window.__app.updateConnectivityTable(els.globalTable, websiteData.global || []);
        els.websiteStatus.textContent = '';
        if (state.summary) {
            state.summary.website_connectivity = websiteData;
        }
    } catch (error) {
        console.error(error);
        els.websiteStatus.textContent = i18n('check_failed');
        NetwatchShared.showToast(i18n('speedtest_failed'), 'error');
    } finally {
        els.websiteRefreshBtn.disabled = false;
    }
}

function renderEgressLookups(data) {
    state.egressData = data;
    var domesticEl = document.getElementById('egress-domestic-list');
    var globalEl = document.getElementById('egress-global-list');
    var statusEl = document.getElementById('egress-status');
    if (!globalEl) return;
    var itemHTML = function (lu, options) {
        options = options || {};
        if (lu.error) {
            return '<div class="egress-item error"><span class="egress-duration">' + (Number.isFinite(lu.duration_ms) && lu.duration_ms > 0 ? lu.duration_ms + ' ms' : '') + '</span><span class="egress-meta">' + i18n('error') + ': ' + NetwatchShared.escapeHtml(lu.error) + '</span></div>';
        }
        var geoParts = [['country', lu.country], ['region', lu.region], ['city', lu.city]].filter(function (p) { return p[1]; }).map(function (p) { return options.translateGeo ? window.__app.translateGeoName(p[1], p[0]) : p[1]; });
        var geo = geoParts.join(' \u00B7 ');
        var meta = [geo, lu.asn, lu.isp].filter(Boolean).join('  ');
        return '<div class="egress-item"><span class="egress-ip">' + NetwatchShared.escapeHtml(lu.ip || '--') + '</span><span class="egress-duration">' + (Number.isFinite(lu.duration_ms) && lu.duration_ms > 0 ? lu.duration_ms + ' ms' : '') + '</span><span class="egress-meta">' + NetwatchShared.escapeHtml(meta || '\u2014') + '</span></div>';
    };
    var lookup = (data.lookups || []).find(function (x) { return x.scope === 'global' && (x.ip || x.error); });
    var domestic = (data.domestic_ip && data.domestic_ip.ipv4) || {};
    if (domesticEl) {
        domesticEl.innerHTML = domestic.ip ? itemHTML({ provider: domestic.source || 'domestic', ip: domestic.ip, duration_ms: 0, country: '', region: domestic.location, city: '', isp: domestic.isp, error: domestic.error }) : '<div class="egress-item"><small>' + i18n('no_data') + '</small></div>';
    }
    globalEl.innerHTML = lookup ? itemHTML(lookup, { translateGeo: true }) : '<div class="egress-item"><small>' + i18n('no_data') + '</small></div>';
    if (statusEl) statusEl.textContent = data.generated_at ? i18n('queried_at') + ' ' + data.generated_at : i18n('waiting');
    renderDomesticIPSnapshot(data.domestic_ip || {});
    refreshProxyDisplay();
}

function setIdentityBadge(el, text, mode) {
    if (!el) return;
    el.textContent = text;
    el.classList.remove('ok', 'warn', 'fail');
    if (mode) el.classList.add(mode);
}

function renderDomesticIPSnapshot(snapshot) {
    var v4 = snapshot.ipv4 || {};
    var v6 = snapshot.ipv6 || {};
    var ipv4IP = document.getElementById('domestic-ipv4-ip');
    var ipv4Location = document.getElementById('domestic-ipv4-location');
    var ipv6IP = document.getElementById('domestic-ipv6-ip');
    var ipv6Location = document.getElementById('domestic-ipv6-location');
    if (ipv4IP) ipv4IP.textContent = v4.ip || i18n('not_detected');
    if (ipv4Location) ipv4Location.textContent = v4.error || v4.location || i18n('waiting');
    if (ipv6IP) ipv6IP.textContent = v6.ip || i18n('not_detected');
    if (ipv6Location) ipv6Location.textContent = v6.error || v6.location || i18n('waiting');
    state.ipv6Avail = snapshot.ipv6_availability || {};
    if (els.ipv6DetailWindow && els.ipv6DetailWindow.classList.contains('active')) {
        renderIPv6DetailWindow(state.ipv6Avail);
    }
}

var ipv6SummaryMap = {
    fully_usable: ['ipv6_fully_usable', 'ok'],
    outbound_only: ['ipv6_outbound_only', 'warn'],
    address_only: ['ipv6_address_only', 'fail'],
    no_global: ['ipv6_no_global', 'fail']
};

function setIPv6LayerDot(id, ok) {
    var el = document.getElementById(id);
    if (!el) return;
    el.classList.remove('ok', 'fail');
    el.classList.add(ok ? 'ok' : 'fail');
    el.textContent = ok ? '\u2713' : '\u2717';
}

function renderIPv6DetailWindow(avail) {
    avail = avail || {};
    var sm = ipv6SummaryMap[avail.summary] || ['ipv6_checking', 'warn'];
    setIdentityBadge(document.getElementById('ipv6-detail-summary'), i18n(sm[0]), sm[1]);
    setIPv6LayerDot('ipv6-detail-addr', avail.has_global_address);
    setIPv6LayerDot('ipv6-detail-outbound', avail.outbound_reachable);
    setIPv6LayerDot('ipv6-detail-https', avail.https_reachable);
    setIPv6LayerDot('ipv6-detail-dns', avail.dns_resolvable);
    var setVal = function (id, text) { var el = document.getElementById(id); if (el) el.textContent = text || ''; };
    setVal('ipv6-detail-addr-val', avail.global_address || i18n('not_detected'));
    setVal('ipv6-detail-outbound-val', avail.outbound_reachable ? (avail.outbound_target || '') + (avail.outbound_latency_ms ? ' \u00B7 ' + avail.outbound_latency_ms + ' ms' : '') : '');
    var checkedEl = document.getElementById('ipv6-detail-checked');
    if (checkedEl) {
        checkedEl.textContent = avail.checked_at ? i18n('ipv6_detail_checked_at') + ': ' + avail.checked_at : '';
    }
}

function openIPv6DetailWindow() {
    renderIPv6DetailWindow(state.ipv6Avail);
    if (els.ipv6DetailWindow) els.ipv6DetailWindow.classList.add('active');
    if (els.ipv6DetailBackdrop) els.ipv6DetailBackdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
}

function closeIPv6DetailWindow() {
    if (els.ipv6DetailWindow) els.ipv6DetailWindow.classList.remove('active');
    if (els.ipv6DetailBackdrop) els.ipv6DetailBackdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
}

async function openIPv6RenewWindow() {
    if (els.ipv6RenewWindow) els.ipv6RenewWindow.classList.add('active');
    if (els.ipv6RenewBackdrop) els.ipv6RenewBackdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
    await loadIPv6RenewNICs();
}

function closeIPv6RenewWindow() {
    if (els.ipv6RenewWindow) els.ipv6RenewWindow.classList.remove('active');
    if (els.ipv6RenewBackdrop) els.ipv6RenewBackdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
}

async function loadIPv6RenewNICs() {
    var selectEl = document.getElementById('ipv6-renew-nic-select');
    var statusEl = document.getElementById('ipv6-renew-status');
    var execBtn = document.getElementById('ipv6-renew-exec-btn');
    if (!selectEl) return;
    if (statusEl) statusEl.textContent = i18n('loading') + '...';
    selectEl.innerHTML = '';
    selectEl.disabled = false;
    if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
    if (execBtn) execBtn.disabled = true;
    try {
        var resp = await fetch('/api/v1/network/ipv6/renew-nics', { cache: 'no-store' });
        if (!resp.ok) {
            var err = await resp.json().catch(function () { return {}; });
            selectEl.disabled = true;
            if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
            if (statusEl) statusEl.textContent = err.error || i18n('ipv6_renew_unavailable');
            return;
        }
        var data = await resp.json();
        var nics = data.nics || [];
        if (nics.length === 0) {
            selectEl.disabled = true;
            if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
            if (statusEl) statusEl.textContent = i18n('ipv6_renew_no_nic');
            return;
        }
        selectEl.innerHTML = nics.map(function (n) {
            var label = n.device + ' \u00B7 ' + n.type + (n.connection ? ' \u00B7 ' + n.connection : '');
            return '<option value="' + NetwatchShared.escapeHtml(n.device) + '">' + NetwatchShared.escapeHtml(label) + '</option>';
        }).join('');
        selectEl.disabled = false;
        if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
        if (execBtn) execBtn.disabled = false;
        if (statusEl) statusEl.textContent = '';
    } catch (e) {
        selectEl.disabled = true;
        if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
        if (statusEl) statusEl.textContent = NetwatchShared.escapeHtml(e.message);
    }
}

async function runIPv6Renew() {
    var selectEl = document.getElementById('ipv6-renew-nic-select');
    var statusEl = document.getElementById('ipv6-renew-status');
    var outputEl = document.getElementById('ipv6-renew-output');
    var execBtn = document.getElementById('ipv6-renew-exec-btn');
    var device = selectEl ? selectEl.value : '';
    if (!device) {
        if (statusEl) statusEl.textContent = i18n('ipv6_renew_no_nic');
        return;
    }
    if (execBtn) execBtn.disabled = true;
    if (outputEl) outputEl.hidden = true;
    if (statusEl) statusEl.textContent = i18n('ipv6_renew_running') + ' ' + device + '...';
    try {
        var resp = await fetch('/api/v1/network/ipv6/renew', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ device: device })
        });
        var result = await resp.json();
        if (result.ok) {
            if (statusEl) statusEl.textContent = i18n('ipv6_renew_ok') + ': ' + device;
            if (outputEl && result.output) {
                outputEl.textContent = result.output;
                outputEl.hidden = false;
            }
            setTimeout(function () {
                fetch('/api/v1/network/egress-lookups', { method: 'POST', cache: 'no-store' })
                    .then(function (r) { return r.json(); }).then(renderEgressLookups).catch(function () {});
            }, 1500);
        } else {
            if (statusEl) statusEl.textContent = i18n('ipv6_renew_failed') + ': ' + (result.error || '');
        }
    } catch (e) {
        if (statusEl) statusEl.textContent = i18n('ipv6_renew_failed') + ': ' + e.message;
    } finally {
        if (execBtn) execBtn.disabled = false;
    }
}

function bindIPv6TitleEasterEgg() {
    var title = document.getElementById('domestic-ipv6-title');
    if (!title) return;
    var count = 0;
    var timer = null;
    title.addEventListener('click', function () {
        count++;
        if (timer) clearTimeout(timer);
        timer = setTimeout(function () { count = 0; }, 1500);
        if (count >= 5) {
            count = 0;
            if (timer) clearTimeout(timer);
            openIPv6RenewWindow();
        }
    });
}

async function initEgressLookups() {
    if (state.egressInitialized) return;
    state.egressInitialized = true;
    var btn = document.getElementById('egress-refresh-btn');
    if (!btn) return;
    var statusEl = document.getElementById('egress-status');
    var load = async function (force) {
        if (statusEl) statusEl.textContent = force ? i18n('querying') + '...' : i18n('loading') + '...';
        btn.disabled = true;
        try {
            var resp = await fetch('/api/v1/network/egress-lookups', {
                method: force ? 'POST' : 'GET',
                cache: 'no-store'
            });
            renderEgressLookups(await resp.json());
        } catch (e) {
            if (statusEl) statusEl.textContent = i18n('load_failed') + ': ' + e.message;
        } finally {
            btn.disabled = false;
        }
    };
    btn.addEventListener('click', function () { load(true); });
    await load(false);
}

function initAppTraffic() {
    if (state.appTrafficInitialized) return;
    state.appTrafficInitialized = true;
    var table = document.getElementById('app-traffic-table');
    var tbody = document.querySelector('#app-traffic-table tbody');
    var statusEl = document.getElementById('app-traffic-status');
    var noteEl = document.getElementById('app-traffic-note');
    var btn = document.getElementById('app-traffic-refresh-btn');
    var sortButtons = Array.from(document.querySelectorAll('#app-traffic-table [data-sort-key]'));
    var latestTrafficData = null;
    if (!tbody) return;
    var getAppTrafficName = function (item) {
        return String(item.app_title || item.app_id || item.project || item.bridge || '').toLowerCase();
    };
    var sortAppTrafficRows = function (a, b, key, direction) {
        var result = 0;
        if (key === 'app') {
            result = getAppTrafficName(a).localeCompare(getAppTrafficName(b), 'zh-CN');
        } else if (key === 'rx') {
            result = (a.rx_bytes || 0) - (b.rx_bytes || 0);
        } else if (key === 'tx') {
            result = (a.tx_bytes || 0) - (b.tx_bytes || 0);
        } else {
            result = ((a.rx_bytes || 0) + (a.tx_bytes || 0)) - ((b.rx_bytes || 0) + (b.tx_bytes || 0));
        }
        return direction === 'asc' ? result : -result;
    };
    var updateSortHeaders = function () {
        if (!table) return;
        var key = state.appTrafficSort.key;
        var direction = state.appTrafficSort.direction;
        sortButtons.forEach(function (button) {
            var active = button.dataset.sortKey === key;
            button.classList.toggle('active', active);
            button.dataset.sortDirection = active ? direction : 'none';
            button.setAttribute('aria-sort', active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
        });
    };
    var renderTraffic = function (data) {
        latestTrafficData = data;
        var list = Array.isArray(data.bridges) ? data.bridges.filter(function (b) { return !NetwatchShared.isNetwatchBridge(b); }) : [];
        updateSortHeaders();
        if (list.length === 0) {
            tbody.innerHTML = '<tr><td colspan="4" class="placeholder">' + i18n('no_app_data') + '</td></tr>';
        } else {
            var key = state.appTrafficSort.key;
            var direction = state.appTrafficSort.direction;
            list.sort(function (a, b) { return sortAppTrafficRows(a, b, key, direction); });
            tbody.innerHTML = list.map(function (b) {
                var total = (b.rx_bytes || 0) + (b.tx_bytes || 0);
                var title = NetwatchShared.escapeHtml(b.app_title || window.__app.shortAppName(b.app_id) || b.bridge);
                var iconHtml = b.icon ? '<img class="app-icon" src="' + NetwatchShared.escapeHtml(b.icon) + '" alt="" loading="lazy" onerror="this.style.display=\'none\'">' : '';
                var statusLine = b.status_text ? '<div class="app-status-text">' + NetwatchShared.escapeHtml(b.status_text) + '</div>' : '';
                var nameHtml;
                if (b.app_title) {
                    nameHtml = '<strong>' + NetwatchShared.escapeHtml(b.app_title) + '</strong>' + statusLine;
                } else if (b.app_id) {
                    nameHtml = '<strong>' + NetwatchShared.escapeHtml(window.__app.shortAppName(b.app_id)) + '</strong>' + statusLine + '<div class="app-status-text">' + NetwatchShared.escapeHtml(b.app_id) + '</div>';
                } else if (b.project) {
                    nameHtml = '<small style="color:var(--text-muted)">' + NetwatchShared.escapeHtml(b.project) + '</small>';
                } else {
                    nameHtml = '<small style="color:var(--text-muted)">' + NetwatchShared.escapeHtml(b.bridge) + '</small>';
                }
                return '<tr><td class="col-app"><div class="app-cell">' + iconHtml + '<div class="app-cell-info">' + nameHtml + '</div></div></td><td class="col-rx">' + NetwatchShared.formatBytes(b.rx_bytes || 0) + '</td><td class="col-tx">' + NetwatchShared.formatBytes(b.tx_bytes || 0) + '</td><td class="col-total">' + NetwatchShared.formatBytes(total) + '</td></tr>';
            }).join('');
        }
        if (statusEl) statusEl.textContent = data.generated_at ? i18n('sampled_at') + ' ' + data.generated_at : '';
        if (noteEl && data.note) noteEl.textContent = data.note;
    };
    var load = async function () {
        if (btn) btn.disabled = true;
        if (statusEl) statusEl.textContent = i18n('sampling') + '...';
        try {
            var resp = await fetch('/api/v1/network/app-traffic', { cache: 'no-store' });
            renderTraffic(await resp.json());
        } catch (e) {
            if (statusEl) statusEl.textContent = i18n('sampling_failed') + ': ' + e.message;
        } finally {
            if (btn) btn.disabled = false;
        }
    };
    sortButtons.forEach(function (button) {
        button.addEventListener('click', function () {
            var key = button.dataset.sortKey || 'total';
            if (state.appTrafficSort.key === key) {
                state.appTrafficSort.direction = state.appTrafficSort.direction === 'asc' ? 'desc' : 'asc';
            } else {
                state.appTrafficSort.key = key;
                state.appTrafficSort.direction = key === 'app' ? 'asc' : 'desc';
            }
            if (latestTrafficData) renderTraffic(latestTrafficData);
            else updateSortHeaders();
        });
    });
    updateSortHeaders();
    if (btn) btn.addEventListener('click', load);
    load();

    var settingsBtn = document.getElementById('traffic-settings-btn');
    var trafficSettingsWindow = document.getElementById('traffic-settings-window');
    var closeTrafficSettingsBtn = document.getElementById('close-traffic-settings-window');

    function populateTrafficSettings() {
        var el = function (id) { return document.getElementById(id); };
        if (el('ts-sampling-enabled')) el('ts-sampling-enabled').checked = !!state.settings.traffic_sampling_enabled;
        if (el('ts-global-interval')) {
            el('ts-global-interval').value = String(state.settings.traffic_sampling_interval_sec || 60);
            if (window.syncCustomSelect) window.syncCustomSelect(el('ts-global-interval'));
        }
        var perAppList = el('ts-per-app-list');
        if (perAppList && latestTrafficData && latestTrafficData.bridges) {
            var perApp = state.settings.per_app_sampling_interval || {};
            perAppList.innerHTML = latestTrafficData.bridges.filter(function (b) { return !NetwatchShared.isNetwatchBridge(b); }).map(function (b) {
                var title = b.app_title || window.__app.shortAppName(b.app_id) || b.bridge;
                var currentVal = perApp[b.bridge] || '';
                var options = [
                    { v: '', l: i18n('follow_global') },
                    { v: '10', l: '10 ' + i18n('seconds_short') },
                    { v: '30', l: '30 ' + i18n('seconds_short') },
                    { v: '60', l: '60 ' + i18n('seconds_short') },
                    { v: '120', l: '120 ' + i18n('seconds_short') },
                    { v: '300', l: '300 ' + i18n('seconds_short') }
                ].map(function (o) { return '<option value="' + o.v + '" ' + (String(currentVal) === o.v ? 'selected' : '') + '>' + o.l + '</option>'; }).join('');
                return '<div class="ts-per-app-item" data-bridge="' + b.bridge + '"><span class="ts-app-name" title="' + b.bridge + '">' + title + '</span><select class="ts-per-app-select">' + options + '</select></div>';
            }).join('');
            if (window.enhanceSelects) window.enhanceSelects(perAppList);
        }
    }

    function openTrafficSettings() {
        if (!trafficSettingsWindow) return;
        populateTrafficSettings();
        trafficSettingsWindow.classList.add('active');
        document.getElementById('window-backdrop').classList.add('active');
        NetwatchShared.lockModalScroll();
    }

    async function saveTrafficSettings() {
        var perApp = {};
        var perAppList = document.getElementById('ts-per-app-list');
        if (perAppList) {
            perAppList.querySelectorAll('.ts-per-app-item').forEach(function (item) {
                var bridge = item.dataset.bridge;
                var select = item.querySelector('.ts-per-app-select');
                if (select && select.value) {
                    perApp[bridge] = parseInt(select.value, 10);
                }
            });
        }
        var el = function (id) { return document.getElementById(id); };
        var payload = {
            refresh_interval_sec: state.settings.refresh_interval_sec,
            broadband_domestic_only: state.settings.broadband_domestic_only,
            nic_realtime_enabled: state.settings.nic_realtime_enabled,
            nic_realtime_interval_sec: state.settings.nic_realtime_interval_sec,
            chart_time_label_interval: state.settings.chart_time_label_interval,
            traffic_sampling_enabled: !!el('ts-sampling-enabled').checked,
            traffic_sampling_interval_sec: parseInt(el('ts-global-interval').value || '60', 10) || 60,
            per_app_sampling_interval: perApp,
            persistent_traffic_bridges: state.settings.persistent_traffic_bridges || [],
            background_monitor_enabled: state.settings.background_monitor_enabled,
            background_monitor_interval_sec: state.settings.background_monitor_interval_sec,
            notifications_enabled: state.settings.notifications_enabled,
            client_notification_enabled: state.settings.client_notification_enabled !== false,
            notify_abnormal_traffic: state.settings.notify_abnormal_traffic,
            notify_egress_change: state.settings.notify_egress_change,
            notify_connectivity_change: state.settings.notify_connectivity_change,
            notify_lan_device_change: state.settings.notify_lan_device_change,
            lan_device_offline_after_sec: state.settings.lan_device_offline_after_sec || 180,
            lan_device_online_after_sec: state.settings.lan_device_online_after_sec || 0,
            lan_device_offline_notify_delay_sec: state.settings.lan_device_offline_notify_delay_sec || 120,
            lan_device_online_notify_delay_sec: state.settings.lan_device_online_notify_delay_sec || 120,
            abnormal_traffic_threshold_mbps: state.settings.abnormal_traffic_threshold_mbps,
            bark_enabled: state.settings.bark_enabled,
            bark_server_url: state.settings.bark_server_url,
            bark_device_key: state.settings.bark_device_key,
            bark_group: state.settings.bark_group
        };
        try {
            var resp = await fetch('/api/v1/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            if (!resp.ok) throw new Error('save failed');
            var saved = await resp.json();
            state.settings = Object.assign({}, state.settings, saved);
            trafficSettingsWindow.classList.remove('active');
            document.getElementById('window-backdrop').classList.remove('active');
            NetwatchShared.unlockModalScroll();
            updateTrafficAnalysisLink();
            if (latestTrafficData) renderTraffic(latestTrafficData);
            NetwatchShared.showToast(i18n('traffic_settings_saved'), 'success');
        } catch (e) {
            NetwatchShared.showToast(i18n('save_settings_fail') + ': ' + e.message, 'error');
        }
    }

    if (settingsBtn) settingsBtn.addEventListener('click', openTrafficSettings);
    if (closeTrafficSettingsBtn) closeTrafficSettingsBtn.addEventListener('click', function () {
        if (trafficSettingsWindow) trafficSettingsWindow.classList.remove('active');
        document.getElementById('window-backdrop').classList.remove('active');
        NetwatchShared.unlockModalScroll();
    });
    var tsSaveBtn = document.getElementById('ts-save-btn');
    if (tsSaveBtn) tsSaveBtn.addEventListener('click', saveTrafficSettings);
}

function initNICRealtime() {
    if (state.nicRealtimeInitialized) return;
    state.nicRealtimeInitialized = true;
    var listEl = document.getElementById('nic-realtime-list');
    var statusEl = document.getElementById('nic-realtime-status');
    if (!listEl) return;
    window.renderNICRealtime = function (data) {
        if (!data.nics || data.nics.length === 0) {
            listEl.innerHTML = '<div class="nic-realtime-item"><small>' + i18n('no_monitored_nics') + '</small></div>';
            if (statusEl) statusEl.textContent = i18n('no_data');
            return;
        }
        listEl.innerHTML = data.nics.map(function (n) {
            var isUp = n.oper_state ? n.oper_state === 'up' : n.present;
            return '<div class="nic-realtime-item"><div class="nic-realtime-head"><span class="nic-realtime-name">' + NetwatchShared.escapeHtml(n.name) + '</span><span class="nic-realtime-badge ' + (isUp ? 'online' : 'offline') + '">' + (isUp ? 'UP' : 'DOWN') + '</span></div><div class="nic-realtime-rows"><div class="nic-realtime-cell"><span class="nic-realtime-label">\u2193 ' + i18n('rx') + '</span><span class="nic-realtime-value rx">' + window.__app.formatBitsPerSec(n.rx_bps) + '</span></div><div class="nic-realtime-cell"><span class="nic-realtime-label">\u2191 ' + i18n('tx') + '</span><span class="nic-realtime-value tx">' + window.__app.formatBitsPerSec(n.tx_bps) + '</span></div></div><div class="nic-realtime-total">' + i18n('cumulative') + ' \u2193 ' + NetwatchShared.formatBytes(n.rx_total) + ' / \u2191 ' + NetwatchShared.formatBytes(n.tx_total) + '</div></div>';
        }).join('');
        if (statusEl) statusEl.textContent = i18n('sampled_at') + ' ' + (data.timestamp || '');
    };
    var tick = async function (manual) {
        if (manual === undefined) manual = false;
        try {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = true;
            var resp = await fetch('/api/v1/network/realtime', { cache: 'no-store' });
            if (!resp.ok) return;
            window.renderNICRealtime(await resp.json());
        } catch (_) {
            if (statusEl) statusEl.textContent = i18n('sampling_failed');
        } finally {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = false;
        }
    };
    updateNICRealtimeRefreshButton();
    if (els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.addEventListener('click', function () { tick(true); });
    tick();
    if (window.__app.applySettingsToForm) window.__app.applySettingsToForm();
}

function initTrace() {
    if (state.traceInitialized) return;
    state.traceInitialized = true;
    var btn = document.getElementById('trace-run');
    var input = document.getElementById('trace-host');
    var out = document.getElementById('trace-output');
    var summary = document.getElementById('trace-summary');
    var detailsBtn = document.getElementById('trace-details-btn');
    if (!btn) return;
    var renderTraceSummary = function (items) {
        if (!summary) return;
        summary.innerHTML = items.map(function (item) {
            return '<div class="trace-summary-item"><span class="trace-summary-label">' + item.label + '</span><span class="trace-summary-value">' + item.value + '</span></div>';
        }).join('');
    };
    var renderTraceRows = function (data) {
        var hops = Array.isArray(data.hops) ? data.hops : [];
        if (hops.length === 0) {
            out.innerHTML = '<div class="trace-empty">' + i18n('trace_no_hops') + '</div>';
            return;
        }
        out.innerHTML = hops.map(function (h) {
            var primary = h.ip || '*';
            var secondary = h.location || (primary === '*' ? i18n('timeout') : i18n('geo_lookup_pending'));
            var timedOut = !h.ip && !h.host;
            var latency = timedOut ? i18n('timeout') : (Number.isFinite(h.latency_ms) && h.latency_ms > 0 ? h.latency_ms + ' ms' : '--');
            var latencyClass = timedOut ? 'fail' : (h.latency_ms > 200 ? 'warn' : '');
            return '<div class="trace-hop"><div class="trace-hop-host">' + NetwatchShared.escapeHtml(primary) + '</div><div class="trace-hop-ip">' + NetwatchShared.escapeHtml(secondary) + '</div><div class="trace-hop-latency ' + latencyClass + '">' + latency + '</div></div>';
        }).join('');
    };
    var run = async function () {
        var host = (input.value || '').trim();
        if (!host) return;
        btn.disabled = true;
        if (detailsBtn) detailsBtn.disabled = true;
        state.traceResult = null;
        window.__app.openTraceWindow();
        renderTraceSummary([
            { label: i18n('target'), value: host },
            { label: i18n('status_col'), value: i18n('tracing') },
            { label: i18n('tool'), value: 'mtr' }
        ]);
        out.innerHTML = '<div class="trace-empty">' + i18n('collecting_trace') + '</div>';
        try {
            await fetch('/api/v1/diagnostics/trace?host=' + encodeURIComponent(host), { method: 'POST', cache: 'no-store' });
            var poll = async function () {
                var resp = await fetch('/api/v1/diagnostics/trace/task', { cache: 'no-store' });
                var data = await resp.json();
                if (data.error) {
                    renderTraceSummary([
                        { label: i18n('target'), value: data.target || host },
                        { label: i18n('status_col'), value: i18n('failed') },
                        { label: i18n('tool'), value: data.tool || 'mtr' }
                    ]);
                    out.innerHTML = '<div class="trace-empty">' + i18n('error') + ': ' + data.error + '</div>';
                    if (state.tracePoller) {
                        clearInterval(state.tracePoller);
                        state.tracePoller = null;
                    }
                    return;
                }
                state.traceResult = data;
                renderTraceSummary([
                    { label: i18n('target'), value: data.target || host },
                    { label: i18n('status_col'), value: data.running ? i18n('tracing') : ((data.hops || []).length + ' ' + i18n('hops')) },
                    { label: i18n('tool'), value: data.tool || 'mtr' }
                ]);
                renderTraceRows(data);
                if (detailsBtn) detailsBtn.disabled = false;
                if (!data.running && state.tracePoller) {
                    clearInterval(state.tracePoller);
                    state.tracePoller = null;
                }
            };
            await poll();
            if (state.tracePoller) clearInterval(state.tracePoller);
            state.tracePoller = setInterval(poll, 1000);
        } catch (e) {
            renderTraceSummary([
                { label: i18n('target'), value: host },
                { label: i18n('status_col'), value: i18n('request_failed') },
                { label: i18n('tool'), value: 'mtr' }
            ]);
            out.innerHTML = '<div class="trace-empty">' + i18n('request_failed') + ': ' + e.message + '</div>';
        } finally {
            btn.disabled = false;
        }
    };
    btn.addEventListener('click', async function () {
        await run();
    });
    if (detailsBtn) detailsBtn.addEventListener('click', function () { window.__app.openTraceWindow(); });
    if (input) input.addEventListener('keydown', async function (event) {
        if (event.key === 'Enter') {
            event.preventDefault();
            await run();
        }
    });
}

function updateNICRealtimeRefreshButton() {
    if (!els.nicRealtimeRefreshBtn) return;
    els.nicRealtimeRefreshBtn.style.display = state.settings.nic_realtime_enabled ? 'none' : '';
}

function updateTrafficAnalysisLink() {
    var link = document.getElementById('traffic-analysis-link');
    if (!link) return;
    link.hidden = !state.settings.traffic_sampling_enabled;
}

window.__app.loadSummary = loadSummary;
window.__app.renderSummary = renderSummary;
window.__app.runFastRefresh = runFastRefresh;
window.__app.runWebsiteRefresh = runWebsiteRefresh;
window.__app.renderEgressLookups = renderEgressLookups;
window.__app.renderDomesticIPSnapshot = renderDomesticIPSnapshot;
window.__app.renderIPv6DetailWindow = renderIPv6DetailWindow;
window.__app.openIPv6DetailWindow = openIPv6DetailWindow;
window.__app.closeIPv6DetailWindow = closeIPv6DetailWindow;
window.__app.openIPv6RenewWindow = openIPv6RenewWindow;
window.__app.closeIPv6RenewWindow = closeIPv6RenewWindow;
window.__app.loadIPv6RenewNICs = loadIPv6RenewNICs;
window.__app.runIPv6Renew = runIPv6Renew;
window.__app.bindIPv6TitleEasterEgg = bindIPv6TitleEasterEgg;
window.__app.initEgressLookups = initEgressLookups;
window.__app.initAppTraffic = initAppTraffic;
window.__app.initNICRealtime = initNICRealtime;
window.__app.initTrace = initTrace;
window.__app.updateNICRealtimeRefreshButton = updateNICRealtimeRefreshButton;
window.__app.updateTrafficAnalysisLink = updateTrafficAnalysisLink;
})();
