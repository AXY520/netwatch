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

window.__app.initAppTraffic = initAppTraffic;
window.__app.initNICRealtime = initNICRealtime;
window.__app.initTrace = initTrace;
window.__app.updateNICRealtimeRefreshButton = updateNICRealtimeRefreshButton;
window.__app.updateTrafficAnalysisLink = updateTrafficAnalysisLink;
window.__app.refreshAppTrafficSoon = refreshAppTrafficSoon;
})();
