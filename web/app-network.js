window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

function netwatchGet(path, query) {
    if (window.NetwatchAPI) return window.NetwatchAPI.get(path, query);
    var url = path;
    if (query && typeof query === 'object') {
        var params = new URLSearchParams();
        Object.keys(query).forEach(function (key) {
            var value = query[key];
            if (value === undefined || value === null || value === '') return;
            params.set(key, String(value));
        });
        var qs = params.toString();
        if (qs) url += (url.indexOf('?') >= 0 ? '&' : '?') + qs;
    }
    return fetch(url, { cache: 'no-store' }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (data) {
            if (!r.ok) {
                var err = new Error((data && data.error) || ('HTTP ' + r.status));
                err.status = r.status;
                err.payload = data;
                throw err;
            }
            return data;
        });
    });
}
function netwatchPost(path, body) {
    if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
    var init = { method: 'POST', cache: 'no-store' };
    if (body !== undefined) {
        init.headers = { 'Content-Type': 'application/json' };
        init.body = JSON.stringify(body);
    }
    return fetch(path, init).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (data) {
            if (!r.ok) {
                var err = new Error((data && data.error) || ('HTTP ' + r.status));
                err.status = r.status;
                err.payload = data;
                throw err;
            }
            return data;
        });
    });
}


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
        var country = String(entry.country || '').trim();
        var region = String(entry.region || entry.location || '').trim();
        var blob = country + ' ' + region;
        if (/中国|中國|China/i.test(blob)) return true;
        // ISO code only when it's a standalone country code field, not substring of ISP text.
        if (/^(CN|CHN)$/i.test(country)) return true;
        return false;
    };
    var hasMetaTun = false;
    try {
        var ifaces = (state.summary && state.summary.network_info && state.summary.network_info.interfaces) || [];
        hasMetaTun = ifaces.some(function (iface) {
            return iface && iface.present && iface.link_type === 'tun' &&
                (iface.device_status === 'connected' || iface.oper_state === 'up');
        });
    } catch (_) {}

    // Need egress geo to decide "proxy vs overseas direct"; otherwise stay unknown.
    var hasEgress = !!(domesticV4 && (domesticV4.ip || domesticV4.country || domesticV4.location)) || !!(glb && glb.ip);
    var boxInChina = inChina(domesticV4) || inChina(glb);

    if (okCount === total) {
        if (!hasEgress && !hasMetaTun) {
            return { mode: 'unknown', label: i18n('unknown_status') };
        }
        // Domestic geo + all foreign sites OK ⇒ system proxy/TUN is carrying global traffic.
        if (boxInChina || hasMetaTun) {
            return { mode: 'proxy', label: i18n('proxy_detected') };
        }
        return { mode: 'direct', label: i18n('global_egress_detected') };
    }
    if (okCount === 0) {
        // All blocked: no working proxy path (or network dead). Don't claim "proxy on".
        return { mode: 'direct', label: i18n('no_proxy') };
    }
    // Split routing / partial proxy.
    if (hasMetaTun) {
        return { mode: 'partial', label: i18n('proxy_detected') + ' · ' + i18n('unknown_status') };
    }
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

function stampOf(obj) {
    return (obj && obj.generated_at) ? String(obj.generated_at) : '';
}

// Merge incoming full summary with any fresher local partials (network_info /
// website_connectivity refreshed out-of-band). Returns null when the whole
// payload is strictly older than what the UI already shows.
function mergeIncomingSummary(incoming) {
    if (!incoming) return null;
    if (!state.summary) return incoming;
    var cur = state.summary;
    var curAt = stampOf(cur);
    var inAt = stampOf(incoming);
    if (curAt && inAt && inAt < curAt) {
        return null;
    }
    var out = Object.assign({}, incoming);
    var curNetAt = stampOf(cur.network_info);
    var inNetAt = stampOf(incoming.network_info);
    if (curNetAt && inNetAt && inNetAt < curNetAt) {
        out.network_info = cur.network_info;
    } else if (curNetAt && !inNetAt && cur.network_info) {
        out.network_info = cur.network_info;
    }
    var curWebAt = stampOf(cur.website_connectivity);
    var inWebAt = stampOf(incoming.website_connectivity);
    if (curWebAt && inWebAt && inWebAt < curWebAt) {
        out.website_connectivity = cur.website_connectivity;
    }
    return out;
}

function applyIncomingSummary(summary, opts) {
    opts = opts || {};
    var next = opts.force ? summary : mergeIncomingSummary(summary);
    if (!next) return false;
    renderSummary(next);
    return true;
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
    if (window.__app.renderNATInfo) window.__app.renderNATInfo((summary.network_info && summary.network_info.nat) || {});
    refreshProxyDisplay();
}


