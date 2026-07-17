window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

function netwatchGet(path) {
    if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
    return fetch(path, { cache: 'no-store' }).then(function (r) {
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

function networkConfigEls() {
    return {
        device: document.getElementById('network-config-device'),
        method: document.getElementById('network-config-method'),
        address: document.getElementById('network-config-address'),
        gateway: document.getElementById('network-config-gateway'),
        dns: document.getElementById('network-config-dns'),
        preflight: document.getElementById('network-config-preflight-btn'),
        apply: document.getElementById('network-config-apply-btn'),
        confirm: document.getElementById('network-config-confirm-btn'),
        rollback: document.getElementById('network-config-rollback-btn'),
        status: document.getElementById('network-config-status'),
        output: document.getElementById('network-config-output')
        ,preview: document.getElementById('network-config-preview')
    };
}

function setNetworkConfigFormEnabled(enabled) {
    var e = networkConfigEls();
    var body = document.querySelector('.network-config-body');
    // is-empty greys CREATE forms only (CSS). Confirm/rollback/dissolve stay usable
    // when the only uplink NIC is enslaved into a pending bridge.
    if (body) body.classList.toggle('is-empty', !enabled);
    [e.device, e.method, e.address, e.gateway, e.dns, e.preflight, e.apply].forEach(function (el) {
        if (el) el.disabled = !enabled;
    });
    if (enabled) {
        updateNetworkConfigMethodState();
    } else {
        if (e.apply) e.apply.disabled = true;
        if (e.preflight) e.preflight.disabled = true;
        // Never hide confirm/rollback just because the device list is empty.
        // After bridge create the uplink NIC is enslaved and dropdown can be empty,
        // but pending confirm must remain usable after reconnect.
    }
    [e.device, e.method].forEach(function (select) {
        if (select && window.syncCustomSelect) window.syncCustomSelect(select);
    });
    // Bridge create needs a NIC; dissolve depends on existing managed bridges.
    setHostBridgeCreateEnabled(enabled);
}

function setHostBridgeCreateEnabled(enabled) {
    var e = typeof hostBridgeEls === 'function' ? hostBridgeEls() : {};
    if (!e.section) return;
    var pending = !!(hostBridgeState && hostBridgeState.pending && hostBridgeState.pending.pending);
    var canCreate = !!enabled && !pending && hostBridgeState.enabled !== false;
    [e.suffix, e.method, e.address, e.gateway, e.dns, e.preflight, e.create].forEach(function (el) {
        if (el) el.disabled = !canCreate;
    });
    if (!canCreate && e.create) {
        // keep create visible unless pending UI hides it
    }
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
    if (!pending) {
        updateHostBridgeMethodState();
    }
}

function stopNetworkConfigCountdown() {
    if (state.networkConfigCountdownTimer) {
        clearInterval(state.networkConfigCountdownTimer);
        state.networkConfigCountdownTimer = null;
    }
}

function setNetworkConfigLocked(locked) {
    var e = networkConfigEls();
    [e.device, e.method, e.address, e.gateway, e.dns, e.preflight, e.apply].forEach(function (el) {
        if (el) el.disabled = !!locked;
    });
    [e.preflight, e.apply].forEach(function (el) {
        if (el) el.hidden = !!locked;
    });
    if (!locked) updateNetworkConfigMethodState();
    [e.device, e.method].forEach(function (select) {
        if (select && window.syncCustomSelect) window.syncCustomSelect(select);
    });
}

function clearNetworkConfigHighlights() {
    var e = networkConfigEls();
    [e.method, e.address, e.gateway, e.dns].forEach(function (el) {
        if (el) el.classList.remove('changed-field');
    });
}

function markNetworkConfigChange(el, before, after) {
    if (!el) return;
    el.classList.toggle('changed-field', String(before || '') !== String(after || ''));
}

function ensureNetworkConfigOption(select, value, label) {
    if (!select || !value) return;
    var exists = Array.prototype.some.call(select.options, function (option) { return option.value === value; });
    if (!exists) {
        var option = document.createElement('option');
        option.value = value;
        option.textContent = label || value;
        select.appendChild(option);
    }
}

function applyPendingNetworkConfigToForm(pending) {
    var e = networkConfigEls();
    if (!pending || !pending.pending) return;
    ensureNetworkConfigOption(e.device, pending.device, pending.device + (pending.connection ? ' · ' + pending.connection : ''));
    if (e.device) e.device.value = pending.device || '';
    if (e.method) e.method.value = pending.method || 'manual';
    if (e.address) e.address.value = pending.address || '';
    if (e.gateway) e.gateway.value = pending.gateway || '';
    if (e.dns) e.dns.value = pending.dns || '';
    markNetworkConfigChange(e.method, pending.prev_method, pending.method);
    markNetworkConfigChange(e.address, pending.prev_address, pending.address);
    markNetworkConfigChange(e.gateway, pending.prev_gateway, pending.gateway);
    markNetworkConfigChange(e.dns, pending.prev_dns, pending.dns);
    if (window.syncCustomSelect && e.device) window.syncCustomSelect(e.device);
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
}

function renderNetworkConfigPending(pending, openWindow) {
    var e = networkConfigEls();
    if (!pending || !pending.pending) {
        stopNetworkConfigCountdown();
        state.networkConfigRollbackID = '';
        state.networkConfigRollbackUntil = 0;
        state.networkConfigPendingData = null;
        clearNetworkConfigHighlights();
        setNetworkConfigLocked(false);
        if (e.confirm) e.confirm.hidden = true;
        if (e.rollback) e.rollback.hidden = true;
        return;
    }
    state.networkConfigPendingData = pending;
    state.networkConfigRollbackID = pending.id || '';
    state.networkConfigRollbackUntil = Date.now() + Math.max(0, pending.remaining_sec || 0) * 1000;
    applyPendingNetworkConfigToForm(pending);
    setNetworkConfigLocked(true);
    if (e.confirm) {
        e.confirm.hidden = false;
        e.confirm.disabled = false;
        e.confirm.removeAttribute('disabled');
    }
    if (e.rollback) {
        e.rollback.hidden = false;
        e.rollback.disabled = false;
        e.rollback.removeAttribute('disabled');
    }
    if (openWindow && window.__app.openWindow) {
        window.__app.openWindow('network-config');
    }
    var tick = function () {
        var left = Math.max(0, Math.ceil(((state.networkConfigRollbackUntil || 0) - Date.now()) / 1000));
        if (e.status) {
            e.status.textContent = i18n('network_config_pending_active') + ': ' + (pending.device || '') + ' / ' + left + 's';
        }
        if (left <= 0) {
            stopNetworkConfigCountdown();
            setTimeout(function () { loadNetworkConfigPending(false); }, 1200);
        }
    };
    stopNetworkConfigCountdown();
    tick();
    state.networkConfigCountdownTimer = setInterval(tick, 1000);
}

async function loadNetworkConfigPending(openWindow) {
    try {
        var pending = await netwatchGet('/api/v1/network/config/pending');
        renderNetworkConfigPending(pending, openWindow);
    } catch (_) {}
}

function fillNetworkConfigForm(dev) {
    var e = networkConfigEls();
    if (state.networkConfigPendingData && state.networkConfigPendingData.pending) {
        applyPendingNetworkConfigToForm(state.networkConfigPendingData);
        return;
    }
    if (!dev) return;
    clearNetworkConfigHighlights();
    if (e.method) e.method.value = dev.ipv4_method === 'manual' ? 'manual' : 'auto';
    if (e.address) e.address.value = (dev.ipv4 || '').split(',')[0] || '';
    if (e.gateway) e.gateway.value = dev.gateway || '';
    if (e.dns) e.dns.value = dev.dns || '';
    state.networkConfigBaseline = {
        method: e.method ? e.method.value : '',
        address: e.address ? e.address.value : '',
        gateway: e.gateway ? e.gateway.value : '',
        dns: e.dns ? e.dns.value : ''
    };
    updateNetworkConfigMethodState();
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
}

function updateNetworkConfigApplyState() {
    var e = networkConfigEls();
    if (!e.apply) return;
    if (state.networkConfigPendingData && state.networkConfigPendingData.pending) {
        e.apply.disabled = true;
        renderNetworkConfigPreview();
        return;
    }
    var baseline = state.networkConfigBaseline;
    if (!baseline) {
        e.apply.disabled = true;
        renderNetworkConfigPreview();
        return;
    }
    var method = e.method ? String(e.method.value || '') : '';
    var baseMethod = String(baseline.method || '');
    var methodChanged = method !== baseMethod;
    var changed;
    if (method === 'auto') {
        // Switching to auto is itself a valid change (manual fields are ignored).
        changed = methodChanged;
    } else {
        // Manual: method flip OR any of address/gateway/dns differs from baseline.
        var addressChanged = String(e.address ? e.address.value : '') !== String(baseline.address || '');
        var gatewayChanged = String(e.gateway ? e.gateway.value : '') !== String(baseline.gateway || '');
        var dnsChanged = String(e.dns ? e.dns.value : '') !== String(baseline.dns || '');
        changed = methodChanged || addressChanged || gatewayChanged || dnsChanged;
    }
    e.apply.disabled = !changed;
    // Keep preflight always clickable on the IP panel (auto path shows a note).
    if (e.preflight && !(state.networkConfigPendingData && state.networkConfigPendingData.pending)) {
        e.preflight.disabled = false;
    }
    renderNetworkConfigPreview();
}

function renderNetworkConfigPreview() {
    var e = networkConfigEls();
    if (!e.preview) return;
    var method = e.method && e.method.value === 'auto' ? i18n('network_config_method_auto') : i18n('network_config_method_manual');
    var values = [
        method,
        e.address && e.address.value ? e.address.value : '—',
        e.gateway && e.gateway.value ? e.gateway.value : '—',
        e.dns && e.dns.value ? e.dns.value : '—'
    ];
    e.preview.textContent = i18n('network_config_preview') + ': ' + values.join(' · ');
}

function updateNetworkConfigMethodState() {
    var e = networkConfigEls();
    var manual = !e.method || e.method.value !== 'auto';
    [e.address, e.gateway, e.dns].forEach(function (el) {
        if (!el) return;
        el.disabled = !manual;
        var label = el.closest('label');
        if (label) label.hidden = !manual;
    });
    var grid = document.querySelector('#network-config-panel-ip .network-config-grid');
    if (grid) grid.classList.toggle('is-auto', !manual);
    if (e.preflight) e.preflight.disabled = false;
    updateNetworkConfigApplyState();
}

async function loadNetworkConfigDevices() {
    var e = networkConfigEls();
    if (!e.device) return;
    e.device.innerHTML = '';
    e.device.disabled = true;
    if (e.apply) e.apply.disabled = true;
    if (e.status) e.status.textContent = i18n('loading') + '...';
    if (e.output) e.output.hidden = true;
    // Loading: lock interactive fields until we know device list state.
    setNetworkConfigFormEnabled(false);
    e.device.disabled = true;
    try {
        var respData;
        var respStatus = 200;
        try {
            respData = await netwatchGet('/api/v1/network/config/devices');
        } catch (err) {
            respStatus = err.status || 500;
            if (respStatus === 404) {
                respData = null;
            } else {
                throw err;
            }
        }
        var resp = {
            status: respStatus,
            ok: respStatus >= 200 && respStatus < 300,
            json: async function () { return respData; }
        };
        if (resp.status === 404) {
            setNetworkConfigFormEnabled(false);
            if (e.status) e.status.textContent = i18n('network_config_backend_old');
            return;
        }
        var data = await resp.json();
        if (!resp.ok || !data.enabled) {
            setNetworkConfigFormEnabled(false);
            if (e.status) e.status.textContent = data.error || i18n('network_config_disabled');
            return;
        }
        var devices = data.devices || [];
        e.device.__allDevices = devices;
        e.device.onchange = function () {
            var list = e.device.__devices || [];
            var idx = e.device.selectedIndex;
            fillNetworkConfigForm(list[idx]);
            var be = hostBridgeEls();
            if (be.suffix) be.suffix.dataset.touched = '';
            if (typeof fillHostBridgeFormForDevice === 'function') {
                fillHostBridgeFormForDevice(e.device.value);
            }
        };
        if (devices.length === 0) {
            e.device.innerHTML = '';
            e.device.disabled = true;
            e.device.__devices = [];
            setNetworkConfigFormEnabled(false);
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
            if (e.status) e.status.textContent = i18n('network_config_no_device');
            if (typeof loadHostBridges === 'function') loadHostBridges();
            return;
        }
        // Fill options for current tab (bridge = ethernet only).
        renderNetworkConfigDeviceOptions();
        if (typeof loadHostBridges === 'function') { loadHostBridges(); }
        if (state.networkConfigPendingData && state.networkConfigPendingData.pending) {
            applyPendingNetworkConfigToForm(state.networkConfigPendingData);
            setNetworkConfigLocked(true);
        }
        if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
        updateNetworkConfigApplyState();
        if (e.status) e.status.textContent = i18n('network_config_ready');
    } catch (err) {
        setNetworkConfigFormEnabled(false);
        if (e.status) e.status.textContent = i18n('load_failed') + ': ' + err.message;
    }
}

async function applyNetworkConfig() {
    var e = networkConfigEls();
    var payload = {
        device: e.device ? e.device.value : '',
        method: e.method ? e.method.value : 'manual',
        address: e.address ? e.address.value : '',
        gateway: e.gateway ? e.gateway.value : '',
        dns: e.dns ? e.dns.value : ''
    };
    if (!payload.device || (payload.method !== 'auto' && (!payload.address || !payload.gateway || !payload.dns))) {
        if (e.status) e.status.textContent = i18n('network_config_required');
        return;
    }
    if (!confirm(i18n('network_config_apply_confirm'))) return;
    if (e.apply) e.apply.disabled = true;
    if (e.status) e.status.textContent = i18n('network_config_applying');
    if (e.output) e.output.hidden = true;
    try {
        var result = await netwatchPost('/api/v1/network/config/apply', payload);
        if (!result.ok) throw new Error(result.error || 'apply failed');
        state.networkConfigRollbackID = result.rollback_id;
        await loadNetworkConfigPending(false);
        if (e.output) {
            e.output.textContent = result.output || '';
            e.output.hidden = !result.output;
        }
        if (e.confirm) e.confirm.hidden = false;
        if (e.rollback) e.rollback.hidden = false;
    } catch (err) {
        if (e.status) e.status.textContent = i18n('network_config_failed') + ': ' + err.message;
    } finally {
        if (e.apply) e.apply.disabled = false;
    }
}

async function checkNetworkConfigIP() {
    var e = networkConfigEls();
    var payload = {
        device: e.device ? e.device.value : '',
        address: e.address ? e.address.value : ''
    };
    if (!payload.device || !payload.address) {
        if (e.status) e.status.textContent = i18n('network_config_check_required');
        return;
    }
    var ipOnly = String(payload.address).split('/')[0];
    if (!/^((25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])$/.test(ipOnly)) {
        if (e.status) e.status.textContent = i18n('network_config_ip_invalid');
        return;
    }
    if (e.preflight) e.preflight.disabled = true;
    if (e.status) e.status.textContent = i18n('network_config_checking_ip') + ': ' + ipOnly;
    try {
        var result = await netwatchPost('/api/v1/network/config/check-ip', payload);
        if (!result.ok) throw new Error(result.error || 'IP check failed');
        if (e.status) e.status.textContent = result.available ? i18n('network_config_ip_available') : (i18n('network_config_ip_occupied') + ': ' + (result.error || result.ip || ''));
    } catch (err) {
        if (e.status) e.status.textContent = i18n('network_config_failed') + ': ' + err.message;
    } finally {
        if (e.preflight) e.preflight.disabled = false;
    }
}

async function preflightNetworkConfig() {
    var e = networkConfigEls();
    // Single preflight entry: auto mode has nothing to ARP-check; manual runs IP occupancy.
    if (e.method && e.method.value === 'auto') {
        if (e.status) e.status.textContent = i18n('network_config_preflight_auto');
        return;
    }
    await checkNetworkConfigIP();
}

async function finishNetworkConfig(kind) {
    var e = networkConfigEls();
    var id = state.networkConfigRollbackID || '';
    if (!id) {
        if (e.status) e.status.textContent = i18n('network_config_no_pending');
        return;
    }
    var url = kind === 'confirm' ? '/api/v1/network/config/confirm' : '/api/v1/network/config/rollback';
    try {
        var result = await netwatchPost(url, { id: id });
        if (!result.ok) throw new Error(result.error || (kind + ' failed'));
        state.networkConfigRollbackID = '';
        state.networkConfigPendingData = null;
        stopNetworkConfigCountdown();
        clearNetworkConfigHighlights();
        setNetworkConfigLocked(false);
        if (e.confirm) e.confirm.hidden = true;
        if (e.rollback) e.rollback.hidden = true;
        if (e.status) e.status.textContent = kind === 'confirm' ? i18n('network_config_confirmed') : i18n('network_config_rolled_back');
        setTimeout(function () { if (window.__app.loadSummary) window.__app.loadSummary(false, true); }, 1200);
    } catch (err) {
        if (e.status) e.status.textContent = i18n('network_config_failed') + ': ' + err.message;
    }
}

function confirmNetworkConfig() { finishNetworkConfig('confirm'); }
function rollbackNetworkConfig() { finishNetworkConfig('rollback'); }

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
        var ctrlEnabled = !!(state.settings && state.settings.container_control_enabled);
        var containerData = null;
        if (ctrlEnabled && window.__app.lastContainerData) {
            containerData = window.__app.lastContainerData;
        }
        var containerMap = {};
        if (containerData && containerData.applications) {
            containerData.applications.forEach(function (app) {
                containerMap[app.bridge] = app.block_mode || '';
            });
        }
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
                var blockMode = containerMap[b.bridge] || '';
                var blockBtns = '';
                if (ctrlEnabled && containerMap.hasOwnProperty(b.bridge)) {
                    if (blockMode) {
                        blockBtns = '<button class="ctr-inline-btn ctr-btn-unblock" data-action="unblock" data-bridge="' + b.bridge + '">' + i18n('unblock_btn') + '</button>';
                    } else {
                        blockBtns = '<button class="ctr-inline-btn ctr-btn-block-net" data-action="block" data-mode="internet" data-bridge="' + b.bridge + '" title="' + i18n('block_internet_title') + '">' + i18n('block_internet_btn') + '</button>';
                    }
                }
                return '<tr data-bridge="' + b.bridge + '" data-block="' + blockMode + '">' +
                    '<td class="col-app"><div class="app-cell">' + iconHtml + '<div class="app-cell-info">' + nameHtml + '</div></div>' + blockBtns + '</td>' +
                    '<td class="col-rx">' + NetwatchShared.formatBytes(b.rx_bytes || 0) + '</td>' +
                    '<td class="col-tx">' + NetwatchShared.formatBytes(b.tx_bytes || 0) + '</td>' +
                    '<td class="col-total">' + NetwatchShared.formatBytes(total) + '</td>' +
                '</tr>';
            }).join('');
        }
        if (statusEl) statusEl.textContent = data.generated_at ? i18n('sampled_at') + ' ' + data.generated_at : '';
        if (noteEl && data.note) noteEl.textContent = data.note;
    };
    var load = async function () {
        if (btn) btn.disabled = true;
        if (statusEl) statusEl.textContent = i18n('sampling') + '...';
        try {
            var ctrlEnabled = !!(state.settings && state.settings.container_control_enabled);
            if (ctrlEnabled && window.__app.fetchContainers) {
                await window.__app.fetchContainers().catch(function () {});
            }
            var trafficData = await netwatchGet('/api/v1/network/app-traffic');
            renderTraffic(trafficData);
        } catch (e) {
            if (statusEl) statusEl.textContent = i18n('sampling_failed') + ': ' + e.message;
        } finally {
            if (btn) btn.disabled = false;
        }
    };
    window.__app.refreshAppTraffic = load;

    // Event delegation for container action buttons
    document.addEventListener('click', function (e) {
        if (window.__app.handleContainerAction) {
            window.__app.handleContainerAction(e);
        }
    });

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
        if (el('ts-container-control-enabled')) el('ts-container-control-enabled').checked = !!state.settings.container_control_enabled;
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
            container_control_enabled: !!el('ts-container-control-enabled').checked,
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
            var saved = await netwatchPost('/api/v1/settings', payload);
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
    updateTrafficAnalysisLink();
}


