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
    var limitWindow = document.getElementById('app-traffic-limit-window');
    var limitBackdrop = document.getElementById('app-traffic-limit-backdrop');
    var limitTitle = document.getElementById('app-traffic-limit-title');
    var limitNote = document.getElementById('app-traffic-limit-note');
    var limitForm = document.getElementById('app-traffic-limit-form');
    var limitUpload = document.getElementById('app-traffic-limit-upload');
    var limitDownload = document.getElementById('app-traffic-limit-download');
    var limitError = document.getElementById('app-traffic-limit-error');
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
        return String(item.app_title || item.app_id || item.project || '').toLowerCase();
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
        return String((item && (item.app_id || item.project || item.app_title)) || ('row-' + index));
    };
    var trafficRenderSignature = function (list, showRealtime, data, containerMap) {
        return JSON.stringify({
            mode: showRealtime,
            sort: state.appTrafficSort.key + ':' + state.appTrafficSort.direction,
            menu: activeMenuAppID,
            limit: !!data.limit_support,
            containers: containerMap,
            apps: list.map(function (item, index) {
                return [
                    trafficRowKey(item, index), item.icon || '', item.app_title || '', item.status_text || '',
                    item.upload_bps || 0, item.download_bps || 0,
                    item.today_upload || 0, item.today_download || 0,
                    item.month_upload || 0, item.month_download || 0,
                    item.total_upload || 0, item.total_download || 0,
                    item.limit || null, item.bridges || []
                ];
            })
        });
    };
    var trafficStructureSignature = function (list, showRealtime, data, containerMap) {
        return JSON.stringify({
            mode: showRealtime,
            sort: state.appTrafficSort.key + ':' + state.appTrafficSort.direction,
            limit: !!data.limit_support,
            containers: containerMap,
            apps: list.map(function (item, index) {
                return [
                    trafficRowKey(item, index), item.icon || '', item.app_title || '', item.status_text || '',
                    item.limit || null, item.bridges || []
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
        var name = item.app_title || item.app_id || item.project || i18n('unknown_status');
        setTrafficCellHTML(info, '<strong>' + NetwatchShared.escapeHtml(name) + '</strong>' +
            (item.status_text ? '<div class="app-status-text">' + NetwatchShared.escapeHtml(item.status_text) + '</div>' : ''));
    };
    var createTrafficRow = function (item, showRealtime) {
        var row = document.createElement('tr');
        row.innerHTML = '<td class="col-app"><div class="app-cell"><div class="app-cell-info"></div></div></td>' +
            '<td class="col-status"></td>' +
            (showRealtime ? '<td class="col-rate"></td>' : '') +
            '<td class="col-period"></td><td class="col-period"></td><td class="col-total"></td><td class="col-actions"></td>';
        return row;
    };
    var updateTrafficRow = function (row, item, showRealtime, data, containerMap, index) {
        row.dataset.appId = String(item.app_id || '');
        row.dataset.appRowKey = trafficRowKey(item, index);
        updateTrafficAppCell(row.querySelector('.col-app'), item);
        setTrafficCellHTML(row.querySelector('.col-status'), appTrafficControlStatusMarkup(item, containerMap));
        var rateCell = row.querySelector('.col-rate');
        if (showRealtime) setTrafficCellHTML(rateCell, appTrafficDualValue(item.upload_bps, item.download_bps, true));
        var periods = row.querySelectorAll('.col-period');
        setTrafficCellHTML(periods[0], appTrafficDualValue(item.today_upload, item.today_download, false));
        setTrafficCellHTML(periods[1], appTrafficDualValue(item.month_upload, item.month_download, false));
        var total = row.querySelector('.col-total');
        setTrafficCellHTML(total, appTrafficDualValue(item.total_upload, item.total_download, false));
        var actions = row.querySelector('.col-actions');
        setTrafficCellHTML(actions, appTrafficMoreMenu(item, data.limit_support, containerMap, activeMenuAppID));
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
        var containerMap = appTrafficContainerMap();
        var list = Array.isArray(data.apps) ? data.apps.slice() : [];
        var showRealtime = syncAppTrafficRealtimeColumn();
        updateSortHeaders();
        var key = state.appTrafficSort.key;
        var direction = state.appTrafficSort.direction;
        list.sort(function (a, b) { return sortAppTrafficRows(a, b, key, direction); });
        var structureSignature = trafficStructureSignature(list, showRealtime, data, containerMap);
        if (options.realtimeOnly && structureSignature === lastTrafficStructureSignature && updateRealtimeTrafficCells(list, showRealtime)) {
            updateTrafficStatus(data, list);
            return;
        }
        var signature = trafficRenderSignature(list, showRealtime, data, containerMap);
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
                updateTrafficRow(row, item, showRealtime, data, containerMap, index);
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
            if ((options.refreshContainers || !latestTrafficData) && window.__app.fetchContainers) {
                await window.__app.fetchContainers().catch(function () {});
            }
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

    var appForID = function (appID) {
        var apps = latestTrafficData && Array.isArray(latestTrafficData.apps) ? latestTrafficData.apps : [];
        return apps.find(function (item) { return item.app_id === appID; }) || null;
    };
    var closeLimit = function () {
        if (limitWindow) limitWindow.classList.remove('active');
        if (limitBackdrop) limitBackdrop.classList.remove('active');
        activeLimitAppID = '';
        NetwatchShared.unlockModalScroll();
    };
    var openLimit = function (item) {
        if (!item || !item.app_id || !limitWindow) return;
        activeMenuAppID = '';
        activeLimitAppID = item.app_id;
        if (latestTrafficData) renderTraffic(latestTrafficData);
        var limit = item.limit || {};
        if (limitTitle) limitTitle.textContent = i18n('app_traffic_limit') + ' · ' + (item.app_title || item.app_id);
        if (limitNote) limitNote.textContent = item.app_id;
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

    tbody.addEventListener('click', async function (e) {
        var action = e.target.closest('[data-action]');
        if (!action) return;
        e.stopPropagation();
        var appID = action.dataset.appId || '';
        if (action.dataset.action === 'traffic-more') {
            activeMenuAppID = activeMenuAppID === appID ? '' : appID;
            if (latestTrafficData) renderTraffic(latestTrafficData);
        } else if (action.dataset.action === 'traffic-limit') {
            openLimit(appForID(appID));
        } else if (action.dataset.action === 'traffic-network') {
            var item = appForID(appID);
            if (!item) return;
            action.disabled = true;
            try {
                await appTrafficSetInternetAccess(item, action.dataset.networkState === 'restore');
                NetwatchShared.showToast(i18n('app_traffic_network_updated'), 'success');
                activeMenuAppID = '';
                await load({ refreshContainers: true });
            } catch (error) {
                NetwatchShared.showToast(i18n('operation_failed') + ': ' + (error.message || ''), 'error');
                action.disabled = false;
            }
        }
    });

    document.addEventListener('click', function (e) {
        if (!activeMenuAppID || e.target.closest('.app-traffic-action-menu')) return;
        activeMenuAppID = '';
        if (latestTrafficData) renderTraffic(latestTrafficData);
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
            await netwatchPost('/api/v1/network/app-traffic/limit', {
                app_id: activeLimitAppID,
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
    var closeLimitButton = document.getElementById('close-app-traffic-limit-window');
    if (closeLimitButton) closeLimitButton.addEventListener('click', closeLimit);
    if (limitBackdrop) limitBackdrop.addEventListener('click', closeLimit);
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
    if (btn) btn.addEventListener('click', function () { load({ refreshContainers: true }); });
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
    load({ refreshContainers: true });

}

function appTrafficNumber(value) {
    return Number(value) || 0;
}

function appTrafficPeriod(item, period) {
    return appTrafficNumber(item[period + '_upload']) + appTrafficNumber(item[period + '_download']);
}

function appTrafficRate(item) {
    return appTrafficNumber(item.upload_bps) + appTrafficNumber(item.download_bps);
}

function formatAppTrafficLimit(kbps) {
    kbps = Math.max(0, Math.round(appTrafficNumber(kbps)));
    if (kbps >= 1000) {
        var mbps = kbps / 1000;
        return (mbps >= 10 ? mbps.toFixed(0) : mbps.toFixed(1).replace(/\.0$/, '')) + ' Mbit/s';
    }
    return kbps + ' kbit/s';
}

function appTrafficContainerMap() {
    var map = {};
    if (!window.__app.lastContainerData || !Array.isArray(window.__app.lastContainerData.applications)) return map;
    window.__app.lastContainerData.applications.forEach(function (app) {
        if (app && app.bridge) map[app.bridge] = app.block_mode || '';
    });
    return map;
}

function appTrafficControllableBridges(item) {
    return (Array.isArray(item && item.bridges) ? item.bridges : []).filter(function (bridge) {
        return String(bridge).indexOf('lzc-br-') === 0;
    });
}

function appTrafficControlStatusMarkup(item, containerMap) {
    var parts = [];
    var limit = item && item.limit || {};
    var uploadLimit = appTrafficNumber(limit.upload_kbps);
    var downloadLimit = appTrafficNumber(limit.download_kbps);
    if (uploadLimit > 0 || downloadLimit > 0) {
        var limitValues = [];
        if (uploadLimit > 0) limitValues.push('↑ ' + formatAppTrafficLimit(uploadLimit));
        if (downloadLimit > 0) limitValues.push('↓ ' + formatAppTrafficLimit(downloadLimit));
        parts.push('<span class="app-control-status app-control-status-limit" title="' + NetwatchShared.escapeHtml(i18n('app_traffic_limit')) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--gauge" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(i18n('app_traffic_limit_status')) + '</span></span>' +
            '<span class="app-control-status-values">' + NetwatchShared.escapeHtml(limitValues.join(' · ')) + '</span></span>');
    }
    var bridges = appTrafficControllableBridges(item);
    var blocked = bridges.some(function (bridge) { return !!(containerMap && containerMap[bridge]); });
    if (blocked) {
        parts.push('<span class="app-control-status app-control-status-blocked" title="' + NetwatchShared.escapeHtml(i18n('app_traffic_internet_disabled')) + '">' +
            '<span class="app-control-status-label"><span class="ui-icon ui-icon--network" aria-hidden="true"></span><span class="app-control-status-label-text">' + NetwatchShared.escapeHtml(i18n('app_traffic_internet_disabled_status')) + '</span></span></span>');
    }
    return parts.length ? '<div class="app-control-status-row">' + parts.join('') + '</div>' : '';
}

function appTrafficMoreMenu(item, limitSupported, containerMap, activeAppID) {
    var appID = item && item.app_id;
    if (!appID) return '';
    var bridges = appTrafficControllableBridges(item);
    var blocked = bridges.some(function (bridge) { return !!containerMap[bridge]; });
    var open = activeAppID === appID;
    var menu = '<button type="button" class="icon-button app-traffic-more-btn" data-action="traffic-more" data-app-id="' + NetwatchShared.escapeHtml(appID) + '" aria-label="' + i18n('app_traffic_more') + '" title="' + i18n('app_traffic_more') + '" aria-expanded="' + (open ? 'true' : 'false') + '"><span class="ui-icon ui-icon--more" aria-hidden="true"></span></button>';
    if (!limitSupported && !bridges.length) return '';
    if (!open) return '<div class="app-traffic-action-menu">' + menu + '</div>';
    var panel = '<div class="app-traffic-more-panel" role="menu">';
    if (limitSupported) {
        panel += '<button type="button" class="app-traffic-menu-item" role="menuitem" data-action="traffic-limit" data-app-id="' + NetwatchShared.escapeHtml(appID) + '"><span class="ui-icon ui-icon--gauge" aria-hidden="true"></span><span>' + i18n('app_traffic_limit') + '</span></button>';
    }
    if (bridges.length) {
        if (limitSupported) panel += '<div class="app-traffic-menu-divider" role="separator"></div>';
        panel += '<button type="button" class="app-traffic-menu-item ' + (blocked ? 'restore' : 'danger') + '" role="menuitem" data-action="traffic-network" data-app-id="' + NetwatchShared.escapeHtml(appID) + '" data-network-state="' + (blocked ? 'restore' : 'disable') + '"><span class="ui-icon ui-icon--network" aria-hidden="true"></span><span>' + (blocked ? i18n('app_traffic_restore_internet') : i18n('app_traffic_disable_internet')) + '</span></button>';
    }
    return '<div class="app-traffic-action-menu">' + menu + panel + '</div>';
}

async function appTrafficSetInternetAccess(item, restore) {
    var bridges = appTrafficControllableBridges(item);
    if (!bridges.length) throw new Error('no controllable bridge');
    for (var index = 0; index < bridges.length; index++) {
        var path = restore ? '/api/v1/containers/unblock' : '/api/v1/containers/block';
        var payload = restore ? { bridge: bridges[index] } : { bridge: bridges[index], mode: 'internet' };
        await netwatchPost(path, payload);
    }
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
            state: data.stale ? 'stale' : 'fresh',
            count: nics.length,
            countLabel: i18n('interfaces_unit'),
            generatedAt: data.timestamp,
            stale: !!data.stale,
            ageSeconds: data.age_seconds,
            staleAfterSeconds: 15
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
    // Startup: always force one double-sample so the card is not empty/zero-rate
    tick(true);
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
})();
