window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var i18n = window.__app.i18n;
var netwatchGet = window.__app.netwatchGet;
var netwatchPost = window.__app.netwatchPost;
var networkMutationCoordinator = window.__app.networkMutationCoordinator;
var currentNetworkConfigTab = window.__app.currentNetworkConfigTab;
var renderNetworkConfigDeviceOptions = window.__app.renderNetworkConfigDeviceOptions;
var ensureNetworkConfigOption = window.__app.ensureNetworkConfigOption;
var appendNetworkMutationVerification = window.__app.appendNetworkMutationVerification;

function getPinnedNetworkConfigDevice() {
    if (typeof window.__app.pinnedNetworkConfigDevice === 'function') {
        return window.__app.pinnedNetworkConfigDevice();
    }
    return networkMutationCoordinator && typeof networkMutationCoordinator.pinnedDevice === 'function'
        ? networkMutationCoordinator.pinnedDevice()
        : '';
}

var hostDNSCountdownTimer = null;
var hostDNSLoadSeq = 0;
var hostDNSState = { pending: null, info: null, enabled: true, candidates: [] };

function hostDNSEls() {
    return {
        section: document.getElementById('host-dns-section'),
        panel: document.getElementById('network-config-panel-dns'),
        method: document.getElementById('host-dns-method'),
        servers: document.getElementById('host-dns-servers'),
        apply: document.getElementById('host-dns-apply-btn'),
        confirm: document.getElementById('host-dns-confirm-btn'),
        rollback: document.getElementById('host-dns-rollback-btn'),
        status: document.getElementById('host-dns-status-line'),
        target: document.getElementById('host-dns-target-line'),
        output: document.getElementById('host-dns-output'),
        presets: document.getElementById('host-dns-presets'),
        device: document.getElementById('network-config-device')
    };
}

function setHostDNSOutput(text) {
    var e = hostDNSEls();
    if (!e.output) return;
    if (!text) {
        e.output.hidden = true;
        e.output.textContent = '';
        return;
    }
    e.output.hidden = false;
    e.output.textContent = text;
}

function updateHostDNSMethodState() {
    var e = hostDNSEls();
    var manual = e.method && e.method.value === 'manual';
    var mutationPending = !!(networkMutationCoordinator && networkMutationCoordinator.active());
    document.querySelectorAll('.host-dns-manual-field').forEach(function (el) {
        el.hidden = !manual;
    });
    if (e.method) e.method.disabled = mutationPending;
    if (e.servers) e.servers.disabled = !manual || mutationPending;
    if (e.presets) e.presets.hidden = !manual;
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
}

function stopHostDNSCountdown() {
    if (hostDNSCountdownTimer) {
        clearInterval(hostDNSCountdownTimer);
        hostDNSCountdownTimer = null;
    }
}

function renderHostDNSPending(pending, openWindow) {
    var e = hostDNSEls();
    stopHostDNSCountdown();
    hostDNSState.pending = pending && pending.pending ? pending : null;
    networkMutationCoordinator.setPending('dns', hostDNSState.pending);
    if (!hostDNSState.pending) {
        if (e.confirm) e.confirm.hidden = true;
        if (e.rollback) e.rollback.hidden = true;
        if (e.apply) e.apply.hidden = false;
        if (e.method) e.method.disabled = !!(networkMutationCoordinator && networkMutationCoordinator.active());
        if (e.servers) e.servers.disabled = !!(networkMutationCoordinator && networkMutationCoordinator.active());
        if (e.device) {
            e.device.disabled = false;
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
        }
        updateHostDNSMethodState();
        if (e.status && e.status.dataset.kind === 'pending') {
            e.status.textContent = '';
            e.status.dataset.kind = '';
        }
        return;
    }
    if (e.confirm) {
        e.confirm.hidden = false;
        e.confirm.disabled = false;
    }
    if (e.rollback) {
        e.rollback.hidden = false;
        e.rollback.disabled = false;
    }
    if (e.apply) e.apply.hidden = true;
    if (e.method) e.method.disabled = true;
    if (e.servers) e.servers.disabled = true;
    var viewingPendingDevice = !e.device || !e.device.value || e.device.value === pending.device;
    if (viewingPendingDevice && e.method && pending.method) e.method.value = pending.method;
    if (viewingPendingDevice && e.servers && pending.dns != null) e.servers.value = pending.dns || '';
    // Select the pending target when opening the recovery UI, then allow the
    // shared selector to browse other devices without changing transaction ownership.
    if (openWindow && e.device && pending.device) {
        ensureNetworkConfigOption(
            e.device,
            pending.device,
            pending.device + (pending.connection ? ' · ' + pending.connection : '')
        );
        e.device.value = pending.device;
        e.device.disabled = false;
        if (window.syncCustomSelect) window.syncCustomSelect(e.device);
    }
    updateHostDNSMethodState();
    if (window.syncCustomSelect && e.method) window.syncCustomSelect(e.method);
    if (openWindow) {
        if (window.__app && window.__app.openWindow) window.__app.openWindow('network-config');
        setTimeout(function () {
            if (typeof switchNetworkConfigTab === 'function') switchNetworkConfigTab('dns');
            if (e.device && pending.device) {
                ensureNetworkConfigOption(e.device, pending.device, pending.device + (pending.connection ? ' · ' + pending.connection : ''));
                e.device.value = pending.device;
                e.device.disabled = false;
                if (window.syncCustomSelect) window.syncCustomSelect(e.device);
            }
            if (typeof loadHostDNS === 'function') {
                loadHostDNS(pending.device).catch(function () {});
            }
        }, 30);
    }
    var untilMs = Date.now() + Math.max(0, pending.remaining_sec || 0) * 1000;
    function paint() {
        var remain = Math.max(0, Math.ceil((untilMs - Date.now()) / 1000));
        if (e.status) {
            e.status.dataset.kind = 'pending';
            e.status.textContent = i18n('host_dns_pending') + ' ' + remain + 's · ' +
                (pending.device || '') + (pending.connection ? ' · ' + pending.connection : '');
        }
        return remain;
    }
    paint();
    hostDNSCountdownTimer = setInterval(function () {
        var remain = paint();
        if (remain <= 0) {
            stopHostDNSCountdown();
            setTimeout(function () {
                var de = hostDNSEls();
                loadHostDNS(de.device ? de.device.value : '');
            }, 800);
        }
    }, 1000);
}