function nicRealtimeColumnCount(n) {
    // PC adaptive: fill rows without empty columns.
    // 1→1, 2→2, 3→3, 4→2x2, 5+→3.
    n = Number(n) || 0;
    if (n <= 1) return 1;
    if (n === 2) return 2;
    if (n === 3) return 3;
    if (n === 4) return 2;
    return 3;
}

function applyNicRealtimeLayout(listEl, count) {
    if (!listEl) return;
    var cols = nicRealtimeColumnCount(count);
    listEl.dataset.count = String(count || 0);
    listEl.dataset.cols = String(cols);
    // Inline style wins on PC; CSS media queries can override on narrow screens.
    listEl.style.gridTemplateColumns = 'repeat(' + cols + ', minmax(0, 1fr))';
}

function initNICRealtime() {
    if (state.nicRealtimeInitialized) return;
    state.nicRealtimeInitialized = true;
    var listEl = document.getElementById('nic-realtime-list');
    var statusEl = document.getElementById('nic-realtime-status');
    if (!listEl) return;
    window.renderNICRealtime = function (data) {
        var nics = Array.isArray(data && data.nics) ? data.nics.slice() : [];
        if (!nics.length) {
            applyNicRealtimeLayout(listEl, 1);
            listEl.innerHTML = '<div class="nic-realtime-item"><small>' + i18n('no_monitored_nics') + '</small></div>';
            if (statusEl) statusEl.textContent = i18n('no_data');
            return;
        }
        // Fixed positions: wired → wifi → bridge → proxy tun → other; name ASC. Never sort by rate.
        function isProxyTunName(name) {
            name = String(name || '');
            return /^(meta|mihomo|clash|utun|metacore|singbox|sb)\d*$/i.test(name);
        }
        function nicRank(name) {
            name = String(name || '');
            if (/^(en|eth|usb)/.test(name)) return 0;
            if (/^wl/.test(name)) return 1;
            if (name.indexOf('nw-') === 0) return 2;
            if (isProxyTunName(name)) return 3;
            return 4;
        }
        nics.sort(function (a, b) {
            var ra = nicRank(a.name), rb = nicRank(b.name);
            if (ra !== rb) return ra - rb;
            return String(a.name || '').localeCompare(String(b.name || ''));
        });
        applyNicRealtimeLayout(listEl, nics.length);
        listEl.innerHTML = nics.map(function (n, idx) {
            var st = String(n.oper_state || '').toLowerCase();
            var isUp = st === 'up' || (!st && n.present) ||
                (n.present && st !== 'down' && st !== 'lowerlayerdown' && st !== 'notpresent' && isProxyTunName(n.name));
            var label = n.name || '';
            if (label.indexOf('nw-') === 0) {
                label = (i18n('host_bridge_title') || '网桥') + ' · ' + label;
            } else if (isProxyTunName(label)) {
                label = (i18n('proxy_tun_title') || '代理') + ' · ' + label;
            }
            return '<div class="nic-realtime-item" data-nic="' + NetwatchShared.escapeHtml(n.name || '') + '" style="--nic-order:' + idx + '">' +
                '<div class="nic-realtime-head"><span class="nic-realtime-name">' + NetwatchShared.escapeHtml(label) +
                '</span><span class="nic-realtime-badge ' + (isUp ? 'online' : 'offline') + '">' + (isUp ? 'UP' : 'DOWN') + '</span></div>' +
                '<div class="nic-realtime-rows"><div class="nic-realtime-cell"><span class="nic-realtime-label">↓ ' + i18n('rx') +
                '</span><span class="nic-realtime-value rx">' + window.__app.formatBitsPerSec(n.rx_bps) + '</span></div>' +
                '<div class="nic-realtime-cell"><span class="nic-realtime-label">↑ ' + i18n('tx') +
                '</span><span class="nic-realtime-value tx">' + window.__app.formatBitsPerSec(n.tx_bps) + '</span></div></div>' +
                '<div class="nic-realtime-total">' + i18n('cumulative') + ' ↓ ' + NetwatchShared.formatBytes(n.rx_total) +
                ' / ↑ ' + NetwatchShared.formatBytes(n.tx_total) + '</div></div>';
        }).join('');
        if (statusEl) statusEl.textContent = i18n('sampled_at') + ' ' + (data.timestamp || '');
    };
    var tick = async function (manual) {
        if (manual === undefined) manual = false;
        try {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = true;
            if (statusEl && manual) statusEl.textContent = i18n('waiting_for_sample');
            // force=1: backend double-samples so first paint already has bps
            var path = manual ? '/api/v1/network/realtime?force=1' : '/api/v1/network/realtime';
            var realtimeData = await netwatchGet(path);
            window.renderNICRealtime(realtimeData);
        } catch (_) {
            if (statusEl) statusEl.textContent = i18n('sampling_failed');
        } finally {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = false;
        }
    };
    updateNICRealtimeRefreshButton();
    if (els.nicRealtimeRefreshBtn) {
        els.nicRealtimeRefreshBtn.addEventListener('click', function () { tick(true); });
    }
    // Startup: always force one double-sample so the card is not empty/zero-rate
    tick(true);
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
            if (window.NetwatchAPI) {
                await window.NetwatchAPI.post('/api/v1/diagnostics/trace?host=' + encodeURIComponent(host));
            } else {
                await fetch('/api/v1/diagnostics/trace?host=' + encodeURIComponent(host), { method: 'POST', cache: 'no-store' });
            }
            var poll = async function () {
                var data = await netwatchGet('/api/v1/diagnostics/trace/task');
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
    // Always show manual refresh; SSE/auto sampling does not replace an immediate re-sample.
    els.nicRealtimeRefreshBtn.style.display = '';
    els.nicRealtimeRefreshBtn.title = i18n('refresh_btn');
    els.nicRealtimeRefreshBtn.setAttribute('aria-label', i18n('refresh_btn'));
}

function updateTrafficAnalysisLink() {
    var link = document.getElementById('traffic-analysis-link');
    if (!link) return;
    var enabled = !!(state.settings && state.settings.traffic_sampling_enabled);
    // Entry only when traffic analysis sampling is enabled in settings.
    link.hidden = !enabled;
    if (enabled) {
        link.removeAttribute('hidden');
        link.classList.remove('is-disabled');
        link.setAttribute('aria-disabled', 'false');
        link.removeAttribute('tabindex');
    } else {
        link.setAttribute('hidden', '');
        link.classList.add('is-disabled');
        link.setAttribute('aria-disabled', 'true');
        link.setAttribute('tabindex', '-1');
    }
    link.title = enabled ? i18n('traffic_analysis_btn') : (i18n('traffic_sampling_disabled') || i18n('traffic_analysis_btn'));
}

function refreshAppTrafficSoon() {
    // Host bridge create briefly rewires the default route; give counters/docker a beat.
    var run = function () {
        if (window.__app && typeof window.__app.refreshAppTraffic === 'function') {
            window.__app.refreshAppTraffic();
        }
        updateTrafficAnalysisLink();
    };
    setTimeout(run, 400);
    setTimeout(run, 2000);
}

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

// --- Host bridge (tab inside network config window) ---

var hostBridgeCountdownTimer = null;
var hostBridgeState = { pending: null, bridges: [], enabled: true, candidates: [] };
var HOST_BRIDGE_PREFIX = 'nw-';
var HOST_BRIDGE_NAME_MAX = 15;

function hostBridgeEls() {
    return {
        section: document.getElementById('host-bridge-section'),
        panel: document.getElementById('network-config-panel-bridge'),
        suffix: document.getElementById('host-bridge-name-suffix'),
        name: document.getElementById('host-bridge-name'),
        method: document.getElementById('host-bridge-method'),
        address: document.getElementById('host-bridge-address'),
        gateway: document.getElementById('host-bridge-gateway'),
        dns: document.getElementById('host-bridge-dns'),
        preflight: document.getElementById('host-bridge-preflight-btn'),
        create: document.getElementById('host-bridge-create-btn'),
        confirm: document.getElementById('host-bridge-confirm-btn'),
        rollback: document.getElementById('host-bridge-rollback-btn'),
        dissolve: document.getElementById('host-bridge-dissolve-btn'),
        select: document.getElementById('host-bridge-select'),
        status: document.getElementById('host-bridge-status-line'),
        list: document.getElementById('host-bridge-list'),
        output: document.getElementById('host-bridge-output'),
        device: document.getElementById('network-config-device')
    };
}

function setHostBridgeOutput(text) {
    var e = hostBridgeEls();
    if (!e.output) return;
    if (!text) {
        e.output.hidden = true;
        e.output.textContent = '';
        return;
    }
    e.output.hidden = false;
    e.output.textContent = text;
}

function currentNetworkConfigDevice() {
    var e = hostBridgeEls();
    return e.device ? String(e.device.value || '').trim() : '';
}

function sanitizeHostBridgeSuffix(raw) {
    raw = String(raw || '').trim();
    // Strip accidental full-name / prefix the user may paste.
    if (raw.toLowerCase().indexOf(HOST_BRIDGE_PREFIX) === 0) {
        raw = raw.slice(HOST_BRIDGE_PREFIX.length);
    }
    raw = raw.toLowerCase().replace(/[^a-z0-9._-]/g, '');
    // Linux IFNAMSIZ: full name <= 15; prefix "nw-" is 3 chars.
    var maxSuffix = HOST_BRIDGE_NAME_MAX - HOST_BRIDGE_PREFIX.length;
    if (raw.length > maxSuffix) raw = raw.slice(0, maxSuffix);
    raw = raw.replace(/^[._-]+/, '').replace(/[._-]+$/, '');
    return raw;
}

function suggestHostBridgeSuffix(device) {
    device = String(device || '').toLowerCase().replace(/[^a-z0-9_-]/g, '');
    if (!device) return 'br0';
    var maxSuffix = HOST_BRIDGE_NAME_MAX - HOST_BRIDGE_PREFIX.length;
    if (device.length > maxSuffix) device = device.slice(0, maxSuffix).replace(/[._-]+$/, '');
    return device || 'br0';
}

function composedHostBridgeName() {
    var e = hostBridgeEls();
    var suffix = sanitizeHostBridgeSuffix(e.suffix ? e.suffix.value : '');
    if (!suffix) return '';
    return HOST_BRIDGE_PREFIX + suffix;
}

function syncHostBridgeNameHidden() {
    var e = hostBridgeEls();
    var full = composedHostBridgeName();
    if (e.name) e.name.value = full;
    if (e.suffix && e.suffix.value !== sanitizeHostBridgeSuffix(e.suffix.value)) {
        // normalize only when dirty chars present; keep caret friendly by not always rewriting
    }
    return full;
}

function validateHostBridgeNameClient(full) {
    full = String(full || '');
    if (!full || full === HOST_BRIDGE_PREFIX) {
        return i18n('host_bridge_name_required') || '请填写网桥名后缀';
    }
    if (full.length > HOST_BRIDGE_NAME_MAX) {
        return i18n('host_bridge_name_too_long') || ('网桥名最长 ' + HOST_BRIDGE_NAME_MAX + ' 字符');
    }
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$/.test(full)) {
        return i18n('host_bridge_name_invalid') || '网桥名不合法：字母数字开头，仅允许 . _ -';
    }
    if (full.indexOf(HOST_BRIDGE_PREFIX) !== 0) {
        return i18n('host_bridge_name_prefix') || '网桥名必须以 nw- 开头';
    }
    return '';
}

