window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var i18n = window.__app.i18n;
var netwatchGet = window.__app.netwatchGet;
var netwatchPost = window.__app.netwatchPost;
var networkConfigEls = window.__app.networkConfigEls;
var networkMutationCoordinator = window.__app.networkMutationCoordinator;
var ensureNetworkConfigOption = window.__app.ensureNetworkConfigOption;
var fillNetworkConfigForm = window.__app.fillNetworkConfigForm;
var updateNetworkConfigApplyState = window.__app.updateNetworkConfigApplyState;
var loadNetworkConfigPending = window.__app.loadNetworkConfigPending;

function getPinnedNetworkConfigDevice() {
    if (typeof window.__app.pinnedNetworkConfigDevice === 'function') {
        return window.__app.pinnedNetworkConfigDevice();
    }
    return networkMutationCoordinator && typeof networkMutationCoordinator.pinnedDevice === 'function'
        ? networkMutationCoordinator.pinnedDevice()
        : '';
}

function setSharedNetworkConfigFormEnabled(enabled) {
    if (typeof window.__app.setNetworkConfigFormEnabled === 'function') {
        window.__app.setNetworkConfigFormEnabled(enabled);
    }
}

function applySharedPendingNetworkConfig(pending) {
    if (typeof window.__app.applyPendingNetworkConfigToForm === 'function') {
        window.__app.applyPendingNetworkConfigToForm(pending);
    }
}

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

function clearNetworkConfigLogs() {
    // Drop create/dissolve/apply logs when the window is closed so they don't linger.
    setHostBridgeOutput('');
    if (typeof window.__app.setHostDNSOutput === 'function') window.__app.setHostDNSOutput('');
    var ne = typeof networkConfigEls === 'function' ? networkConfigEls() : {};
    if (ne.output) {
        ne.output.hidden = true;
        ne.output.textContent = '';
    }
    // Keep pending countdown text; only clear one-shot status notes.
    var be = typeof hostBridgeEls === 'function' ? hostBridgeEls() : {};
    if (be.status && be.status.dataset.kind !== 'pending') {
        be.status.textContent = '';
        be.status.dataset.kind = '';
    }
    if (ne.status && !(state.networkConfigPendingData && state.networkConfigPendingData.pending)) {
        ne.status.textContent = '';
    }
    var de = typeof window.__app.hostDNSEls === 'function' ? window.__app.hostDNSEls() : {};
    if (de.status && de.status.dataset.kind !== 'pending') {
        de.status.textContent = '';
        de.status.dataset.kind = '';
    }
}