async function refreshNetworkDetailCards() {
    // Lightweight: re-collect interfaces for NIC detail table without full probe/run.
    try {
        var info = await netwatchPost('/api/v1/network/interfaces/refresh');
        if (info && window.__app && window.__app.renderNetworkInfo) {
            if (state.summary) {
                state.summary.network_info = info;
                // Keep summary clock in step so a concurrent older SSE cannot wipe this.
                if (info.generated_at && (!state.summary.generated_at || state.summary.generated_at < info.generated_at)) {
                    state.summary.generated_at = info.generated_at;
                }
            }
            window.__app.renderNetworkInfo(info);
        }
    } catch (err) {
        // Fallback to full summary if light path unavailable.
        try {
            if (window.__app && window.__app.refreshInterfacesOnly) {
                await window.__app.refreshInterfacesOnly();
            } else if (window.__app && window.__app.loadSummary) {
                await window.__app.loadSummary(false, true);
            }
        } catch (_) {}
    }
    try {
        if (typeof window.renderNICRealtime === 'function') {
            var rt = await netwatchGet('/api/v1/network/realtime?force=1');
            window.renderNICRealtime(rt);
        }
    } catch (_) {}
}

async function loadSummary(showOverlay, refresh) {
    if (showOverlay === undefined) showOverlay = false;
    if (refresh === undefined) refresh = false;
    if (showOverlay) els.overlay.style.display = 'flex';
    try {
        var data;
        if (window.NetwatchAPI) {
            data = refresh
                ? await window.NetwatchAPI.post('/api/v1/probe/run')
                : await window.NetwatchAPI.get('/api/v1/summary');
        } else {
            var url = refresh ? '/api/v1/probe/run' : '/api/v1/summary';
            var method = refresh ? 'POST' : 'GET';
            var response = await fetch(url, { method: method, cache: 'no-store' });
            if (!response.ok) throw new Error('HTTP ' + response.status);
            data = await response.json();
        }
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
        var data = window.NetwatchAPI
            ? await window.NetwatchAPI.post('/api/v1/probe/run')
            : await (await fetch('/api/v1/probe/run', { method: 'POST' })).json();
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

async function refreshInterfacesOnly() {
    if (!els.interfacesRefreshBtn || state.interfacesRefreshing) return;
    state.interfacesRefreshing = true;
    els.interfacesRefreshBtn.disabled = true;
    try {
        await refreshNetworkDetailCards();
    } catch (error) {
        console.error(error);
        NetwatchShared.showToast(i18n('refresh_failed') + ': ' + error.message, 'error');
    } finally {
        state.interfacesRefreshing = false;
        els.interfacesRefreshBtn.disabled = false;
    }
}

async function runWebsiteRefresh() {
    els.websiteRefreshBtn.disabled = true;
    els.websiteStatus.textContent = i18n('checking') + '...';
    try {
        var websiteData = window.NetwatchAPI
            ? await window.NetwatchAPI.post('/api/v1/connectivity/websites/run')
            : await (async function () {
                var response = await fetch('/api/v1/connectivity/websites/run', { method: 'POST' });
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return response.json();
            })();
        window.__app.updateConnectivityTable(els.domesticTable, websiteData.domestic || []);
        window.__app.updateConnectivityTable(els.globalTable, websiteData.global || []);
        els.websiteStatus.textContent = '';
        if (state.summary) {
            state.summary.website_connectivity = websiteData;
            if (websiteData.generated_at && (!state.summary.generated_at || state.summary.generated_at < websiteData.generated_at)) {
                state.summary.generated_at = websiteData.generated_at;
            }
        }
    } catch (error) {
        console.error(error);
        els.websiteStatus.textContent = i18n('check_failed');
        NetwatchShared.showToast(i18n('check_failed'), 'error');
    } finally {
        els.websiteRefreshBtn.disabled = false;
    }
}

async function runNATRefresh() {
    if (!els.natRefreshBtn) return;
    els.natRefreshBtn.disabled = true;
    if (els.natStatus) els.natStatus.textContent = i18n('checking') + '...';
    try {
        var nat = window.NetwatchAPI
            ? await window.NetwatchAPI.post('/api/v1/network/nat/run')
            : await (async function () {
                var response = await fetch('/api/v1/network/nat/run', { method: 'POST', cache: 'no-store' });
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return response.json();
            })();
        if (state.summary && state.summary.network_info) {
            state.summary.network_info.nat = nat;
        }
        if (window.__app.renderNATInfo) window.__app.renderNATInfo(nat);
    } catch (error) {
        console.error(error);
        if (els.natStatus) els.natStatus.textContent = i18n('check_failed');
        NetwatchShared.showToast(i18n('check_failed') + ': ' + error.message, 'error');
    } finally {
        els.natRefreshBtn.disabled = false;
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
    if (window.closeCustomSelects) window.closeCustomSelects();
    var win = document.getElementById('ipv6-detail-window') || (els && els.ipv6DetailWindow);
    var backdrop = document.getElementById('ipv6-detail-window-backdrop') || (els && els.ipv6DetailBackdrop);
    renderIPv6DetailWindow(state.ipv6Avail || {});
    if (!win) {
        console.warn('ipv6-detail-window missing');
        return;
    }
    // Ensure above any other floating layer / sticky headers.
    win.style.zIndex = '1300';
    if (backdrop) {
        backdrop.style.zIndex = '1290';
        backdrop.classList.add('active');
    }
    win.classList.add('active');
    NetwatchShared.lockModalScroll();
}

function closeIPv6DetailWindow() {
    if (window.closeCustomSelects) window.closeCustomSelects();
    var win = document.getElementById('ipv6-detail-window') || (els && els.ipv6DetailWindow);
    var backdrop = document.getElementById('ipv6-detail-window-backdrop') || (els && els.ipv6DetailBackdrop);
    if (win) win.classList.remove('active');
    if (backdrop) backdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
}

async function openIPv6RenewWindow() {
    if (window.closeCustomSelects) window.closeCustomSelects();
    var win = document.getElementById('ipv6-renew-window') || (els && els.ipv6RenewWindow);
    var backdrop = document.getElementById('ipv6-renew-window-backdrop') || (els && els.ipv6RenewBackdrop);
    if (!win) {
        console.warn('ipv6-renew-window missing');
        return;
    }
    win.style.zIndex = '1300';
    if (backdrop) {
        backdrop.style.zIndex = '1290';
        backdrop.classList.add('active');
    }
    win.classList.add('active');
    NetwatchShared.lockModalScroll();
    await loadIPv6RenewNICs();
}

function closeIPv6RenewWindow() {
    if (window.closeCustomSelects) window.closeCustomSelects();
    var win = document.getElementById('ipv6-renew-window') || (els && els.ipv6RenewWindow);
    var backdrop = document.getElementById('ipv6-renew-window-backdrop') || (els && els.ipv6RenewBackdrop);
    if (win) win.classList.remove('active');
    if (backdrop) backdrop.classList.remove('active');
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
        var data;
        try {
            data = await netwatchGet('/api/v1/network/ipv6/renew-nics');
        } catch (err) {
            selectEl.disabled = true;
            if (window.syncCustomSelect) window.syncCustomSelect(selectEl);
            if (statusEl) statusEl.textContent = (err && err.message) || i18n('ipv6_renew_unavailable');
            return;
        }
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
        var result = await netwatchPost('/api/v1/network/ipv6/renew', { device: device });
        if (result.ok) {
            var note = result.note || (i18n('ipv6_renew_ok') + ': ' + device);
            if (statusEl) statusEl.textContent = note;
            var detail = [];
            if (result.ipv6_before && result.ipv6_before.length) detail.push('before: ' + result.ipv6_before.join(', '));
            if (result.ipv6_after && result.ipv6_after.length) detail.push('after: ' + result.ipv6_after.join(', '));
            if (result.output) detail.push(result.output);
            if (outputEl) {
                if (detail.length) {
                    outputEl.textContent = detail.join('\n');
                    outputEl.hidden = false;
                } else {
                    outputEl.hidden = true;
                }
            }
            try { await refreshNetworkDetailCards(); } catch (_) {}
            setTimeout(function () {
                netwatchPost('/api/v1/network/egress-lookups')
                    .then(renderEgressLookups).catch(function () {});
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
            var egressData = force
                ? await netwatchPost('/api/v1/network/egress-lookups')
                : await netwatchGet('/api/v1/network/egress-lookups');
            renderEgressLookups(egressData);
        } catch (e) {
            if (statusEl) statusEl.textContent = i18n('load_failed') + ': ' + e.message;
        } finally {
            btn.disabled = false;
        }
    };
    btn.addEventListener('click', function () { load(true); });
    await load(false);
}


window.__app.netwatchGet = netwatchGet;
window.__app.netwatchPost = netwatchPost;
window.__app.loadSummary = loadSummary;
window.__app.refreshNetworkDetailCards = refreshNetworkDetailCards;
window.__app.renderSummary = renderSummary;
window.__app.applyIncomingSummary = applyIncomingSummary;
window.__app.mergeIncomingSummary = mergeIncomingSummary;
window.__app.runFastRefresh = runFastRefresh;
window.__app.refreshInterfacesOnly = refreshInterfacesOnly;
window.__app.runWebsiteRefresh = runWebsiteRefresh;
window.__app.runNATRefresh = runNATRefresh;
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
})();