function updateHostBridgeMethodState() {
    var e = hostBridgeEls();
    var manual = e.method && e.method.value === 'manual';
    document.querySelectorAll('.host-bridge-manual-field').forEach(function (el) {
        el.hidden = !manual;
    });
    // If create is locked (no NIC / pending / disabled), keep manual fields disabled too.
    var createLocked = !e.create || e.create.disabled || e.create.hidden;
    [e.address, e.gateway, e.dns].forEach(function (el) {
        if (el) el.disabled = createLocked || !manual;
    });
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
}

function renderHostBridgeList() {
    var e = hostBridgeEls();
    if (!e.list) return;
    var bridges = hostBridgeState.bridges || [];
    // Dissolve select lists ALL managed bridges (multi-NIC multi-bridge).
    if (e.select) {
        var prev = e.select.value;
        if (!bridges.length) {
            e.select.innerHTML = '<option value="">' + NetwatchShared.escapeHtml(i18n('host_bridge_none')) + '</option>';
            e.select.disabled = true;
            if (e.dissolve) e.dissolve.disabled = true;
        } else {
            e.select.innerHTML = bridges.map(function (b) {
                var label = b.bridge + (b.device ? ' ← ' + b.device : '');
                return '<option value="' + NetwatchShared.escapeHtml(b.bridge) + '">' + NetwatchShared.escapeHtml(label) + '</option>';
            }).join('');
            e.select.disabled = false;
            if (prev && bridges.some(function (b) { return b.bridge === prev; })) {
                e.select.value = prev;
            }
            if (e.dissolve) e.dissolve.disabled = false;
        }
        if (window.syncCustomSelect) window.syncCustomSelect(e.select);
    }

    if (!bridges.length) {
        e.list.innerHTML = '<div class="note">' + i18n('host_bridge_none') + '</div>';
        return;
    }
    var selected = e.select ? e.select.value : '';
    e.list.innerHTML = bridges.map(function (b) {
        var meta = [b.device, b.method || '', b.address || '', b.ipv6_method || '', b.note || ''].filter(Boolean).join(' · ');
        var active = selected && selected === b.bridge ? ' active-select' : '';
        return '<div class="host-bridge-card' + active + '" data-bridge="' + NetwatchShared.escapeHtml(b.bridge) + '">' +
            '<div><strong>' + NetwatchShared.escapeHtml(b.bridge) + '</strong></div>' +
            '<div class="meta">' + NetwatchShared.escapeHtml(meta) + '</div></div>';
    }).join('');
}

