window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;
var netwatchGet = window.__app.netwatchGet;
var netwatchPost = window.__app.netwatchPost;

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
    var activeMenuAppID = '';
    var activeLimitAppID = '';
    var activeProxyAppID = '';
    var activeConnectionsAppID = '';
    var activeConnectionsItem = null;
    var connectionsTimer = null;
    var connectionsLoading = false;
    var connectionsReloadPending = false;
    var connectionsGeneration = 0;
    var connectionsAutoRefresh = true;
    var lastConnectionsRenderSignature = '';
    var limitWindow = document.getElementById('app-traffic-limit-window');
    var limitBackdrop = document.getElementById('app-traffic-limit-backdrop');
    var limitTitle = document.getElementById('app-traffic-limit-title');
    var limitNote = document.getElementById('app-traffic-limit-note');
    var limitForm = document.getElementById('app-traffic-limit-form');
    var limitUpload = document.getElementById('app-traffic-limit-upload');
    var limitDownload = document.getElementById('app-traffic-limit-download');
    var limitError = document.getElementById('app-traffic-limit-error');
    var proxyWindow = document.getElementById('app-traffic-proxy-window');
    var proxyBackdrop = document.getElementById('app-traffic-proxy-backdrop');
    var proxyTitle = document.getElementById('app-traffic-proxy-title');
    var proxyNote = document.getElementById('app-traffic-proxy-note');
    var proxyForm = document.getElementById('app-traffic-proxy-form');
    var proxyProtocol = document.getElementById('app-traffic-proxy-protocol');
    var proxyHost = document.getElementById('app-traffic-proxy-host');
    var proxyPort = document.getElementById('app-traffic-proxy-port');
    var proxyError = document.getElementById('app-traffic-proxy-error');
    var connectionsWindow = document.getElementById('app-connections-window');
    var connectionsBackdrop = document.getElementById('app-connections-backdrop');
    var connectionsTitle = document.getElementById('app-connections-title');
    var connectionsInstance = document.getElementById('app-connections-instance');
    var connectionsStatus = document.getElementById('app-connections-status');
    var connectionsNote = document.getElementById('app-connections-note');
    var connectionsBody = document.getElementById('app-connections-body');
    var connectionsRefreshToggle = document.getElementById('toggle-app-connections-refresh');
    var connectionsLive = document.getElementById('app-connections-live');
    var trafficLoading = false;
    // Keep failed icon URLs out of subsequent renders so a missing/broken icon
    // is not fetched again on every counter sample. A later URL change (or
    // expiry) can retry.
    var failedAppIcons = Object.create(null);
    var failedAppIconRetryMs = 10 * 60 * 1000;
    var lastTrafficRenderSignature = '';
    var lastTrafficStructureSignature = '';
    var renderedTrafficMode = null;
    if (!tbody) return;
    var appTrafficRealtimeEnabled = function () {
        return state.settings.app_traffic_realtime_enabled !== false;
    };
    var syncAppTrafficRealtimeColumn = function () {
        var enabled = appTrafficRealtimeEnabled();
        if (!enabled && state.appTrafficSort.key === 'rate') {
            state.appTrafficSort.key = 'total';
            state.appTrafficSort.direction = 'desc';
        }
        if (table) table.classList.toggle('app-traffic-realtime-hidden', !enabled);
        return enabled;
    };
    var getAppTrafficName = function (item) {
        return String(appTrafficDisplayName(item)).toLowerCase();
    };
    var sortAppTrafficRows = function (a, b, key, direction) {
        var result = 0;
        if (key === 'app') {
            result = getAppTrafficName(a).localeCompare(getAppTrafficName(b), 'zh-CN');
        } else if (key === 'rate') {
            result = appTrafficRate(a) - appTrafficRate(b);
        } else if (key === 'today') {
            result = appTrafficPeriod(a, 'today') - appTrafficPeriod(b, 'today');
        } else if (key === 'month') {
            result = appTrafficPeriod(a, 'month') - appTrafficPeriod(b, 'month');
        } else {
            result = appTrafficPeriod(a, 'total') - appTrafficPeriod(b, 'total');
        }
        return direction === 'asc' ? result : -result;
    };
    var updateSortHeaders = function () {
        if (!table) return;
        syncAppTrafficRealtimeColumn();
        var key = state.appTrafficSort.key;
        var direction = state.appTrafficSort.direction;
        sortButtons.forEach(function (button) {
            var active = button.dataset.sortKey === key;
            button.classList.toggle('active', active);
            button.dataset.sortDirection = active ? direction : 'none';
            button.setAttribute('aria-sort', active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
        });
    };
    var trafficRowKey = function (item, index) {
        return String((item && (item.instance_id || item.app_id || item.project || item.app_title)) || ('row-' + index));
    };
    var trafficRenderSignature = function (list, showRealtime, data) {
        return JSON.stringify({
            mode: showRealtime,
            sort: state.appTrafficSort.key + ':' + state.appTrafficSort.direction,
            limit: !!data.limit_support,
            apps: list.map(function (item, index) {
                return [
                    trafficRowKey(item, index), item.icon || '', item.app_title || '', item.status_text || '',
                    item.upload_bps || 0, item.download_bps || 0,
                    item.today_upload || 0, item.today_download || 0,
                    item.month_upload || 0, item.month_download || 0,
                    item.total_upload || 0, item.total_download || 0,
                    item.limit || null, item.bridges || [], item.network_modes || [],
                    item.network_topology || '', item.network_policy || null
                ];
            })
        });
    };
    var trafficStructureSignature = function (list, showRealtime, data) {
        return JSON.stringify({
            mode: showRealtime,
            sort: state.appTrafficSort.key + ':' + state.appTrafficSort.direction,
            limit: !!data.limit_support,
            apps: list.map(function (item, index) {
                return [
                    trafficRowKey(item, index), item.icon || '', item.app_title || '', item.status_text || '',
                    item.limit || null, item.bridges || [], item.network_modes || [],
                    item.network_topology || '', item.network_policy || null
                ];
            })
        });
    };
    var setTrafficCellHTML = function (cell, html) {
        if (cell && cell.innerHTML !== html) cell.innerHTML = html;
    };
    var updateTrafficAppCell = function (cell, item) {
        var wrapper = cell.querySelector('.app-cell');
        var info = cell.querySelector('.app-cell-info');
        if (!wrapper || !info) return;
        var iconURL = String(item.icon || '').trim();
        var iconKey = appTrafficIconKey(item);
        var failed = iconKey && iconURL ? failedAppIcons[iconKey] : null;
        var iconRetryAllowed = !failed || failed.url !== iconURL || (Date.now() - failed.at) >= failedAppIconRetryMs;
        var image = wrapper.querySelector('img.app-icon');
        if (iconURL && iconRetryAllowed) {
            if (!image) {
                image = document.createElement('img');
                image.className = 'app-icon';
                image.alt = '';
                image.loading = 'lazy';
                wrapper.insertBefore(image, info);
            }
            image.dataset.appIconKey = iconKey;
            if (image.getAttribute('src') !== iconURL) image.src = iconURL;
            image.style.display = '';
        } else if (image) {
            image.style.display = 'none';
        }
        var name = appTrafficDisplayName(item) || i18n('unknown_status');
        setTrafficCellHTML(info, '<strong>' + NetwatchShared.escapeHtml(name) + '</strong>' +
            (item.status_text ? '<div class="app-status-text">' + NetwatchShared.escapeHtml(item.status_text) + '</div>' : ''));
    };
    var createTrafficRow = function (item, showRealtime) {
        var row = document.createElement('tr');
        row.innerHTML = '<td class="col-app"><div class="app-cell"><div class="app-cell-info"></div></div></td>' +
            '<td class="col-status"></td>' +
            (showRealtime ? '<td class="col-rate"></td>' : '') +
            '<td class="col-period col-today"></td><td class="col-period col-month"></td><td class="col-total"></td><td class="col-actions"></td>';
        return row;
    };
    var updateTrafficRow = function (row, item, showRealtime, data, index) {
        row.dataset.appId = String(item.app_id || '');
        row.dataset.instanceId = appTrafficInstanceID(item);
        row.dataset.appRowKey = trafficRowKey(item, index);
        updateTrafficAppCell(row.querySelector('.col-app'), item);
        var statusCell = row.querySelector('.col-status');
        statusCell.dataset.label = i18n('status_col');
        setTrafficCellHTML(statusCell, appTrafficControlStatusMarkup(item));
        var rateCell = row.querySelector('.col-rate');
        if (showRealtime) {
            rateCell.dataset.label = i18n('app_traffic_realtime');
            setTrafficCellHTML(rateCell, appTrafficDualValue(item.upload_bps, item.download_bps, true));
        }
        var periods = row.querySelectorAll('.col-period');
        periods[0].dataset.label = i18n('app_traffic_today');
        periods[1].dataset.label = i18n('app_traffic_month');
        setTrafficCellHTML(periods[0], appTrafficDualValue(item.today_upload, item.today_download, false));
        setTrafficCellHTML(periods[1], appTrafficDualValue(item.month_upload, item.month_download, false));
        var total = row.querySelector('.col-total');
        total.dataset.label = i18n('total');
        setTrafficCellHTML(total, appTrafficDualValue(item.total_upload, item.total_download, false));
        var actions = row.querySelector('.col-actions');
        setTrafficCellHTML(actions, appTrafficMoreMenu(item, data.limit_support, activeMenuAppID));
    };
    var updateTrafficMenus = function () {
        if (!latestTrafficData || !Array.isArray(latestTrafficData.apps)) return;
        var apps = new Map(latestTrafficData.apps.map(function (item) { return [appTrafficInstanceID(item), item]; }));
        tbody.querySelectorAll('tr[data-instance-id]').forEach(function (row) {
            var item = apps.get(row.dataset.instanceId || '');
            if (item) setTrafficCellHTML(row.querySelector('.col-actions'), appTrafficMoreMenu(item, latestTrafficData.limit_support, activeMenuAppID));
        });
    };
    var updateTrafficStatus = function (data, list) {
        if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
            state: list.length ? 'fresh' : 'empty',
            count: list.length,
            countLabel: i18n('apps_unit'),
            generatedAt: data.generated_at,
            title: data.note || ''
        });
        if (noteEl) noteEl.textContent = data.note || '';
    };
    var updateRealtimeTrafficCells = function (list, showRealtime) {
        if (!showRealtime || renderedTrafficMode !== true) return false;
        var rows = new Map(Array.from(tbody.querySelectorAll('tr[data-app-row-key]')).map(function (row) {
            return [row.dataset.appRowKey, row];
        }));
        if (rows.size !== list.length) return false;
        for (var index = 0; index < list.length; index++) {
            var item = list[index];
            var row = rows.get(trafficRowKey(item, index));
            if (!row) return false;
            setTrafficCellHTML(row.querySelector('.col-rate'), appTrafficDualValue(item.upload_bps, item.download_bps, true));
        }
        if (state.appTrafficSort.key === 'rate') {
            // Reorder existing rows without replacing them. This preserves
            // hover/focus state for the action menu while keeping rate sort
            // semantics correct.
            var orderedRows = list.map(function (item, index) {
                return rows.get(trafficRowKey(item, index));
            });
            var currentRows = Array.from(tbody.children);
            var needsReorder = orderedRows.some(function (row, index) {
                return currentRows[index] !== row;
            });
            if (needsReorder) {
                var fragment = document.createDocumentFragment();
                orderedRows.forEach(function (row) { fragment.appendChild(row); });
                tbody.appendChild(fragment);
            }
        }
        return true;
    };
    var renderTraffic = function (data, options) {
        options = options || {};
        latestTrafficData = data;
        var list = Array.isArray(data.apps) ? data.apps.slice() : [];
        var showRealtime = syncAppTrafficRealtimeColumn();
        updateSortHeaders();
        var key = state.appTrafficSort.key;
        var direction = state.appTrafficSort.direction;
        list.sort(function (a, b) { return sortAppTrafficRows(a, b, key, direction); });
        var structureSignature = trafficStructureSignature(list, showRealtime, data);
        if (options.realtimeOnly && structureSignature === lastTrafficStructureSignature && updateRealtimeTrafficCells(list, showRealtime)) {
            updateTrafficStatus(data, list);
            return;
        }
        var signature = trafficRenderSignature(list, showRealtime, data);
        if (signature === lastTrafficRenderSignature) {
            updateTrafficStatus(data, list);
            return;
        }
        lastTrafficRenderSignature = signature;
        lastTrafficStructureSignature = structureSignature;
        if (list.length === 0) {
            tbody.innerHTML = '<tr><td colspan="' + (showRealtime ? 7 : 6) + '" class="placeholder">' + i18n('no_app_data') + '</td></tr>';
            renderedTrafficMode = showRealtime;
        } else {
            if (renderedTrafficMode !== showRealtime || tbody.querySelector('.placeholder')) tbody.replaceChildren();
            var existingRows = new Map(Array.from(tbody.querySelectorAll('tr[data-app-row-key]')).map(function (row) {
                return [row.dataset.appRowKey, row];
            }));
            var fragment = document.createDocumentFragment();
            list.forEach(function (item, index) {
                var row = existingRows.get(trafficRowKey(item, index));
                if (!row) row = createTrafficRow(item, showRealtime);
                updateTrafficRow(row, item, showRealtime, data, index);
                fragment.appendChild(row);
            });
            tbody.replaceChildren(fragment);
            renderedTrafficMode = showRealtime;
        }
        updateTrafficStatus(data, list);
    };

    function appTrafficIconKey(item) {
        return String((item && (item.app_id || item.project || item.app_title)) || '').trim();
    }

    // `error` does not bubble, so capture it on the table before the first
    // render. This also catches a cached failure that fires immediately.
    tbody.addEventListener('error', function (event) {
        var image = event.target;
        if (!image || !image.matches || !image.matches('img.app-icon')) return;
        var key = image.dataset.appIconKey || '';
        var url = image.getAttribute('src') || '';
        if (key && url) failedAppIcons[key] = { url: url, at: Date.now() };
        image.style.display = 'none';
    }, true);
    var load = async function (options) {
        options = options || {};
        if (trafficLoading) return;
        trafficLoading = true;
        var silent = !!options.silent;
        if (btn && !silent) btn.disabled = true;
        if (statusEl && !silent) NetwatchShared.setObservationStatus(statusEl, {
            state: latestTrafficData ? 'refreshing' : 'loading'
        });
        if (!silent) document.getElementById('app-traffic-section')?.setAttribute('aria-busy', 'true');
        try {
            var trafficData = await netwatchGet('/api/v1/network/app-traffic');
            renderTraffic(trafficData, { realtimeOnly: silent });
        } catch (e) {
            if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
                state: 'error',
                error: e.message
            });
        } finally {
            trafficLoading = false;
            if (btn && !silent) btn.disabled = false;
            if (!silent) document.getElementById('app-traffic-section')?.removeAttribute('aria-busy');
        }
    };
    window.__app.refreshAppTraffic = load;

    var appForID = function (instanceID) {
        var apps = latestTrafficData && Array.isArray(latestTrafficData.apps) ? latestTrafficData.apps : [];
        return apps.find(function (item) { return appTrafficInstanceID(item) === instanceID; }) || null;
    };
    var closeLimit = function () {
        if (limitWindow) limitWindow.classList.remove('active');
        if (limitBackdrop) limitBackdrop.classList.remove('active');
        activeLimitAppID = '';
        NetwatchShared.unlockModalScroll();
    };
    var openLimit = function (item) {
        var capabilities = item && item.network_policy && item.network_policy.capabilities || {};
        var blocked = item && item.network_policy && item.network_policy.desired && item.network_policy.desired.internet_allowed === false;
        var proxyEnabled = item && item.network_policy && item.network_policy.desired && item.network_policy.desired.proxy_enabled === true;
        if (!item || !item.app_id || blocked || (appTrafficHasHostTarget(item) && proxyEnabled) || capabilities.upload_limit !== true || capabilities.download_limit !== true || !limitWindow) return;
        activeMenuAppID = '';
        activeLimitAppID = appTrafficInstanceID(item);
        updateTrafficMenus();
        var limit = item.limit || {};
        if (limitTitle) limitTitle.textContent = i18n('app_traffic_limit') + ' · ' + appTrafficDisplayName(item);
        if (limitNote) limitNote.textContent = appTrafficInstanceCaption(item);
        appTrafficShowLimitError(limitError, '');
        if (limitUpload) limitUpload.value = appTrafficNumber(limit.upload_kbps) ? appTrafficNumber(limit.upload_kbps) / 1000 : '';
        if (limitDownload) limitDownload.value = appTrafficNumber(limit.download_kbps) ? appTrafficNumber(limit.download_kbps) / 1000 : '';
        appTrafficSetLimitInputValidity(limitUpload);
        appTrafficSetLimitInputValidity(limitDownload);
        if (limitBackdrop) limitBackdrop.classList.add('active');
        limitWindow.classList.add('active');
        NetwatchShared.lockModalScroll();
        setTimeout(function () { if (limitUpload) limitUpload.focus(); }, 0);
    };
    var closeProxy = function () {
        if (proxyWindow) proxyWindow.classList.remove('active');
        if (proxyBackdrop) proxyBackdrop.classList.remove('active');
        activeProxyAppID = '';
        NetwatchShared.unlockModalScroll();
    };
    var openProxy = function (item) {
        var capabilities = item && item.network_policy && item.network_policy.capabilities || {};
        var blocked = item && item.network_policy && item.network_policy.desired && item.network_policy.desired.internet_allowed === false;
        if (!item || !item.app_id || blocked || (appTrafficHasHostTarget(item) && appTrafficHasLimit(item)) || capabilities.proxy_control !== true || !proxyWindow) return;
        activeMenuAppID = '';
        activeProxyAppID = appTrafficInstanceID(item);
        updateTrafficMenus();
		var config = item.network_policy.proxy_settings || state.settings.app_proxy || {};
        if (proxyTitle) proxyTitle.textContent = i18n('app_proxy_set') + ' · ' + appTrafficDisplayName(item);
        if (proxyNote) proxyNote.textContent = appTrafficInstanceCaption(item);
        if (proxyProtocol) {
            proxyProtocol.value = config.protocol || 'socks5';
            if (window.syncCustomSelect) window.syncCustomSelect(proxyProtocol);
        }
        if (proxyHost) {
            proxyHost.value = config.host || '127.0.0.1';
            proxyHost.setAttribute('aria-invalid', 'false');
        }
        if (proxyPort) {
            proxyPort.value = String(config.port || 7890);
            proxyPort.setAttribute('aria-invalid', 'false');
        }
        appTrafficShowLimitError(proxyError, '');
        if (proxyBackdrop) proxyBackdrop.classList.add('active');
        proxyWindow.classList.add('active');
        NetwatchShared.lockModalScroll();
        setTimeout(function () { if (proxyHost) proxyHost.focus(); }, 0);
    };

    var appConnectionEndpoint = function (address, port) {
        address = String(address || '');
        var shown = address.indexOf(':') >= 0 ? '[' + address + ']' : address;
        return NetwatchShared.escapeHtml(shown || '—') + ':' + NetwatchShared.escapeHtml(String(Number(port) || 0));
    };
    var appConnectionDirection = function (direction) {
        if (direction === 'inbound') return i18n('app_connections_inbound');
        if (direction === 'outbound') return i18n('app_connections_outbound');
        return i18n('app_connections_direction_unknown');
    };
    var renderAppConnections = function (data) {
        var rows = data && Array.isArray(data.connections) ? data.connections : [];
        if (connectionsStatus) {
            connectionsStatus.textContent = (data.generated_at || '—') + ' · ' + rows.length + ' ' + i18n('app_connections_count') + (data.truncated ? ' · ' + i18n('app_connections_truncated') : '');
        }
        if (connectionsNote) {
            connectionsNote.textContent = data.note || '';
            connectionsNote.hidden = !data.note;
        }
        if (!connectionsBody) return;
        var renderSignature = JSON.stringify([
            Boolean(data.supported), Boolean(data.truncated), String(data.note || ''), rows
        ]);
        if (renderSignature === lastConnectionsRenderSignature) return;
        lastConnectionsRenderSignature = renderSignature;
        if (!data.supported) {
            connectionsBody.innerHTML = '<div class="app-connections-empty">' + NetwatchShared.escapeHtml(data.note || i18n('app_connections_unavailable')) + '</div>';
            return;
        }
        if (!rows.length) {
            connectionsBody.innerHTML = '<div class="app-connections-empty">' + i18n('app_connections_empty') + '</div>';
            return;
        }
        connectionsBody.innerHTML = rows.map(function (item) {
            var active = item.state === 'ESTABLISHED' || item.state === 'ACTIVE';
            var direction = ['inbound', 'outbound'].indexOf(item.direction) >= 0 ? item.direction : 'unknown';
            var service = item.container_name || item.project || '—';
            var process = item.process_name || '';
            if (process && item.process_pid) process += ' · PID ' + item.process_pid;
            var remoteHost = item.remote_host ? '<small class="app-connection-domain"><span>' + NetwatchShared.escapeHtml(i18n('app_connections_domain')) + '</span><strong title="' + NetwatchShared.escapeHtml(item.remote_host) + '">' + NetwatchShared.escapeHtml(item.remote_host) + '</strong></small>' : '';
            return '<div class="app-connections-row is-' + direction + '" role="row">' +
                '<span class="app-connection-protocol" role="cell"><span>' + NetwatchShared.escapeHtml(String(item.protocol || '').toUpperCase()) + ' · ' + NetwatchShared.escapeHtml(item.ip_version || '') + '</span><small class="app-connection-direction is-' + direction + '">' + NetwatchShared.escapeHtml(appConnectionDirection(direction)) + '</small></span>' +
                '<span class="app-connection-endpoint app-connection-local" role="cell" title="' + appConnectionEndpoint(item.local_address, item.local_port) + '">' + appConnectionEndpoint(item.local_address, item.local_port) + '</span>' +
                '<span class="app-connection-remote" role="cell"><span class="app-connection-endpoint" title="' + appConnectionEndpoint(item.remote_address, item.remote_port) + '">' + appConnectionEndpoint(item.remote_address, item.remote_port) + '</span>' + remoteHost + '</span>' +
                '<span class="app-connection-state' + (active ? ' is-active' : '') + '" role="cell" title="' + NetwatchShared.escapeHtml(item.state || '—') + '">' + NetwatchShared.escapeHtml(item.state || '—') + '</span>' +
                '<span class="app-connection-service" role="cell"><strong title="' + NetwatchShared.escapeHtml(service) + '">' + NetwatchShared.escapeHtml(service) + '</strong>' + (process ? '<small title="' + NetwatchShared.escapeHtml(process) + '">' + NetwatchShared.escapeHtml(process) + '</small>' : '') + '</span>' +
                '</div>';
        }).join('');
    };
    var stopAppConnectionsTimer = function () {
        if (!connectionsTimer) return;
        clearInterval(connectionsTimer);
        connectionsTimer = null;
    };
    var syncAppConnectionsRefreshState = function () {
        var labelKey = connectionsAutoRefresh ? 'app_connections_pause_refresh' : 'app_connections_resume_refresh';
        if (connectionsRefreshToggle) {
            connectionsRefreshToggle.setAttribute('title', i18n(labelKey));
            connectionsRefreshToggle.setAttribute('aria-label', i18n(labelKey));
            connectionsRefreshToggle.setAttribute('aria-pressed', connectionsAutoRefresh ? 'false' : 'true');
            connectionsRefreshToggle.setAttribute('data-i18n-title', labelKey);
            connectionsRefreshToggle.setAttribute('data-i18n-aria-label', labelKey);
            var icon = connectionsRefreshToggle.querySelector('.ui-icon');
            if (icon) {
                icon.classList.toggle('ui-icon--pause', connectionsAutoRefresh);
                icon.classList.toggle('ui-icon--play', !connectionsAutoRefresh);
            }
        }
        if (connectionsLive) {
            var liveKey = connectionsAutoRefresh ? 'app_connections_live' : 'app_connections_paused';
            connectionsLive.textContent = i18n(liveKey);
            connectionsLive.setAttribute('data-i18n', liveKey);
            connectionsLive.classList.toggle('is-paused', !connectionsAutoRefresh);
        }
    };
    var startAppConnectionsTimer = function () {
        stopAppConnectionsTimer();
        if (!connectionsAutoRefresh || !activeConnectionsItem) return;
        connectionsTimer = setInterval(function () {
            if (!document.hidden) loadAppConnections(false);
        }, 3000);
    };
    var setAppConnectionsAutoRefresh = function (enabled) {
        enabled = Boolean(enabled);
        if (connectionsAutoRefresh === enabled) {
            syncAppConnectionsRefreshState();
            return;
        }
        connectionsAutoRefresh = enabled;
        if (enabled) {
            startAppConnectionsTimer();
            loadAppConnections(false);
        } else {
            stopAppConnectionsTimer();
            connectionsReloadPending = false;
            // Freeze the visible snapshot even if an earlier request is still in flight.
            connectionsGeneration++;
        }
        syncAppConnectionsRefreshState();
    };
    var loadAppConnections = async function (manual) {
        if (!activeConnectionsItem) return;
        if (connectionsLoading) {
            connectionsReloadPending = true;
            return;
        }
        connectionsLoading = true;
        var generation = connectionsGeneration;
        if (connectionsStatus && manual) connectionsStatus.textContent = i18n('app_connections_loading');
        try {
            var data = await netwatchGet('/api/v1/network/app-traffic/connections', {
                app_id: activeConnectionsItem.app_id,
                instance_id: appTrafficInstanceID(activeConnectionsItem),
                limit: 300
            });
            if (generation === connectionsGeneration && activeConnectionsAppID) renderAppConnections(data || {});
        } catch (error) {
            if (generation === connectionsGeneration && activeConnectionsAppID) {
                lastConnectionsRenderSignature = '';
                if (connectionsStatus) connectionsStatus.textContent = i18n('app_connections_failed');
                if (connectionsNote) {
                    connectionsNote.textContent = error.message || i18n('app_connections_failed');
                    connectionsNote.hidden = false;
                }
                if (connectionsBody) connectionsBody.innerHTML = '<div class="app-connections-empty">' + NetwatchShared.escapeHtml(error.message || i18n('app_connections_failed')) + '</div>';
            }
        } finally {
            connectionsLoading = false;
            if (connectionsReloadPending && activeConnectionsItem) {
                connectionsReloadPending = false;
                loadAppConnections(false);
            }
        }
    };
    var closeAppConnections = function () {
        stopAppConnectionsTimer();
        connectionsGeneration++;
        connectionsAutoRefresh = true;
        connectionsReloadPending = false;
        lastConnectionsRenderSignature = '';
        activeConnectionsAppID = '';
        activeConnectionsItem = null;
        if (connectionsWindow) connectionsWindow.classList.remove('active');
        if (connectionsBackdrop) connectionsBackdrop.classList.remove('active');
        NetwatchShared.unlockModalScroll();
    };
    var openAppConnections = function (item) {
        if (!item || !item.app_id || !connectionsWindow) return;
        activeMenuAppID = '';
        activeConnectionsItem = item;
        activeConnectionsAppID = appTrafficInstanceID(item);
        connectionsGeneration++;
        connectionsAutoRefresh = true;
        connectionsReloadPending = false;
        lastConnectionsRenderSignature = '';
        updateTrafficMenus();
        if (connectionsTitle) connectionsTitle.textContent = i18n('app_connections_title') + ' · ' + appTrafficDisplayName(item);
        if (connectionsInstance) connectionsInstance.textContent = appTrafficInstanceCaption(item);
        if (connectionsStatus) connectionsStatus.textContent = i18n('app_connections_loading');
        if (connectionsNote) connectionsNote.hidden = true;
        if (connectionsBody) connectionsBody.innerHTML = '<div class="app-connections-empty">' + i18n('app_connections_loading') + '</div>';
        if (connectionsBackdrop) connectionsBackdrop.classList.add('active');
        connectionsWindow.classList.add('active');
        NetwatchShared.lockModalScroll();
        syncAppConnectionsRefreshState();
        loadAppConnections(true);
        startAppConnectionsTimer();
    };

    tbody.addEventListener('click', async function (e) {
        var action = e.target.closest('[data-action]');
        if (!action) return;
        e.stopPropagation();
        var appID = action.dataset.appId || '';
        var instanceID = action.dataset.instanceId || appID;
        if (action.dataset.action === 'traffic-more') {
            activeMenuAppID = activeMenuAppID === instanceID ? '' : instanceID;
            updateTrafficMenus();
        } else if (action.dataset.action === 'traffic-connections') {
            openAppConnections(appForID(instanceID));
        } else if (action.dataset.action === 'traffic-limit') {
			openLimit(appForID(instanceID));
		} else if (action.dataset.action === 'traffic-network') {
            var item = appForID(instanceID);
            if (!item) return;
            action.disabled = true;
            try {
                await appTrafficSetInternetAccess(item, action.dataset.networkState === 'restore');
                NetwatchShared.showToast(i18n('app_traffic_network_updated'), 'success');
                activeMenuAppID = '';
                updateTrafficMenus();
                await load();
			} catch (error) {
                NetwatchShared.showToast(i18n('operation_failed') + ': ' + (error.message || ''), 'error');
                action.disabled = false;
			}
        } else if (action.dataset.action === 'traffic-proxy') {
            var proxyItem = appForID(instanceID);
            if (!proxyItem) return;
            if (action.dataset.proxyState === 'enable') {
                openProxy(proxyItem);
                return;
            }
            action.disabled = true;
            try {
                await netwatchPost('/api/v1/network/app-policy', {
                    app_id: appID,
                    instance_id: instanceID,
                    proxy_enabled: false
                });
                NetwatchShared.showToast(i18n('app_proxy_updated'), 'success');
                activeMenuAppID = '';
                updateTrafficMenus();
                await load();
            } catch (error) {
                NetwatchShared.showToast(i18n('operation_failed') + ': ' + (error.message || ''), 'error');
                action.disabled = false;
            }
        }
    });

    document.addEventListener('click', function (e) {
        if (!activeMenuAppID || e.target.closest('.app-traffic-action-menu')) return;
        activeMenuAppID = '';
        updateTrafficMenus();
    });

    if (limitForm) limitForm.addEventListener('submit', async function (e) {
        e.preventDefault();
        if (!activeLimitAppID) return;
        var upload = appTrafficLimitInputValue(limitUpload);
        var download = appTrafficLimitInputValue(limitDownload);
        appTrafficSetLimitInputValidity(limitUpload);
        appTrafficSetLimitInputValidity(limitDownload);
        if (upload === null || download === null) {
            appTrafficShowLimitError(limitError, i18n('app_traffic_limit_invalid'));
            return;
        }
        appTrafficShowLimitError(limitError, '');
        var submit = limitForm.querySelector('[type="submit"]');
        if (submit) submit.disabled = true;
        try {
            var limitItem = appForID(activeLimitAppID);
            if (!limitItem) throw new Error('application instance is no longer available');
            await netwatchPost('/api/v1/network/app-policy', {
                app_id: limitItem.app_id,
                instance_id: appTrafficInstanceID(limitItem),
                upload_kbps: Math.round(upload * 1000),
                download_kbps: Math.round(download * 1000)
            });
            NetwatchShared.showToast(i18n('app_traffic_limit_saved'), 'success');
            closeLimit();
            await load();
        } catch (error) {
            NetwatchShared.showToast(i18n('operation_failed') + ': ' + (error.message || ''), 'error');
        } finally {
            if (submit) submit.disabled = false;
        }
    });
    if (proxyForm) proxyForm.addEventListener('submit', async function (e) {
        e.preventDefault();
        if (!activeProxyAppID) return;
        var config = {
            protocol: (proxyProtocol && proxyProtocol.value) || 'socks5',
            host: String(proxyHost && proxyHost.value || '').trim(),
            port: Number(proxyPort && proxyPort.value)
        };
        var hostValid = !!config.host;
        var portValid = Number.isInteger(config.port) && config.port >= 1 && config.port <= 65535 && (!proxyPort || proxyPort.validity.valid);
        if (proxyHost) proxyHost.setAttribute('aria-invalid', hostValid ? 'false' : 'true');
        if (proxyPort) proxyPort.setAttribute('aria-invalid', portValid ? 'false' : 'true');
        if (!hostValid || !portValid) {
            appTrafficShowLimitError(proxyError, i18n('app_proxy_invalid'));
            return;
        }
        appTrafficShowLimitError(proxyError, '');
        var submit = proxyForm.querySelector('[type="submit"]');
        if (submit) submit.disabled = true;
        try {
			var proxyItem = appForID(activeProxyAppID);
			if (!proxyItem) throw new Error('application instance is no longer available');
			await netwatchPost('/api/v1/network/app-policy', {
				app_id: proxyItem.app_id,
				instance_id: appTrafficInstanceID(proxyItem),
				proxy_enabled: true,
				proxy_settings: config
            });
            NetwatchShared.showToast(i18n('app_proxy_updated'), 'success');
            closeProxy();
            await load();
        } catch (error) {
            appTrafficShowLimitError(proxyError, error.message || i18n('operation_failed'));
            NetwatchShared.showToast(i18n('operation_failed') + ': ' + (error.message || ''), 'error');
        } finally {
            if (submit) submit.disabled = false;
        }
    });
    var closeLimitButton = document.getElementById('close-app-traffic-limit-window');
    if (closeLimitButton) closeLimitButton.addEventListener('click', closeLimit);
    if (limitBackdrop) limitBackdrop.addEventListener('click', closeLimit);
    var closeProxyButton = document.getElementById('close-app-traffic-proxy-window');
    if (closeProxyButton) closeProxyButton.addEventListener('click', closeProxy);
    if (proxyBackdrop) proxyBackdrop.addEventListener('click', closeProxy);
    var closeConnectionsButton = document.getElementById('close-app-connections-window');
    if (closeConnectionsButton) closeConnectionsButton.addEventListener('click', closeAppConnections);
    if (connectionsBackdrop) connectionsBackdrop.addEventListener('click', closeAppConnections);
    if (connectionsRefreshToggle) connectionsRefreshToggle.addEventListener('click', function () {
        setAppConnectionsAutoRefresh(!connectionsAutoRefresh);
    });
    [proxyHost, proxyPort].forEach(function (input) {
        if (input) input.addEventListener('input', function () {
            input.setAttribute('aria-invalid', 'false');
            appTrafficShowLimitError(proxyError, '');
        });
    });
    [limitUpload, limitDownload].forEach(function (input) {
        if (input) input.addEventListener('input', function () {
            if (appTrafficSetLimitInputValidity(input)) {
                if (appTrafficLimitInputValue(limitUpload) !== null && appTrafficLimitInputValue(limitDownload) !== null) {
                    appTrafficShowLimitError(limitError, '');
                }
                return;
            }
            appTrafficShowLimitError(limitError, i18n('app_traffic_limit_invalid'));
        });
    });
    document.addEventListener('keydown', function (e) {
        if (e.key !== 'Escape') return;
        if (limitWindow && limitWindow.classList.contains('active')) closeLimit();
        if (proxyWindow && proxyWindow.classList.contains('active')) closeProxy();
        if (connectionsWindow && connectionsWindow.classList.contains('active')) closeAppConnections();
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
    if (btn) btn.addEventListener('click', function () { load(); });
    window.__app.updateAppTrafficRealtime = function () {
        if (state.appTrafficRealtimeTimer) {
            clearInterval(state.appTrafficRealtimeTimer);
            state.appTrafficRealtimeTimer = null;
        }
        var enabled = syncAppTrafficRealtimeColumn();
        if (latestTrafficData) renderTraffic(latestTrafficData);
        if (document.hidden || !enabled) return;
        state.appTrafficRealtimeTimer = setInterval(function () { load({ silent: true }); }, 2000);
    };
    document.addEventListener('visibilitychange', function () {
        window.__app.updateAppTrafficRealtime();
        if (!document.hidden && state.settings.app_traffic_realtime_enabled !== false) load({ silent: true });
    });
    window.__app.updateAppTrafficRealtime();
    load();

}

function appTrafficNumber(value) {
    return Number(value) || 0;
}

function appTrafficInstanceID(item) {
    return String(item && (item.instance_id || item.app_id) || '').trim();
}

function appTrafficDisplayName(item) {
    if (!item) return '';
    var name = item.app_title || item.app_id || item.project || '';
    var instanceLabel = item.multi_instance && (item.user_id || item.project);
    if (instanceLabel) name += '（' + instanceLabel + '）';
    return name;
}

function appTrafficInstanceCaption(item) {
    if (!item) return '';
    var instanceLabel = item.multi_instance && (item.user_id || item.project);
    return instanceLabel ? item.app_id + ' · ' + instanceLabel : item.app_id;
}

function appTrafficPeriod(item, period) {
    return appTrafficNumber(item[period + '_upload']) + appTrafficNumber(item[period + '_download']);
}

function appTrafficRate(item) {
    return appTrafficNumber(item.upload_bps) + appTrafficNumber(item.download_bps);
}

function formatAppTrafficLimit(kbps) {
    kbps = Math.max(0, Math.round(appTrafficNumber(kbps)));
    var mbps = kbps / 1000;
    return mbps >= 10 ? mbps.toFixed(0) : mbps.toFixed(1).replace(/\.0$/, '');
}

function appTrafficCompactStatusLabel(key, zhFallback, enFallback) {
    var value = i18n(key);
    if (!value || value === key) {
        var lang = String(document.documentElement && document.documentElement.lang || '').toLowerCase();
        return lang.indexOf('en') === 0 ? enFallback : zhFallback;
    }
    return value;
}

function appTrafficControlStatusMarkup(item) {
    var parts = [];
    var limit = item && item.limit || {};
    var uploadLimit = appTrafficNumber(limit.upload_kbps);
    var downloadLimit = appTrafficNumber(limit.download_kbps);
    var capabilities = item && item.network_policy && item.network_policy.capabilities || {};
    var policy = item && item.network_policy || {};
    if ((uploadLimit > 0 || downloadLimit > 0) && capabilities.upload_limit === true && capabilities.download_limit === true) {
        var limitValues = [];
        if (uploadLimit > 0) limitValues.push('↑ ' + formatAppTrafficLimit(uploadLimit));
        if (downloadLimit > 0) limitValues.push('↓ ' + formatAppTrafficLimit(downloadLimit));
        var limitStatusLabel = appTrafficCompactStatusLabel('app_traffic_limit_status', '限速', 'Limited');
        var limitInSync = policy.limit_in_sync !== false;
        var limitTitle = limitInSync ? i18n('app_traffic_limit') : (policy.diagnostic || i18n('app_traffic_limit'));
        parts.push('<span class="app-control-status app-control-status-limit' + (limitInSync ? '' : ' app-control-status--drifted') + '" title="' + NetwatchShared.escapeHtml(limitTitle) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--gauge" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(limitStatusLabel) + '</span></span>' +
            '<span class="app-control-status-values">' + NetwatchShared.escapeHtml(limitValues.join(' · ')) + '</span></span>');
    }
    var desiredBlocked = policy.desired && policy.desired.internet_allowed === false;
    if (desiredBlocked) {
        var blockedStatusLabel = appTrafficCompactStatusLabel('app_traffic_internet_disabled_status', '禁用外网', 'Internet blocked');
        var internetInSync = policy.internet_in_sync !== false && policy.internet_state === 'blocked';
        var blockedTitle = internetInSync ? i18n('app_traffic_internet_disabled') : (policy.diagnostic || i18n('app_traffic_internet_disabled'));
        parts.push('<span class="app-control-status app-control-status-blocked' + (internetInSync ? '' : ' app-control-status--drifted') + '" title="' + NetwatchShared.escapeHtml(blockedTitle) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--network" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(blockedStatusLabel) + '</span></span></span>');
	} else if (policy.internet_state === 'partial' && capabilities.internet_control === true) {
        var internetDriftLabel = appTrafficCompactStatusLabel('app_traffic_internet_policy_drifted', '外网策略异常', 'Internet policy drift');
        parts.push('<span class="app-control-status app-control-status--drifted" title="' + NetwatchShared.escapeHtml(policy.diagnostic || internetDriftLabel) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--network" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(internetDriftLabel) + '</span></span></span>');
    }
    var proxyEnabled = policy.desired && policy.desired.proxy_enabled === true;
    if (proxyEnabled) {
        var proxyPaused = policy.proxy_state === 'paused';
        var proxyLabel = appTrafficCompactStatusLabel(proxyPaused ? 'app_proxy_paused_status' : 'app_proxy_status', proxyPaused ? '代理暂停' : '代理', proxyPaused ? 'Proxy paused' : 'Proxy');
        parts.push('<span class="app-control-status app-control-status-proxy' + (policy.proxy_in_sync === false ? ' app-control-status--drifted' : '') + '" title="' + NetwatchShared.escapeHtml(proxyPaused ? i18n('app_proxy_paused') : i18n('app_proxy_enabled')) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--route" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(proxyLabel) + '</span></span></span>');
    }
    return parts.length ? '<div class="app-control-status-row">' + parts.join('') + '</div>' : '';
}

function appTrafficHasHostTarget(item) {
    var modes = item && Array.isArray(item.network_modes) ? item.network_modes : [];
    if (modes.some(function (mode) { return String(mode).toLowerCase() === 'host'; })) return true;
    var targets = item && Array.isArray(item.network_targets) ? item.network_targets : [];
    return targets.some(function (target) {
        return target && (target.kind === 'cgroup' || target.network_mode === 'host');
    });
}

function appTrafficHasLimit(item) {
    var limit = item && item.limit || {};
    return appTrafficNumber(limit.upload_kbps) > 0 || appTrafficNumber(limit.download_kbps) > 0;
}

function appTrafficMoreMenu(item, limitSupported, activeAppID) {
    var appID = item && item.app_id;
    if (!appID) return '';
    var instanceID = appTrafficInstanceID(item);
    var policy = item.network_policy || {};
    var capabilities = policy.capabilities || {};
    var limitAllowed = capabilities.upload_limit === true && capabilities.download_limit === true;
    var internetAllowed = capabilities.internet_control === true;
    var proxyAllowed = capabilities.proxy_control === true;
    var blocked = policy.desired && policy.desired.internet_allowed === false;
    var proxyEnabled = policy.desired && policy.desired.proxy_enabled === true;
    var hostTarget = appTrafficHasHostTarget(item);
    var limited = appTrafficHasLimit(item);
    var showLimit = limitSupported && limitAllowed && !blocked && !(hostTarget && proxyEnabled);
    var showProxy = proxyAllowed && (!blocked || proxyEnabled) && (proxyEnabled || !hostTarget || !limited);
    var open = activeAppID === instanceID;
    var identityAttrs = ' data-app-id="' + NetwatchShared.escapeHtml(appID) + '" data-instance-id="' + NetwatchShared.escapeHtml(instanceID) + '"';
    var menu = '<button type="button" class="icon-button app-traffic-more-btn" data-action="traffic-more"' + identityAttrs + ' aria-label="' + i18n('app_traffic_more') + '" title="' + i18n('app_traffic_more') + '" aria-expanded="' + (open ? 'true' : 'false') + '"><span class="ui-icon ui-icon--more" aria-hidden="true"></span></button>';
    if (!open) return '<div class="app-traffic-action-menu">' + menu + '</div>';
    var panel = '<div class="app-traffic-more-panel" role="menu">' +
        '<button type="button" class="app-traffic-menu-item" role="menuitem" data-action="traffic-connections"' + identityAttrs + '><span class="ui-icon ui-icon--connections" aria-hidden="true"></span><span>' + i18n('app_connections_title') + '</span></button>';
    var controls = '';
    // The experimental switch gates Host/Mixed policing; read-only accounting
    // stays active regardless of that setting.
	if (showLimit) {
		controls += '<button type="button" class="app-traffic-menu-item" role="menuitem" data-action="traffic-limit"' + identityAttrs + '><span class="ui-icon ui-icon--gauge" aria-hidden="true"></span><span>' + i18n('app_traffic_limit') + '</span></button>';
	}
    if (showProxy) {
		controls += '<button type="button" class="app-traffic-menu-item' + (proxyEnabled ? ' restore' : '') + '" role="menuitem" data-action="traffic-proxy"' + identityAttrs + ' data-proxy-state="' + (proxyEnabled ? 'disable' : 'enable') + '"><span class="ui-icon ui-icon--route" aria-hidden="true"></span><span>' + (proxyEnabled ? i18n('app_proxy_restore_direct') : i18n('app_proxy_set')) + '</span></button>';
    }
    if (internetAllowed) {
		controls += '<button type="button" class="app-traffic-menu-item ' + (blocked ? 'restore' : 'danger') + '" role="menuitem" data-action="traffic-network"' + identityAttrs + ' data-network-state="' + (blocked ? 'restore' : 'disable') + '"><span class="ui-icon ui-icon--network" aria-hidden="true"></span><span>' + (blocked ? i18n('app_traffic_restore_internet') : i18n('app_traffic_disable_internet')) + '</span></button>';
    }
	if (controls) panel += '<div class="app-traffic-menu-divider" role="separator"></div>' + controls;
    return '<div class="app-traffic-action-menu">' + menu + panel + '</div></div>';
}

async function appTrafficSetInternetAccess(item, restore) {
    var capabilities = item && item.network_policy && item.network_policy.capabilities || {};
    if (!item || !item.app_id || capabilities.internet_control !== true) throw new Error('internet control is not allowed');
    await netwatchPost('/api/v1/network/app-policy', {
        app_id: item.app_id,
        instance_id: appTrafficInstanceID(item),
        internet_allowed: restore === true
    });
}

function appTrafficDualValue(upload, download, rate) {
    var format = rate ? function (v) { return window.__app.formatBitsPerSec(appTrafficNumber(v)); } : NetwatchShared.formatBytes;
    return '<div class="app-traffic-direction"><span class="app-traffic-value">' + format(appTrafficNumber(upload)) + '</span><span class="app-traffic-dir up">↑</span></div>' +
        '<div class="app-traffic-direction"><span class="app-traffic-value">' + format(appTrafficNumber(download)) + '</span><span class="app-traffic-dir down">↓</span></div>';
}

function appTrafficLimitInputValue(input) {
    if (!input) return 0;
    var raw = String(input.value || '').trim();
    if (raw === '') return 0;
    var value = Number(raw);
    if (!Number.isFinite(value) || value < 0 || value > 10000 || !input.validity.valid) return null;
    return value;
}

function appTrafficSetLimitInputValidity(input) {
    if (!input) return true;
    var valid = appTrafficLimitInputValue(input) !== null;
    input.setAttribute('aria-invalid', valid ? 'false' : 'true');
    return valid;
}

function appTrafficShowLimitError(el, message) {
    if (!el) return;
    el.textContent = message || '';
    el.hidden = !message;
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
            if (statusEl) NetwatchShared.setObservationStatus(statusEl, { state: 'empty', count: 0, countLabel: i18n('interfaces_unit'), generatedAt: data && data.timestamp });
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
        if (statusEl) NetwatchShared.setObservationStatus(statusEl, {
            state: 'fresh',
            count: nics.length,
            countLabel: i18n('interfaces_unit'),
            generatedAt: data.timestamp
        });
    };
    var tick = async function (manual) {
        if (manual === undefined) manual = false;
        try {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = true;
            if (statusEl && manual) NetwatchShared.setObservationStatus(statusEl, { state: 'refreshing' });
            // force=1: backend double-samples so first paint already has bps
            var path = manual ? '/api/v1/network/realtime?force=1' : '/api/v1/network/realtime';
            var realtimeData = await netwatchGet(path);
            window.renderNICRealtime(realtimeData);
        } catch (error) {
            if (statusEl) NetwatchShared.setObservationStatus(statusEl, { state: 'error', error: error && error.message });
        } finally {
            if (manual && els.nicRealtimeRefreshBtn) els.nicRealtimeRefreshBtn.disabled = false;
        }
    };
    updateNICRealtimeRefreshButton();
    if (els.nicRealtimeRefreshBtn) {
        els.nicRealtimeRefreshBtn.addEventListener('click', function () { tick(true); });
    }
    // Page startup reads the lifecycle sampler's current snapshot. Only the
    // card refresh button performs a forced double-sample.
    tick(false);
    if (window.__app.applySettingsToForm) window.__app.applySettingsToForm();
}

function initTrace() {
    if (state.traceInitialized) return;
    state.traceInitialized = true;
    var btn = document.getElementById('trace-run');
    var stopBtn = document.getElementById('trace-stop');
    var winStopBtn = document.getElementById('trace-window-stop');
    var input = document.getElementById('trace-host');
    var out = document.getElementById('trace-output');
    var summary = document.getElementById('trace-summary');
    var detailsBtn = document.getElementById('trace-details-btn');
    if (!btn) return;

    var setStopVisible = function (visible) {
        [stopBtn, winStopBtn].forEach(function (el) {
            if (!el) return;
            el.hidden = !visible;
            el.disabled = false;
        });
        btn.disabled = !!visible;
    };

    var renderTraceSummary = function (items) {
        if (!summary) return;
        summary.innerHTML = items.map(function (item) {
            return '<div class="trace-summary-item"><span class="trace-summary-label">' + item.label + '</span><span class="trace-summary-value">' + item.value + '</span></div>';
        }).join('');
    };

    var statusLabelFor = function (data, host) {
        if (!data) return i18n('trace_not_started');
        if (data.error === 'cancelled') return i18n('trace_stopped');
        if (data.error) return i18n('failed');
        if (data.running) return i18n('tracing');
        var hops = (data.hops || []).length;
        if (hops > 0) return i18n('trace_done') + ' · ' + hops + ' ' + i18n('hops');
        return i18n('trace_done');
    };

    var applyTraceUI = function (data, host) {
        host = host || (data && data.target) || (input && input.value) || '';
        state.traceResult = data || null;
        renderTraceSummary([
            { label: i18n('target'), value: (data && data.target) || host || '—' },
            { label: i18n('status_col'), value: statusLabelFor(data, host) },
            { label: i18n('tool'), value: (data && data.tool) || 'mtr' }
        ]);
        if (out) {
            if (!data) {
                out.innerHTML = '<div class="trace-empty">' + i18n('trace_not_started') + '</div>';
            } else if (data.error && data.error !== 'cancelled' && !(data.hops || []).length) {
                out.innerHTML = '<div class="trace-empty">' + i18n('error') + ': ' + NetwatchShared.escapeHtml(data.error) + '</div>';
            } else {
                renderTraceRows(data);
            }
        }
        if (detailsBtn) {
            var hasHops = !!(data && (data.hops || []).length);
            detailsBtn.disabled = !hasHops && !(data && data.running);
        }
        setStopVisible(!!(data && data.running));
    };

    var renderTraceRows = function (data) {
        if (!out) return;
        var hops = Array.isArray(data.hops) ? data.hops : [];
        if (hops.length === 0) {
            out.innerHTML = '<div class="trace-empty">' +
                (data.running ? i18n('collecting_trace') : (data.error === 'cancelled' ? i18n('trace_stopped') : i18n('trace_no_hops'))) +
                '</div>';
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

    var stopPoller = function () {
        if (state.tracePoller) {
            clearInterval(state.tracePoller);
            state.tracePoller = null;
        }
    };

    var stopTrace = async function () {
        stopPoller();
        try {
            if (window.NetwatchAPI) {
                await window.NetwatchAPI.post('/api/v1/diagnostics/trace/cancel');
            } else {
                await fetch('/api/v1/diagnostics/trace/cancel', { method: 'POST', cache: 'no-store' });
            }
        } catch (_) {}
        var data = state.traceResult || {};
        data = Object.assign({}, data, { running: false, finished: true, error: data.error || 'cancelled' });
        applyTraceUI(data, data.target || (input && input.value) || '');
        setStopVisible(false);
        if (btn) btn.disabled = false;
    };

    var run = async function () {
        var host = (input && input.value || '').trim();
        if (!host) return;
        stopPoller();
        state.traceResult = null;
        setStopVisible(true);
        if (detailsBtn) detailsBtn.disabled = true;
        window.__app.openTraceWindow();
        applyTraceUI({ target: host, tool: 'mtr', running: true, hops: [] }, host);
        if (out) out.innerHTML = '<div class="trace-empty">' + i18n('collecting_trace') + '</div>';
        try {
            if (window.NetwatchAPI) {
                await window.NetwatchAPI.post('/api/v1/diagnostics/trace?host=' + encodeURIComponent(host));
            } else {
                await fetch('/api/v1/diagnostics/trace?host=' + encodeURIComponent(host), { method: 'POST', cache: 'no-store' });
            }
            var poll = async function () {
                try {
                    var data = await netwatchGet('/api/v1/diagnostics/trace/task');
                    applyTraceUI(data, host);
                    if (!data.running) {
                        stopPoller();
                        setStopVisible(false);
                        if (btn) btn.disabled = false;
                    }
                } catch (err) {
                    stopPoller();
                    setStopVisible(false);
                    if (btn) btn.disabled = false;
                    renderTraceSummary([
                        { label: i18n('target'), value: host },
                        { label: i18n('status_col'), value: i18n('request_failed') },
                        { label: i18n('tool'), value: 'mtr' }
                    ]);
                }
            };
            await poll();
            if (state.traceResult && state.traceResult.running) {
                stopPoller();
                state.tracePoller = setInterval(poll, 1000);
            }
        } catch (e) {
            setStopVisible(false);
            if (btn) btn.disabled = false;
            renderTraceSummary([
                { label: i18n('target'), value: host },
                { label: i18n('status_col'), value: i18n('request_failed') },
                { label: i18n('tool'), value: 'mtr' }
            ]);
            if (out) out.innerHTML = '<div class="trace-empty">' + i18n('request_failed') + ': ' + NetwatchShared.escapeHtml(e.message || '') + '</div>';
        }
    };

    btn.addEventListener('click', function () { run(); });
    if (stopBtn) stopBtn.addEventListener('click', function () { stopTrace(); });
    if (winStopBtn) winStopBtn.addEventListener('click', function () { stopTrace(); });
    if (detailsBtn) detailsBtn.addEventListener('click', function () { window.__app.openTraceWindow(); });
    if (input) input.addEventListener('keydown', function (event) {
        if (event.key === 'Enter') {
            event.preventDefault();
            run();
        }
    });

    window.__app.stopTrace = stopTrace;
    window.__app.applyTraceUI = applyTraceUI;
}

function updateNICRealtimeRefreshButton() {
    if (!els.nicRealtimeRefreshBtn) return;
    // Always show manual refresh; SSE/auto sampling does not replace an immediate re-sample.
    els.nicRealtimeRefreshBtn.style.display = '';
    els.nicRealtimeRefreshBtn.title = i18n('refresh_btn');
    els.nicRealtimeRefreshBtn.setAttribute('aria-label', i18n('refresh_btn'));
}

function refreshAppTrafficSoon() {
    // Host bridge create briefly rewires the default route; give counters/docker a beat.
    var run = function () {
        if (window.__app && typeof window.__app.refreshAppTraffic === 'function') {
            window.__app.refreshAppTraffic();
        }
    };
    setTimeout(run, 400);
    setTimeout(run, 2000);
}

window.__app.initAppTraffic = initAppTraffic;
window.__app.initNICRealtime = initNICRealtime;
window.__app.initTrace = initTrace;
window.__app.updateNICRealtimeRefreshButton = updateNICRealtimeRefreshButton;
window.__app.refreshAppTrafficSoon = refreshAppTrafficSoon;
window.__app.appTrafficHasHostTarget = appTrafficHasHostTarget;
window.__app.appTrafficHasLimit = appTrafficHasLimit;
window.__app.appTrafficMoreMenu = appTrafficMoreMenu;
})();
