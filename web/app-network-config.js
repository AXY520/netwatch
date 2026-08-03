window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;
var netwatchGet = window.__app.netwatchGet;
var netwatchPost = window.__app.netwatchPost;
var refreshNetworkDetailCards = window.__app.refreshNetworkDetailCards;
function refreshAppTrafficSoon() {
    if (window.__app.refreshAppTrafficSoon) window.__app.refreshAppTrafficSoon();
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

// IP, bridge and DNS operations share one host-network transaction domain on
// the backend. Mirror that ownership here so the shared NIC selector has one
// deterministic source of truth instead of three independent lock checks.
var networkMutationCoordinator = (function () {
    var pendingByKind = { ip: null, bridge: null, dns: null };
    var priority = ['ip', 'bridge', 'dns'];

    function normalize(pending) {
        return pending && pending.pending ? pending : null;
    }

    return {
        setPending: function (kind, pending) {
            if (!Object.prototype.hasOwnProperty.call(pendingByKind, kind)) return;
            pendingByKind[kind] = normalize(pending);
        },
        active: function () {
            for (var i = 0; i < priority.length; i++) {
                var kind = priority[i];
                if (pendingByKind[kind]) return { kind: kind, pending: pendingByKind[kind] };
            }
            return null;
        },
        pinnedDevice: function () {
            var current = this.active();
            return current && current.pending.device
                ? String(current.pending.device).trim()
                : '';
        }
    };
})();


function pinnedNetworkConfigDevice() {
    return networkMutationCoordinator.pinnedDevice();
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
    var pin = String(pending.device || '').trim();
    if (pin) {
        ensureNetworkConfigOption(e.device, pin, pin + (pending.connection ? ' · ' + pending.connection : ''));
        // Keep pending target in __devices so later re-renders can prefer it.
        if (e.device) {
            var list = e.device.__devices || [];
            if (!list.some(function (d) { return d && d.device === pin; })) {
                list = list.concat([{ device: pin, type: '', connection: pending.connection || '' }]);
                e.device.__devices = list;
            }
            e.device.value = pin;
        }
    }
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
    networkMutationCoordinator.setPending('ip', pending);
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
        e.device.onchange = onNetworkConfigDeviceChange;
        if (devices.length === 0) {
            e.device.innerHTML = '';
            e.device.disabled = true;
            e.device.__devices = [];
            setNetworkConfigFormEnabled(false);
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
            if (e.status) e.status.textContent = i18n('network_config_no_device');
            if (typeof window.__app.loadHostBridges === 'function') {
                window.__app.loadHostBridges();
            }
            // Bridge may hold the only address — DNS candidates can still repopulate the dropdown.
            if (typeof window.__app.loadHostDNS === 'function' &&
                typeof window.__app.currentNetworkConfigTab === 'function' &&
                window.__app.currentNetworkConfigTab() === 'dns') {
                window.__app.loadHostDNS().catch(function () {});
            }
            return;
        }
        // Fill options for current tab (bridge = ethernet only).
        if (typeof window.__app.renderNetworkConfigDeviceOptions === 'function') {
            window.__app.renderNetworkConfigDeviceOptions();
        }
        if (typeof window.__app.loadHostBridges === 'function') {
            window.__app.loadHostBridges();
        }
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
    if (!(await NetwatchShared.confirmDialog({
        title: i18n('network_config_title') || '网络配置',
        message: i18n('network_config_apply_confirm'),
        okText: i18n('network_config_apply') || '应用配置',
        cancelText: i18n('close_btn') || '取消',
        danger: true
    }))) return;
    if (e.apply) e.apply.disabled = true;
    if (e.status) e.status.textContent = i18n('network_config_applying');
    if (e.output) e.output.hidden = true;
    try {
        var result = await netwatchPost('/api/v1/network/config/apply', payload);
        if (!result.ok) throw new Error(result.error || 'apply failed');
        state.networkConfigRollbackID = result.rollback_id;
        await loadNetworkConfigPending(false);
        // Hard-pin dropdown to applied NIC (device list refresh must not snap away).
        if (e.device && payload.device) {
            ensureNetworkConfigOption(e.device, payload.device, payload.device);
            e.device.value = payload.device;
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
        }
        if (e.output) {
            var configOutput = appendNetworkMutationVerification(result.output || '', result.verification);
            e.output.textContent = configOutput;
            e.output.hidden = !configOutput;
        }
        if (e.confirm) e.confirm.hidden = false;
        if (e.rollback) e.rollback.hidden = false;
    } catch (err) {
        if (e.status) e.status.textContent = i18n('network_config_failed') + ': ' + err.message;
        if (e.output && err && err.payload) {
            var failedOutput = appendNetworkMutationVerification(err.payload.output || '', err.payload.verification);
            e.output.textContent = failedOutput;
            e.output.hidden = !failedOutput;
        }
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


window.__app.networkConfigEls = networkConfigEls;
window.__app.setHostBridgeCreateEnabled = setHostBridgeCreateEnabled;
window.__app.loadNetworkConfigDevices = loadNetworkConfigDevices;
window.__app.loadNetworkConfigPending = loadNetworkConfigPending;
window.__app.updateNetworkConfigMethodState = updateNetworkConfigMethodState;
window.__app.updateNetworkConfigApplyState = updateNetworkConfigApplyState;
window.__app.checkNetworkConfigIP = checkNetworkConfigIP;
window.__app.preflightNetworkConfig = preflightNetworkConfig;
window.__app.applyNetworkConfig = applyNetworkConfig;
window.__app.confirmNetworkConfig = confirmNetworkConfig;
window.__app.rollbackNetworkConfig = rollbackNetworkConfig;
window.__app.networkMutationCoordinator = networkMutationCoordinator;
window.__app.ensureNetworkConfigOption = ensureNetworkConfigOption;
window.__app.fillNetworkConfigForm = fillNetworkConfigForm;
})();