function fillHostBridgeFormForDevice(device) {
    var e = hostBridgeEls();
    if (e.suffix && !e.suffix.dataset.touched) {
        e.suffix.value = suggestHostBridgeSuffix(device);
        syncHostBridgeNameHidden();
    }
    // Prefer inheriting current NIC IP fields when switching device.
    var ne = networkConfigEls();
    if (e.address && ne.address && !e.address.dataset.touched) e.address.value = ne.address.value || '';
    if (e.gateway && ne.gateway && !e.gateway.dataset.touched) e.gateway.value = ne.gateway.value || '';
    if (e.dns && ne.dns && !e.dns.dataset.touched) e.dns.value = ne.dns.value || '';
    updateHostBridgeMethodState();
    renderHostBridgeList();
}

async function loadHostBridges() {
    var e = hostBridgeEls();
    if (!e.section) return;
    try {
        var data = await netwatchGet('/api/v1/network/bridges');
        hostBridgeState.enabled = data.enabled !== false;
        hostBridgeState.bridges = data.bridges || [];
        hostBridgeState.candidates = data.candidates || [];
        var hasDevice = !!currentNetworkConfigDevice();
        if (!hostBridgeState.enabled) {
            if (e.status && e.status.dataset.kind !== 'pending') {
                e.status.textContent = data.error || i18n('host_bridge_disabled');
            }
            setHostBridgeCreateEnabled(false);
            if (e.select) e.select.disabled = true;
            if (e.dissolve) e.dissolve.disabled = true;
            renderHostBridgeList();
            // Keep confirm UI if a pending change exists (reconnect recovery).
            renderHostBridgePending(data.pending || null, false);
            return;
        }
        // Create needs a selected NIC; dissolve works from managed bridge list even if NIC dropdown is empty.
        setHostBridgeCreateEnabled(hasDevice);
        if (hasDevice) {
            fillHostBridgeFormForDevice(currentNetworkConfigDevice());
        }
        renderHostBridgeList();
        renderHostBridgePending(data.pending || null, false);
        if (e.status && e.status.dataset.kind !== 'pending' && !e.status.textContent) {
            e.status.textContent = '';
        }
        return data;
    } catch (err) {
        if (e.status) e.status.textContent = (err && err.message) || i18n('host_bridge_failed');
        return null;
    }
}