async function loadHostDNSPending(openWindow) {
    try {
        var pending = await netwatchGet('/api/v1/network/dns/pending');
        renderHostDNSPending(pending, !!openWindow);
    } catch (_) {}
}

function fillHostDNSFormFromInfo(data, keepDevice) {
    var e = hostDNSEls();
    if (!data) return;
    if (e.target) {
        var bits = [
            i18n('host_dns_target') + ': ' + (data.device || '—'),
            data.connection || '',
            data.type || '',
            data.runtime_dns ? (i18n('host_dns_runtime') + ' ' + data.runtime_dns) : ''
        ].filter(Boolean);
        e.target.textContent = bits.join(' · ');
    }
    if (e.method) {
        e.method.value = data.method === 'manual' ? 'manual' : 'auto';
        if (window.syncCustomSelect) window.syncCustomSelect(e.method);
    }
    if (e.servers) {
        e.servers.value = data.dns || data.runtime_dns || '';
    }
    if (!keepDevice && e.device && data.device) {
        var has = Array.prototype.some.call(e.device.options || [], function (o) { return o.value === data.device; });
        if (has) {
            e.device.value = data.device;
            if (window.syncCustomSelect) window.syncCustomSelect(e.device);
        }
    }
    updateHostDNSMethodState();
}

async function loadHostDNS(preferredDevice) {
    var e = hostDNSEls();
    if (!e.section) return null;
    var seq = ++hostDNSLoadSeq;
    var pin = getPinnedNetworkConfigDevice();
    var requested = preferredDevice != null ? String(preferredDevice || '').trim() : '';
    if (!requested) requested = pin;
    if (!requested && e.device) requested = String(e.device.value || '').trim();
    try {
        var query = requested ? { device: requested } : undefined;
        var data = await netwatchGet('/api/v1/network/dns', query);
        if (seq !== hostDNSLoadSeq) return data; // newer selection won the race
        hostDNSState.info = data;
        hostDNSState.enabled = data.enabled !== false;
        hostDNSState.candidates = data.candidates || [];

        // Rebuild shared device dropdown from DNS candidates (includes nw-* bridges).
        if (currentNetworkConfigTab() === 'dns') {
            var prefer = requested || (e.device && e.device.value) || pin || data.device || '';
            renderNetworkConfigDeviceOptions({
                dnsCandidates: hostDNSState.candidates,
                preferredDevice: prefer
            });
            if (e.device && prefer) {
                ensureNetworkConfigOption(e.device, prefer, prefer);
                e.device.value = prefer;
                e.device.disabled = false;
                if (window.syncCustomSelect) window.syncCustomSelect(e.device);
            }
        }

        if (!hostDNSState.enabled) {
            if (e.status && e.status.dataset.kind !== 'pending') {
                e.status.textContent = data.error || i18n('host_dns_disabled');
            }
            if (e.apply) e.apply.disabled = true;
            renderHostDNSPending(data.pending || null, false);
            return data;
        }
        if (e.apply) e.apply.disabled = !!(networkMutationCoordinator && networkMutationCoordinator.active());
        // keepDevice=true when user picked a device — do not snap select back to server default.
        fillHostDNSFormFromInfo(data, !!requested);
        renderHostDNSPending(data.pending || null, false);
        if (data.error && e.status && e.status.dataset.kind !== 'pending') {
            e.status.textContent = data.error;
        } else if (e.status && e.status.dataset.kind !== 'pending' && !data.error) {
            // Clear stale errors when a valid target loads.
            if (e.status.textContent === i18n('host_dns_failed') || e.status.textContent === i18n('host_dns_disabled')) {
                e.status.textContent = '';
            }
        }
        return data;
    } catch (err) {
        if (seq !== hostDNSLoadSeq) return null;
        if (e.status) e.status.textContent = (err && err.message) || i18n('host_dns_failed');
        return null;
    }
}

