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
    var searchInput = document.getElementById('host-ports-search');
    var latestPortsData = null;
    var lastSuccessfulData = null;
    var clickCount = 0;
    var clickTimer = null;
    if (!listEl) return;

    var ownerInfo = function (p) {
        var c = p.container || null;
        if (c) {
            var appName = c.app_title || window.__app.shortAppName(c.app_id) || c.project || c.name || c.id || i18n('unknown_app');
            return {
                type: 'app',
                key: c.app_id || c.project || c.app_title || c.name || c.id || i18n('unknown_app'),
                typeLabel: i18n('app_owner'),
                name: appName,
                icon: c.icon || ''
            };
        }
        var proc = p.process || {};
        return {
            type: 'host',
            key: 'host',
            typeLabel: i18n('host_owner'),
            name: proc.name || proc.cmdline || i18n('unknown_process'),
            icon: ''
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
                ownerKey: owner.key,
                ownerIcon: owner.icon,
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

    var sortRows = function (rows) {
        rows.sort(function (a, b) {
            return ((a.port || 0) - (b.port || 0)) || String(a.protocol || '').localeCompare(String(b.protocol || '')) || String(a.ownerName || '').localeCompare(String(b.ownerName || ''), 'zh-CN');
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
        var allRows = compactPorts(Array.isArray(latestPortsData.ports) ? latestPortsData.ports : []);
        var rows = allRows.slice();
        var query = searchInput ? searchInput.value.trim() : '';
        var queryValid = query === '' || /^\d{1,5}$/.test(query);
        if (queryValid && query !== '') {
            rows = rows.filter(function (row) {
                return String(row.port || '').indexOf(query) !== -1;
            });
        }
        sortRows(rows);
        if (queryValid && query !== '') {
            rows.sort(function (a, b) {
                var aPort = String(a.port || '');
                var bPort = String(b.port || '');
                var aRank = aPort === query ? 0 : (aPort.indexOf(query) === 0 ? 1 : 2);
                var bRank = bPort === query ? 0 : (bPort.indexOf(query) === 0 ? 1 : 2);
                return (aRank - bRank) || ((a.port || 0) - (b.port || 0));
            });
        }
        listEl.classList.toggle('advanced', !!state.hostPortsAdvanced);
        if (rows.length === 0) {
            listEl.innerHTML = '<div class="host-port-empty"><strong>' + NetwatchShared.escapeHtml(query === '' ? i18n('no_host_ports') : (queryValid ? i18n('port_not_occupied') : i18n('port_search_invalid'))) + '</strong></div>';
        } else {
            var groups = [];
            var groupMap = {};
            rows.forEach(function (row) {
                var key = row.ownerType + '|' + row.ownerKey;
                if (!groupMap[key]) {
                    groupMap[key] = { type: row.ownerType, name: row.ownerName, icon: row.ownerIcon, rows: [] };
                    groups.push(groupMap[key]);
                }
                groupMap[key].rows.push(row);
            });
            groups.sort(function (a, b) {
                if (a.type !== b.type) return a.type === 'host' ? 1 : -1;
                return String(a.name || '').localeCompare(String(b.name || ''), 'zh-CN');
            });
            listEl.innerHTML = groups.map(function (group) {
                var ownerClass = group.type === 'app' ? 'app' : 'host';
                var iconHtml = group.icon ? '<img class="app-icon host-port-app-icon" src="' + NetwatchShared.escapeHtml(group.icon) + '" alt="" loading="lazy" onerror="this.style.display=\'none\'">' : '';
                var groupRows = group.rows.map(function (row, index) {
                    var clickable = state.hostPortsAdvanced ? ' role="button" tabindex="0" data-group="' + NetwatchShared.escapeHtml(group.type + '|' + (group.type === 'host' ? 'host' : group.rows[0].ownerKey)) + '" data-index="' + index + '"' : '';
                    var advancedSummary = state.hostPortsAdvanced
                        ? '<div class="host-port-listen">' + (group.type === 'host' ? '<strong class="host-port-row-owner">' + NetwatchShared.escapeHtml(row.ownerName) + '</strong><span> · </span>' : '') + NetwatchShared.escapeHtml(uniqueJoined(row.details, function (item) { return item.address; })) + '</div>'
                        : '';
                    return '<div class="host-port-item"' + clickable + '>' +
                        '<div class="host-port-main"><strong>' + NetwatchShared.escapeHtml(String(row.port || '')) + '</strong><span class="host-port-proto">' + NetwatchShared.escapeHtml(row.protocol) + '</span><span class="host-port-state">' + NetwatchShared.escapeHtml(row.state || '') + '</span></div>' +
                        advancedSummary +
                    '</div>';
                }).join('');
                return '<section class="host-port-group ' + ownerClass + '"><div class="host-port-group-head">' + iconHtml + '<span class="owner-badge ' + ownerClass + '">' + NetwatchShared.escapeHtml(group.type === 'app' ? i18n('app_owner') : i18n('host_services')) + '</span><strong>' + NetwatchShared.escapeHtml(group.type === 'app' ? group.name : i18n('host_services')) + '</strong><span class="host-port-group-count">' + group.rows.length + '</span></div><div class="host-port-group-list">' + groupRows + '</div></section>';
            }).join('');
            Array.from(listEl.querySelectorAll('[data-group]')).forEach(function (item) {
                var group = groupMap[item.dataset.group];
                if (!group) return;
                var row = group.rows[parseInt(item.dataset.index || '0', 10)];
                item.addEventListener('click', function () {
                    openDetail(row);
                });
                item.addEventListener('keydown', function (event) {
                    if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        openDetail(row);
                    }
                });
            });
        }
        if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
            state: rows.length ? 'fresh' : 'empty',
            count: allRows.length,
            countLabel: i18n('ports_unit'),
            generatedAt: latestPortsData.generated_at,
            stale: !!latestPortsData.stale,
            ageSeconds: latestPortsData.age_seconds,
            staleAfterSeconds: 120,
            title: latestPortsData.note || ''
        });
    };

    var load = async function () {
        if (btn) btn.disabled = true;
        if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
            state: lastSuccessfulData ? 'refreshing' : 'loading',
            count: lastSuccessfulData && Array.isArray(lastSuccessfulData.ports) ? compactPorts(lastSuccessfulData.ports).length : null,
            countLabel: i18n('ports_unit'),
            generatedAt: lastSuccessfulData && lastSuccessfulData.generated_at
        });
        document.getElementById('host-ports-section')?.setAttribute('aria-busy', 'true');
        try {
            var data = window.NetwatchAPI
                ? await window.NetwatchAPI.get('/api/v1/network/ports')
                : await (async function () {
                    var resp = await fetch('/api/v1/network/ports', { cache: 'no-store' });
                    if (!resp.ok) throw new Error('HTTP ' + resp.status);
                    return resp.json();
                })();
            lastSuccessfulData = data;
            render(data);
        } catch (e) {
            if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
                state: 'error',
                count: lastSuccessfulData && Array.isArray(lastSuccessfulData.ports) ? compactPorts(lastSuccessfulData.ports).length : null,
                countLabel: i18n('ports_unit'),
                generatedAt: lastSuccessfulData && lastSuccessfulData.generated_at,
                error: e.message
            });
        } finally {
            if (btn) btn.disabled = false;
            document.getElementById('host-ports-section')?.removeAttribute('aria-busy');
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

    if (searchInput) searchInput.addEventListener('input', function () { render(latestPortsData); });
    if (btn) btn.addEventListener('click', load);
    window.__app.refreshHostPorts = load;
    load();
}

window.__app.initHostPorts = initHostPorts;
})();