/** Like loadNetworkConfigPending: fetch pending and optionally auto-open the confirm UI. */
async function loadHostBridgePending(openWindow) {
    try {
        // Dedicated pending endpoint (same contract as /network/config/pending).
        var pending = await netwatchGet('/api/v1/network/bridges/pending');
        renderHostBridgePending(pending, !!openWindow);
        // Best-effort refresh inventory when a confirm window is needed.
        if (pending && pending.pending) {
            try { await loadHostBridges(); } catch (_) {}
        }
    } catch (_) {}
}

function stopHostBridgeCountdown() {
    if (hostBridgeCountdownTimer) {
        clearInterval(hostBridgeCountdownTimer);
        hostBridgeCountdownTimer = null;
    }
}

function renderHostBridgePending(pending, openWindow) {
    var e = hostBridgeEls();
    stopHostBridgeCountdown();
    hostBridgeState.pending = pending && pending.pending ? pending : null;
    if (!hostBridgeState.pending) {
        if (e.confirm) e.confirm.hidden = true;
        if (e.rollback) e.rollback.hidden = true;
        if (e.create) e.create.hidden = false;
        if (e.preflight) e.preflight.hidden = false;
        if (e.status && e.status.dataset.kind === 'pending') {
            e.status.textContent = '';
            e.status.dataset.kind = '';
        }
        // re-enable create fields when not pending
        setHostBridgeCreateEnabled(!!currentNetworkConfigDevice());
        return;
    }
    // Always force confirm/rollback interactive — is-empty / form locks must not freeze them.
    if (e.confirm) {
        e.confirm.hidden = false;
        e.confirm.disabled = false;
        e.confirm.removeAttribute('disabled');
        e.confirm.style.pointerEvents = 'auto';
        e.confirm.style.opacity = '1';
    }
    if (e.rollback) {
        e.rollback.hidden = false;
        e.rollback.disabled = false;
        e.rollback.removeAttribute('disabled');
        e.rollback.style.pointerEvents = 'auto';
        e.rollback.style.opacity = '1';
    }
    if (e.create) e.create.hidden = true;
    if (e.preflight) e.preflight.hidden = true;
    // lock create fields during pending
    [e.suffix, e.method, e.address, e.gateway, e.dns, e.preflight, e.create].forEach(function (el) {
        if (el) el.disabled = true;
    });
    // Same as NIC config pending: auto-open window + bridge tab after reconnect.
    if (openWindow) {
        if (window.__app && window.__app.openWindow) {
            window.__app.openWindow('network-config');
        }
        // Defer tab switch so window layout exists first.
        setTimeout(function () {
            if (typeof switchNetworkConfigTab === 'function') {
                switchNetworkConfigTab('bridge');
            }
            // Re-assert clickability after tab switch / device list reload races.
            var be = hostBridgeEls();
            if (be.confirm) { be.confirm.disabled = false; be.confirm.hidden = false; }
            if (be.rollback) { be.rollback.disabled = false; be.rollback.hidden = false; }
        }, 30);
    }
    var untilMs = Date.now() + Math.max(0, pending.remaining_sec || 0) * 1000;
    function paint() {
        var remain = Math.max(0, Math.ceil((untilMs - Date.now()) / 1000));
        if (e.status) {
            e.status.dataset.kind = 'pending';
            e.status.textContent = i18n('host_bridge_pending') + ' ' + remain + 's · ' +
                (pending.bridge || '') + ' ← ' + (pending.device || '');
        }
        return remain;
    }
    paint();
    hostBridgeCountdownTimer = setInterval(function () {
        var remain = paint();
        if (remain <= 0) {
            stopHostBridgeCountdown();
            setTimeout(function () { loadHostBridges(); }, 1200);
        }
    }, 1000);
}