async function applyHostDNS() {
    var e = hostDNSEls();
    var method = e.method ? e.method.value : 'manual';
    var body = {
        method: method,
        device: (e.device && e.device.value) ? e.device.value.trim() : '',
        dns: e.servers ? e.servers.value.trim() : ''
    };
    if (method === 'manual' && !body.dns) {
        if (e.status) e.status.textContent = i18n('network_config_required');
        return;
    }
    if (!(await NetwatchShared.confirmDialog({
        title: i18n('network_config_tab_dns') || 'DNS',
        message: i18n('host_dns_apply_confirm'),
        okText: i18n('host_dns_apply') || '应用 DNS',
        cancelText: i18n('close_btn') || '取消',
        danger: true
    }))) return;
    if (e.status) e.status.textContent = i18n('host_dns_applying');
    setHostDNSOutput('');
    if (e.apply) e.apply.disabled = true;
    try {
        var result = await netwatchPost('/api/v1/network/dns/apply', body);
        setHostDNSOutput(appendNetworkMutationVerification(result.output || result.note || '', result.verification));
        if (e.status) e.status.textContent = result.note || '';
        if (result && (result.ok || result.rollback_id)) {
            var pendDev = result.device || body.device || '';
            renderHostDNSPending({
                pending: true,
                id: result.rollback_id || '',
                device: pendDev,
                connection: result.connection || '',
                method: result.method || body.method,
                dns: result.dns != null ? result.dns : body.dns,
                remaining_sec: 60
            }, true);
            if (e.device && pendDev) {
                ensureNetworkConfigOption(e.device, pendDev, pendDev);
                e.device.value = pendDev;
                e.device.disabled = false;
                if (window.syncCustomSelect) window.syncCustomSelect(e.device);
            }
        }
        setTimeout(function () {
            loadHostDNSPending(true).catch(function () {});
            var keep = (result && result.device) || body.device || (e.device && e.device.value) || '';
            loadHostDNS(keep).catch(function () {});
        }, 800);
    } catch (err) {
        var msg = (err && err.payload && (err.payload.error || err.payload.message)) || (err && err.message) || i18n('host_dns_failed');
        if (e.status) e.status.textContent = msg;
        setHostDNSOutput(appendNetworkMutationVerification((err && err.payload && err.payload.output) || '', err && err.payload && err.payload.verification));
    } finally {
        if (e.apply) e.apply.disabled = false;
    }
}

async function finishHostDNS(action) {
    var e = hostDNSEls();
    var pending = hostDNSState.pending;
    var body = { id: pending && pending.id ? pending.id : '' };
    var path = action === 'confirm'
        ? '/api/v1/network/dns/confirm'
        : '/api/v1/network/dns/rollback';
    try {
        var result = await netwatchPost(path, body);
        setHostDNSOutput(result.output || result.note || '');
        if (e.status) {
            e.status.dataset.kind = '';
            e.status.textContent = result.note || '';
        }
        stopHostDNSCountdown();
        hostDNSState.pending = null;
        networkMutationCoordinator.setPending('dns', null);
        await loadHostDNS(e.device ? e.device.value : '');
    } catch (err) {
        var msg = (err && err.payload && err.payload.error) || (err && err.message) || i18n('host_dns_failed');
        if (e.status) e.status.textContent = msg;
        setHostDNSOutput((err && err.payload && err.payload.output) || '');
    }
}

function bindHostDNSUI() {
    var e = hostDNSEls();
    if (!e.section || e.section.dataset.bound) return;
    e.section.dataset.bound = '1';
    if (e.method) e.method.addEventListener('change', updateHostDNSMethodState);
    if (e.apply) e.apply.addEventListener('click', applyHostDNS);
    if (e.confirm) e.confirm.addEventListener('click', function () { finishHostDNS('confirm'); });
    if (e.rollback) e.rollback.addEventListener('click', function () { finishHostDNS('rollback'); });
    if (e.presets) {
        e.presets.addEventListener('click', function (ev) {
            var btn = ev.target && ev.target.closest ? ev.target.closest('.host-dns-preset') : null;
            if (!btn || !e.servers) return;
            e.servers.value = btn.getAttribute('data-dns') || '';
            if (e.method) {
                e.method.value = 'manual';
                updateHostDNSMethodState();
            }
        });
    }
    updateHostDNSMethodState();
}



window.__app.hostDNSState = hostDNSState;
window.__app.hostDNSEls = hostDNSEls;
window.__app.setHostDNSOutput = setHostDNSOutput;
window.__app.fillHostDNSFormFromInfo = fillHostDNSFormFromInfo;
window.__app.loadHostDNS = loadHostDNS;
window.__app.loadHostDNSPending = loadHostDNSPending;
window.__app.bindHostDNSUI = bindHostDNSUI;
})();
