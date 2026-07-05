/*
 * Host port usage panel.
 */

window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var i18n = window.__app.i18n;

function initHostPorts() {
    if (state.hostPortsInitialized) return;
    state.hostPortsInitialized = true;
    var listEl = document.getElementById('host-ports-list');
    var titleEl = document.getElementById('host-ports-title');
    var statusEl = document.getElementById('host-ports-status');
    var btn = document.getElementById('host-ports-refresh-btn');
    var detailWindow = document.getElementById('host-port-detail-window');
    var detailBody = document.getElementById('host-port-detail-body');
    var detailNote = document.getElementById('host-port-detail-note');
    var detailClose = document.getElementById('close-host-port-detail-window');
    var sharedBackdrop = document.getElementById('window-backdrop');
    var sortButtons = Array.from(document.querySelectorAll('[data-host-port-sort-key]'));
    var latestPortsData = null;
    var clickCount = 0;
    var clickTimer = null;
    if (!listEl) return;

    var ownerInfo = function (p) {
        var c = p.container || null;
        if (c) {
            return {
                type: 'app',
                typeLabel: i18n('app_owner'),
                name: c.app_title || window.__app.shortAppName(c.app_id) || c.name || c.project || c.id || i18n('unknown_app')
            };
        }
        var proc = p.process || {};
        return {
            type: 'host',
            typeLabel: i18n('host_owner'),
            name: proc.name || proc.cmdline || i18n('unknown_process')
        };
    };

    var processName = function (p) {
        var proc = p.process || {};
        return proc.name || proc.cmdline || i18n('unknown_process');
    };

    var compactPorts = function (ports) {
        var seen = {};
        var out = [];
        ports.forEach(function (p) {
            var owner = ownerInfo(p);
            var protocol = String(p.protocol || '').toUpperCase();
            var key = [protocol, p.port || '', owner.type, owner.name].join('|');
            if (seen[key]) return;
            out.push({
                port: p.port || 0,
                protocol: protocol,
                state: p.state || '',
                ownerType: owner.type,
                ownerTypeLabel: owner.typeLabel,
                ownerName: owner.name,
                processName: processName(p),
                details: [p]
            });
            seen[key] = out[out.length - 1];
        });
        ports.forEach(function (p) {
            var owner = ownerInfo(p);
            var protocol = String(p.protocol || '').toUpperCase();
            var key = [protocol, p.port || '', owner.type, owner.name].join('|');
            var row = seen[key];
            if (row && row.details.indexOf(p) < 0) row.details.push(p);
        });
        return out;
    };

    var updateSortHeaders = function () {
        sortButtons.forEach(function (button) {
            var active = button.dataset.hostPortSortKey === state.hostPortsSort.key;
            button.classList.toggle('active', active);
            button.dataset.sortDirection = active ? state.hostPortsSort.direction : 'none';
            button.setAttribute('aria-sort', active ? (state.hostPortsSort.direction === 'asc' ? 'ascending' : 'descending') : 'none');
        });
    };

    var sortRows = function (rows) {
        var key = state.hostPortsSort.key;
        var direction = state.hostPortsSort.direction;
        rows.sort(function (a, b) {
            var result;
            if (key === 'owner') {
                result = String(a.ownerName || '').localeCompare(String(b.ownerName || ''), 'zh-CN');
                if (result === 0) result = (a.port || 0) - (b.port || 0);
            } else {
                result = (a.port || 0) - (b.port || 0);
                if (result === 0) result = String(a.ownerName || '').localeCompare(String(b.ownerName || ''), 'zh-CN');
            }
            return direction === 'asc' ? result : -result;
        });
        return rows;
    };

    var detailLine = function (label, value) {
        if (!value) return '';
        return '<div class="host-port-detail-row"><span>' + NetwatchShared.escapeHtml(label) + '</span><strong>' + NetwatchShared.escapeHtml(String(value)) + '</strong></div>';
    };

    var uniqueJoined = function (items, pick) {
        var seen = {};
        var out = [];
        items.forEach(function (item) {
            var value = pick(item);
            if (value === undefined || value === null || value === '') return;
            value = String(value);
            if (seen[value]) return;
            seen[value] = true;
            out.push(value);
        });
        return out.join(', ');
    };

    var openDetail = function (row) {
        if (!state.hostPortsAdvanced || !detailWindow || !detailBody) return;
        var first = row.details[0] || {};
        var proc = first.process || {};
        var c = first.container || null;
        var addresses = row.details.map(function (item) {
            return (item.address || '') + ':' + (item.port || '');
        }).filter(Boolean).join(', ');
        if (detailNote) {
            detailNote.textContent = row.port + ' ' + row.protocol + (row.state ? ' · ' + row.state : '');
        }
        detailBody.innerHTML =
            detailLine(i18n('port_col'), row.port) +
            detailLine('Protocol', row.protocol) +
            detailLine(i18n('status_col'), row.state) +
            detailLine(i18n('listen_addr_col'), addresses) +
            detailLine('IP Version', uniqueJoined(row.details, function (item) { return item.ip_version; })) +
            detailLine('Socket Inode', uniqueJoined(row.details, function (item) { return item.inode; })) +
            detailLine(i18n('owner_col'), row.ownerTypeLabel + ' · ' + row.ownerName) +
            detailLine(i18n('process_col'), row.processName) +
            detailLine('PID', proc.pid || '') +
            detailLine('PPID', proc.ppid || '') +
            detailLine('User', proc.user || '') +
            detailLine('Command', proc.cmdline || '') +
            (c ? detailLine(i18n('app_owner'), c.app_title || window.__app.shortAppName(c.app_id) || c.name || '') : '') +
            (c ? detailLine('App ID', c.app_id || '') : '') +
            (c ? detailLine('Project', c.project || '') : '') +
            (c ? detailLine(i18n('container_col'), c.name || c.id || '') : '') +
            (c ? detailLine('Container ID', c.id || '') : '') +
            (c ? detailLine('Container PID', c.pid || '') : '') +
            (c ? detailLine('Network Mode', c.network_mode || '') : '') +
            (c ? detailLine('Image', c.image || '') : '');
        detailWindow.classList.add('active');
        if (sharedBackdrop) sharedBackdrop.classList.add('active');
        NetwatchShared.lockModalScroll();
    };

    var closeDetail = function () {
        if (detailWindow) detailWindow.classList.remove('active');
        if (sharedBackdrop) sharedBackdrop.classList.remove('active');
        NetwatchShared.unlockModalScroll();
    };
    window.__app.closeHostPortDetail = closeDetail;

    var render = function (data) {
        latestPortsData = data || {};
        var rows = compactPorts(Array.isArray(latestPortsData.ports) ? latestPortsData.ports : []);
        sortRows(rows);
        updateSortHeaders();
        listEl.classList.toggle('advanced', !!state.hostPortsAdvanced);
        if (rows.length === 0) {
            listEl.innerHTML = '<div class="placeholder">' + i18n('no_host_ports') + '</div>';
        } else {
            listEl.innerHTML = rows.map(function (row, index) {
                var ownerClass = row.ownerType === 'app' ? 'app' : 'host';
                var clickable = state.hostPortsAdvanced ? ' role="button" tabindex="0" data-index="' + index + '"' : '';
                return '<div class="host-port-item"' + clickable + '>' +
                    '<div class="host-port-main"><strong>' + NetwatchShared.escapeHtml(String(row.port || '')) + '</strong><span class="host-port-proto">' + NetwatchShared.escapeHtml(row.protocol) + '</span><span class="host-port-state">' + NetwatchShared.escapeHtml(row.state || '') + '</span></div>' +
                    '<div class="host-port-owner"><span class="owner-badge ' + ownerClass + '">' + NetwatchShared.escapeHtml(row.ownerTypeLabel) + '</span><strong>' + NetwatchShared.escapeHtml(row.ownerName) + '</strong></div>' +
                '</div>';
            }).join('');
            Array.from(listEl.querySelectorAll('[data-index]')).forEach(function (item) {
                item.addEventListener('click', function () {
                    openDetail(rows[parseInt(item.dataset.index || '0', 10)]);
                });
                item.addEventListener('keydown', function (event) {
                    if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        openDetail(rows[parseInt(item.dataset.index || '0', 10)]);
                    }
                });
            });
        }
        if (statusEl) statusEl.textContent = latestPortsData.generated_at ? i18n('sampled_at') + ' ' + latestPortsData.generated_at : '';
    };

    var load = async function () {
        if (btn) btn.disabled = true;
        if (statusEl) statusEl.textContent = i18n('sampling') + '...';
        try {
            var resp = await fetch('/api/v1/network/ports', { cache: 'no-store' });
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            render(await resp.json());
        } catch (e) {
            if (statusEl) statusEl.textContent = i18n('sampling_failed') + ': ' + e.message;
        } finally {
            if (btn) btn.disabled = false;
        }
    };

    if (titleEl) {
        titleEl.addEventListener('click', function () {
            clickCount++;
            clearTimeout(clickTimer);
            clickTimer = setTimeout(function () { clickCount = 0; }, 4000);
            if (clickCount >= 10) {
                clickCount = 0;
                state.hostPortsAdvanced = !state.hostPortsAdvanced;
                render(latestPortsData);
            }
        });
    }
    if (detailClose) detailClose.addEventListener('click', closeDetail);
    if (sharedBackdrop) sharedBackdrop.addEventListener('click', closeDetail);

    sortButtons.forEach(function (button) {
        button.addEventListener('click', function () {
            var key = button.dataset.hostPortSortKey || 'port';
            if (state.hostPortsSort.key === key) {
                state.hostPortsSort.direction = state.hostPortsSort.direction === 'asc' ? 'desc' : 'asc';
            } else {
                state.hostPortsSort.key = key;
                state.hostPortsSort.direction = 'asc';
            }
            render(latestPortsData);
        });
    });
    if (btn) btn.addEventListener('click', load);
    window.__app.refreshHostPorts = load;
    load();
}

window.__app.initHostPorts = initHostPorts;
})();