async function finishHostBridge(action) {
    var e = hostBridgeEls();
    var pending = hostBridgeState.pending;
    var body = { id: pending && pending.id ? pending.id : '' };
    var path = action === 'confirm'
        ? '/api/v1/network/bridges/confirm'
        : '/api/v1/network/bridges/rollback';
    try {
        var result = await netwatchPost(path, body);
        setHostBridgeOutput(result.output || result.note || '');
        if (e.status) {
            e.status.dataset.kind = '';
            e.status.textContent = result.note || '';
        }
        await loadHostBridges();
        if (window.__app && window.__app.loadNetworkConfigDevices) {
            await window.__app.loadNetworkConfigDevices();
        }
        try { await refreshNetworkDetailCards(); } catch (_) {}
        refreshAppTrafficSoon();
    } catch (err) {
        var msg = (err && err.payload && err.payload.error) || (err && err.message) || i18n('host_bridge_failed');
        if (e.status) e.status.textContent = msg;
        setHostBridgeOutput((err && err.payload && err.payload.output) || '');
    }
}

async function preflightHostBridge() {
    var e = hostBridgeEls();
    var method = e.method ? e.method.value : 'inherit';
    if (method !== 'manual') {
        if (e.status) e.status.textContent = i18n('host_bridge_preflight_skip') || '继承/自动模式无需 IP 占用预检';
        return;
    }
    var device = currentNetworkConfigDevice();
    var address = e.address ? e.address.value.trim() : '';
    if (!device || !address) {
        if (e.status) e.status.textContent = i18n('network_config_check_required');
        return;
    }
    var ipOnly = String(address).split('/')[0];
    if (!/^((25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])$/.test(ipOnly)) {
        if (e.status) e.status.textContent = i18n('network_config_ip_invalid');
        return;
    }
    if (e.preflight) e.preflight.disabled = true;
    if (e.status) e.status.textContent = i18n('network_config_checking_ip') + ': ' + ipOnly;
    try {
        var result = await netwatchPost('/api/v1/network/config/check-ip', { device: device, address: address });
        if (!result.ok) throw new Error(result.error || 'IP check failed');
        if (e.status) {
            e.status.textContent = result.available
                ? i18n('network_config_ip_available')
                : (i18n('network_config_ip_occupied') + ': ' + (result.error || result.ip || ''));
        }
    } catch (err) {
        if (e.status) e.status.textContent = i18n('host_bridge_failed') + ': ' + ((err && err.message) || '');
    } finally {
        if (e.preflight) e.preflight.disabled = false;
    }
}

