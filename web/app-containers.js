(function () {
var i18n = window.__app.i18n;

window.__app.lastContainerData = null;

function escapeHtml(v) {
    return typeof v === 'string' ? v.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;') : '';
}

function fetchContainers() {
    return fetch('/api/v1/containers', { cache: 'no-store' }).then(function (r) {
        if (!r.ok) throw new Error('fetch failed');
        return r.json();
    }).then(function (data) {
        window.__app.lastContainerData = data;
        return data;
    });
}

function handleContainerAction(e) {
    var btn = e.target.closest('[data-action]');
    if (!btn) return;
    var action = btn.dataset.action;
    var bridge = btn.dataset.bridge;
    if (!bridge) return;

    if (action === 'unblock') {
        btn.disabled = true;
        fetch('/api/v1/containers/unblock', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ bridge: bridge })
        }).then(function (r) {
            if (!r.ok) return r.json().then(function (d) { throw new Error(d.error || 'unblock failed'); });
            NetwatchShared.showToast(i18n('container_unblocked'), 'success');
            refreshAll();
        }).catch(function (err) {
            NetwatchShared.showToast(i18n('operation_failed') + ': ' + err.message, 'error');
        }).finally(function () { btn.disabled = false; });
    } else if (action === 'block') {
        var mode = btn.dataset.mode || 'internet';
        btn.disabled = true;
        fetch('/api/v1/containers/block', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ bridge: bridge, mode: mode })
        }).then(function (r) {
            if (!r.ok) return r.json().then(function (d) { throw new Error(d.error || 'block failed'); });
            NetwatchShared.showToast(i18n('container_blocked'), 'success');
            refreshAll();
        }).catch(function (err) {
            NetwatchShared.showToast(i18n('operation_failed') + ': ' + err.message, 'error');
        }).finally(function () { btn.disabled = false; });
    }
}

function refreshAll() {
    if (window.__app.refreshAppTraffic) window.__app.refreshAppTraffic();
}

window.__app.fetchContainers = fetchContainers;
window.__app.handleContainerAction = handleContainerAction;
})();