function setHostBridgeCreateEnabled(enabled) {
    var e = hostBridgeEls();
    if (!e.section) return;
    var pending = !!(hostBridgeState.pending && hostBridgeState.pending.pending);
    var canCreate = !!enabled && !pending && hostBridgeState.enabled !== false;
    [e.suffix, e.method, e.address, e.gateway, e.dns, e.preflight, e.create].forEach(function (el) {
        if (el) el.disabled = !canCreate;
    });
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
    if (!pending) updateHostBridgeMethodState();
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

function appendNetworkMutationVerification(output, verification) {
	output = String(output || '').trim();
	if (!verification || !Array.isArray(verification.steps)) return output;
	var statusKey = verification.status === 'passed' ? 'ok' : (verification.status === 'warning' ? 'degraded' : 'failed');
	var lines = [i18n('network_mutation_verification') + ': ' + i18n(statusKey) + ' (' + (verification.duration_ms || 0) + 'ms)'];
	verification.steps.forEach(function (step) {
		if (step && !step.ok) {
			var label = i18n('network_verify_' + step.name);
			if (!label || label === 'network_verify_' + step.name) label = step.name || i18n('unknown');
			lines.push(label + ': ' + (step.error || i18n('failed')));
		}
	});
	return [output, lines.join('\n')].filter(Boolean).join('\n');
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
    networkMutationCoordinator.setPending('bridge', hostBridgeState.pending);
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
        setHostBridgeOutput(appendNetworkMutationVerification(result.output || result.note || '', result.verification));
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
        setHostBridgeOutput(appendNetworkMutationVerification((err && err.payload && err.payload.output) || '', err && err.payload && err.payload.verification));
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
    if (!(await NetwatchShared.confirmDialog({
        title: i18n('host_bridge_create') || '创建网桥',
        message: i18n('host_bridge_create_confirm'),
        okText: i18n('host_bridge_create') || '创建网桥',
        cancelText: i18n('close_btn') || '取消',
        danger: true
    }))) return;
    if (e.status) e.status.textContent = i18n('host_bridge_creating');
    setHostBridgeOutput('');
    if (e.create) e.create.disabled = true;
    try {
        var result = await netwatchPost('/api/v1/network/bridges/create', body);
        setHostBridgeOutput(appendNetworkMutationVerification(result.output || result.note || '', result.verification));
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
        setHostBridgeOutput(appendNetworkMutationVerification((err && err.payload && err.payload.output) || '', err && err.payload && err.payload.verification));
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
    if (!(await NetwatchShared.confirmDialog({
        title: i18n('host_bridge_dissolve') || '拆除网桥',
        message: i18n('host_bridge_dissolve_confirm'),
        okText: i18n('host_bridge_dissolve') || '拆除网桥',
        cancelText: i18n('close_btn') || '取消',
        danger: true
    }))) return;
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
    var tab = active && active.dataset.tab;
    if (tab === 'bridge' || tab === 'dns') return tab;
    return 'ip';
}

function devicesFromDNSCandidates(candidates) {
    return (candidates || []).map(function (c) {
        return {
            device: c.device,
            type: c.type || 'dns',
            connection: c.connection || '',
            ipv4_method: c.method === 'manual' ? 'manual' : 'auto',
            dns: c.dns || ''
        };
    });
}

function renderNetworkConfigDeviceOptions(opts) {
    opts = opts || {};
    var e = networkConfigEls();
    if (!e.device) return;
    var all = e.device.__allDevices || [];
    var tab = currentNetworkConfigTab();
    var devices;
    if (tab === 'bridge') {
        devices = all.filter(isBridgeEligibleDevice);
    } else if (tab === 'dns') {
        var cands = opts.dnsCandidates || ((window.__app.hostDNSState && window.__app.hostDNSState.candidates) || []);
        devices = cands.length ? devicesFromDNSCandidates(cands) : all.slice();
    } else {
        devices = all.slice();
    }
    var pin = getPinnedNetworkConfigDevice();
    var prev = opts.preferredDevice || pin || e.device.value;
    // Pending confirmation must always keep the target NIC visible/selected.
    if (pin && !devices.some(function (d) { return d && d.device === pin; })) {
        devices = devices.concat([{ device: pin, type: 'pending', connection: '' }]);
    }
    e.device.__devices = devices;
    if (!devices.length) {
        NetwatchShared.setSelectOptions(e.device, [], '', true);
        // Keep IP/bridge create locked when no eligible NIC; DNS confirm stays CSS-exempt.
        if (tab !== 'dns') setSharedNetworkConfigFormEnabled(false);
        if (e.status && tab === 'bridge') {
            e.status.textContent = i18n('host_bridge_no_ethernet') || i18n('network_config_no_device');
        }
        return;
    }
    var options = devices.map(function (d, idx) {
        var label = d.device + ' · ' + d.type + (d.connection ? ' · ' + d.connection : '');
        return { value: d.device, label: label, dataset: { index: idx } };
    });
    var selectedValue;
    if (pin && devices.some(function (d) { return d.device === pin; })) {
        selectedValue = pin;
    } else if (prev && devices.some(function (d) { return d.device === prev; })) {
        selectedValue = prev;
    } else {
        selectedValue = devices[0].device;
    }
    NetwatchShared.setSelectOptions(e.device, options, selectedValue, false);
    if (tab === 'dns') {
        // Shared body may be is-empty after bridge enslaves NIC — re-enable device only.
        e.device.disabled = false;
        var body = document.querySelector('.network-config-body');
        // Do not clear is-empty globally (IP/bridge create should stay locked).
    } else {
        setSharedNetworkConfigFormEnabled(true);
    }
    var selected = devices.find(function (d) { return d.device === e.device.value; }) || devices[0];
    if (pin && state.networkConfigPendingData && state.networkConfigPendingData.pending) {
        applySharedPendingNetworkConfig(state.networkConfigPendingData);
    } else if (tab === 'ip') {
        fillNetworkConfigForm(selected);
    } else if (tab === 'bridge' && typeof fillHostBridgeFormForDevice === 'function') {
        fillHostBridgeFormForDevice(e.device.value);
    }
    // dns form fill is handled by loadHostDNS / onNetworkConfigDeviceChange
}

function onNetworkConfigDeviceChange() {
    var e = networkConfigEls();
    if (!e.device) return;
    var pin = getPinnedNetworkConfigDevice();
    if (pin) {
        // Pending confirm/rollback: never follow accidental selection changes.
        if (e.device.value !== pin) {
            e.device.value = pin;
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
        }
        return;
    }
    var tab = currentNetworkConfigTab();
    var list = e.device.__devices || [];
    var selected = null;
    for (var i = 0; i < list.length; i++) {
        if (list[i] && list[i].device === e.device.value) {
            selected = list[i];
            break;
        }
    }
    if (!selected && e.device.selectedIndex >= 0) selected = list[e.device.selectedIndex] || null;

    if (tab === 'dns') {
        // Instant local paint from candidate cache, then confirm with API for that device.
        if (selected) {
            if (typeof window.__app.fillHostDNSFormFromInfo === 'function') window.__app.fillHostDNSFormFromInfo({
                device: selected.device,
                connection: selected.connection || '',
                type: selected.type || '',
                method: selected.ipv4_method === 'manual' ? 'manual' : 'auto',
                dns: selected.dns || ''
            }, true);
        }
        window.__app.loadHostDNS(e.device.value).catch(function () {});
        return;
    }
    if (tab === 'bridge') {
        var be = hostBridgeEls();
        if (be.suffix) be.suffix.dataset.touched = '';
        if (typeof fillHostBridgeFormForDevice === 'function') {
            fillHostBridgeFormForDevice(e.device.value);
        }
        return;
    }
    if (selected) fillNetworkConfigForm(selected);
}

function switchNetworkConfigTab(tab) {
    if (tab !== 'bridge' && tab !== 'dns') tab = 'ip';
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
    if (tab === 'dns' && typeof window.__app.loadHostDNS === 'function') {
        var de = networkConfigEls();
        var pinDev = getPinnedNetworkConfigDevice();
        window.__app.loadHostDNS(pinDev || (de.device ? de.device.value : ''));
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
    // Device change is owned by onNetworkConfigDeviceChange (set in loadNetworkConfigDevices).
    updateHostBridgeMethodState();
    syncHostBridgeNameHidden();
}



window.__app.loadHostBridges = loadHostBridges;
window.__app.loadHostBridgePending = loadHostBridgePending;
window.__app.bindHostBridgeUI = bindHostBridgeUI;
window.__app.clearNetworkConfigLogs = clearNetworkConfigLogs;
window.__app.switchNetworkConfigTab = switchNetworkConfigTab;
window.__app.currentNetworkConfigTab = currentNetworkConfigTab;
window.__app.renderNetworkConfigDeviceOptions = renderNetworkConfigDeviceOptions;
window.__app.onNetworkConfigDeviceChange = onNetworkConfigDeviceChange;
window.__app.appendNetworkMutationVerification = appendNetworkMutationVerification;
window.__app.setHostBridgeCreateEnabled = setHostBridgeCreateEnabled;
})();