async function createHostBridge() {
    var e = hostBridgeEls();
    var device = currentNetworkConfigDevice();
    if (!device) {
        if (e.status) e.status.textContent = i18n('host_bridge_need_device');
        return;
    }
    var fullName = syncHostBridgeNameHidden();
    var nameErr = validateHostBridgeNameClient(fullName);
    if (nameErr) {
        if (e.status) e.status.textContent = nameErr;
        return;
    }
    var method = e.method ? e.method.value : 'inherit';
    var body = {
        device: device,
        bridge: fullName,
        method: method,
        ipv6_method: 'auto'
    };
    if (method === 'manual') {
        body.address = e.address ? e.address.value.trim() : '';
        body.gateway = e.gateway ? e.gateway.value.trim() : '';
        body.dns = e.dns ? e.dns.value.trim() : '';
        if (!body.address || !body.gateway || !body.dns) {
            if (e.status) e.status.textContent = i18n('network_config_required');
            return;
        }
    }
    if (!window.confirm(i18n('host_bridge_create_confirm'))) return;
    if (e.status) e.status.textContent = i18n('host_bridge_creating');
    setHostBridgeOutput('');
    if (e.create) e.create.disabled = true;
    try {
        var result = await netwatchPost('/api/v1/network/bridges/create', body);
        setHostBridgeOutput(result.output || result.note || '');
        if (e.status) e.status.textContent = result.note || '';
        if (e.suffix) e.suffix.dataset.touched = '';
        // Network usually drops right after create. Paint confirm UI from the
        // create response immediately — do not await heavy reloads that hang
        // on a dead TCP connection.
        if (result && (result.ok || result.rollback_id)) {
            renderHostBridgePending({
                pending: true,
                id: result.rollback_id || '',
                bridge: result.bridge || body.bridge || '',
                device: result.device || body.device || '',
                method: body.method || '',
                remaining_sec: 180
            }, true);
        }
        // Best-effort background refresh; ignore failures while link is settling.
        setTimeout(function () {
            loadHostBridgePending(true).catch(function () {});
            if (window.__app && window.__app.loadNetworkConfigDevices) {
                window.__app.loadNetworkConfigDevices().catch(function () {});
            }
            refreshNetworkDetailCards().catch(function () {});
            refreshAppTrafficSoon();
        }, 1500);
    } catch (err) {
        var msg = (err && err.payload && (err.payload.error || err.payload.message)) || (err && err.message) || i18n('host_bridge_failed');
        if (e.status) e.status.textContent = msg;
        setHostBridgeOutput((err && err.payload && err.payload.output) || '');
    } finally {
        if (e.create) e.create.disabled = false;
    }
}

async function dissolveHostBridge() {
    var e = hostBridgeEls();
    var bridge = e.select ? String(e.select.value || '').trim() : '';
    if (!bridge) {
        if (e.status) e.status.textContent = i18n('host_bridge_select_need') || '请先选择要拆除的网桥';
        return;
    }
    if (!window.confirm(i18n('host_bridge_dissolve_confirm'))) return;
    try {
        var result = await netwatchPost('/api/v1/network/bridges/dissolve', { bridge: bridge });
        setHostBridgeOutput(result.output || result.note || '');
        if (e.status) e.status.textContent = result.note || '';
        await loadHostBridges();
        if (window.__app && window.__app.loadNetworkConfigDevices) {
            await window.__app.loadNetworkConfigDevices();
        }
        try { await refreshNetworkDetailCards(); } catch (_) {}
        refreshAppTrafficSoon();
    } catch (err) {
        var msg = (err && err.payload && err.payload.error) || (err && err.message) || i18n('host_bridge_failed');
        if (e.status) e.status.textContent = msg;
        setHostBridgeOutput((err && err.payload && err.payload.output) || '');
    }
}


/** Bridge create only allows wired ethernet — Wi-Fi client mode cannot L2-bridge reliably. */
function isBridgeEligibleDevice(d) {
    if (!d || !d.device) return false;
    var typ = String(d.type || '').toLowerCase();
    if (typ && typ !== 'ethernet') return false;
    var name = String(d.device || '');
    if (/^wl/i.test(name) || /wireless/i.test(typ)) return false;
    return true;
}

function currentNetworkConfigTab() {
    var active = document.querySelector('.network-config-tab.active');
    return (active && active.dataset.tab === 'bridge') ? 'bridge' : 'ip';
}

function renderNetworkConfigDeviceOptions() {
    var e = networkConfigEls();
    if (!e.device) return;
    var all = e.device.__allDevices || [];
    var tab = currentNetworkConfigTab();
    var devices = tab === 'bridge' ? all.filter(isBridgeEligibleDevice) : all.slice();
    var prev = e.device.value;
    e.device.__devices = devices;
    if (!devices.length) {
        e.device.innerHTML = '';
        e.device.disabled = true;
        setNetworkConfigFormEnabled(false);
        if (window.syncCustomSelect) window.syncCustomSelect(e.device);
        if (e.status && tab === 'bridge') {
            e.status.textContent = i18n('host_bridge_no_ethernet') || i18n('network_config_no_device');
        }
        return;
    }
    e.device.innerHTML = devices.map(function (d, idx) {
        var label = d.device + ' · ' + d.type + (d.connection ? ' · ' + d.connection : '');
        return '<option value="' + NetwatchShared.escapeHtml(d.device) + '" data-index="' + idx + '">' + NetwatchShared.escapeHtml(label) + '</option>';
    }).join('');
    e.device.disabled = false;
    if (prev && devices.some(function (d) { return d.device === prev; })) {
        e.device.value = prev;
    }
    setNetworkConfigFormEnabled(true);
    var selected = devices.find(function (d) { return d.device === e.device.value; }) || devices[0];
    if (tab === 'ip') {
        fillNetworkConfigForm(selected);
    } else if (typeof fillHostBridgeFormForDevice === 'function') {
        fillHostBridgeFormForDevice(e.device.value);
    }
    if (window.syncCustomSelect) window.syncCustomSelect(e.device);
}

function switchNetworkConfigTab(tab) {
    tab = tab === 'bridge' ? 'bridge' : 'ip';
    // Blur any focused control BEFORE hiding a panel — otherwise Chrome warns
    // "Blocked aria-hidden on an element because its descendant retained focus"
    // (common with network-config-method custom select).
    var active = document.activeElement;
    if (active && active.closest && active.closest('.network-config-panel')) {
        try { active.blur(); } catch (_) {}
    }
    document.querySelectorAll('.network-config-tab').forEach(function (btn) {
        var on = btn.dataset.tab === tab;
        btn.classList.toggle('active', on);
        btn.setAttribute('aria-selected', on ? 'true' : 'false');
        btn.tabIndex = on ? 0 : -1;
    });
    document.querySelectorAll('.network-config-panel').forEach(function (panel) {
        var match = panel.dataset.panel === tab;
        panel.classList.toggle('active', match);
        // Keep both panels in layout (stacked) so window height doesn't jump.
        panel.removeAttribute('hidden');
        if (match) {
            panel.removeAttribute('aria-hidden');
            panel.removeAttribute('inert');
        } else {
            panel.setAttribute('aria-hidden', 'true');
            // inert prevents focus entering the inactive stacked panel.
            panel.setAttribute('inert', '');
        }
    });
    // Rebuild device dropdown: bridge tab hides Wi-Fi (cannot L2-bridge in client mode).
    if (typeof renderNetworkConfigDeviceOptions === 'function') {
        renderNetworkConfigDeviceOptions();
    }
    if (tab === 'bridge' && typeof loadHostBridges === 'function') {
        loadHostBridges();
    }
}

function bindHostBridgeUI() {
    var e = hostBridgeEls();
    if (!e.section || e.section.dataset.bound) return;
    e.section.dataset.bound = '1';

    document.querySelectorAll('.network-config-tab').forEach(function (btn) {
        btn.addEventListener('click', function () {
            switchNetworkConfigTab(btn.dataset.tab);
        });
    });

    if (e.create) e.create.addEventListener('click', createHostBridge);
    if (e.dissolve) e.dissolve.addEventListener('click', dissolveHostBridge);
    if (e.preflight) e.preflight.addEventListener('click', preflightHostBridge);
    if (e.confirm) e.confirm.addEventListener('click', function () { finishHostBridge('confirm'); });
    if (e.rollback) e.rollback.addEventListener('click', function () { finishHostBridge('rollback'); });
    if (e.method) {
        e.method.addEventListener('change', updateHostBridgeMethodState);
    }
    if (e.suffix) {
        e.suffix.addEventListener('input', function () {
            e.suffix.dataset.touched = '1';
            // live-sanitize illegal chars but keep typing smooth
            var cleaned = sanitizeHostBridgeSuffix(e.suffix.value);
            if (e.suffix.value !== cleaned && /[^a-z0-9._-]/i.test(e.suffix.value)) {
                e.suffix.value = cleaned;
            }
            syncHostBridgeNameHidden();
        });
        e.suffix.addEventListener('blur', function () {
            e.suffix.value = sanitizeHostBridgeSuffix(e.suffix.value);
            syncHostBridgeNameHidden();
        });
    }
    ['address', 'gateway', 'dns'].forEach(function (key) {
        var el = e[key];
        if (!el) return;
        el.addEventListener('input', function () { el.dataset.touched = '1'; });
    });
    if (e.select) {
        e.select.addEventListener('change', function () {
            renderHostBridgeList();
        });
    }
    if (e.device) {
        e.device.addEventListener('change', function () {
            if (e.suffix) e.suffix.dataset.touched = '';
            fillHostBridgeFormForDevice(e.device.value);
        });
    }
    updateHostBridgeMethodState();
    syncHostBridgeNameHidden();
}


window.__app.loadNetworkConfigDevices = loadNetworkConfigDevices;
window.__app.loadHostBridges = loadHostBridges;
window.__app.loadHostBridgePending = loadHostBridgePending;
window.__app.bindHostBridgeUI = bindHostBridgeUI;
window.__app.switchNetworkConfigTab = switchNetworkConfigTab;
window.__app.loadNetworkConfigPending = loadNetworkConfigPending;
window.__app.updateNetworkConfigMethodState = updateNetworkConfigMethodState;
window.__app.updateNetworkConfigApplyState = updateNetworkConfigApplyState;
window.__app.checkNetworkConfigIP = checkNetworkConfigIP;
window.__app.preflightNetworkConfig = preflightNetworkConfig;
window.__app.applyNetworkConfig = applyNetworkConfig;
window.__app.confirmNetworkConfig = confirmNetworkConfig;
window.__app.rollbackNetworkConfig = rollbackNetworkConfig;
window.__app.bindIPv6TitleEasterEgg = bindIPv6TitleEasterEgg;
window.__app.initEgressLookups = initEgressLookups;
window.__app.initAppTraffic = initAppTraffic;
window.__app.initNICRealtime = initNICRealtime;
window.__app.initTrace = initTrace;
window.__app.updateNICRealtimeRefreshButton = updateNICRealtimeRefreshButton;
window.__app.updateTrafficAnalysisLink = updateTrafficAnalysisLink;
})();
