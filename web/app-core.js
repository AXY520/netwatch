window.__app = window.__app || {};

(function () {
var state = {
    theme: localStorage.getItem('theme') || 'dark',
    refreshInterval: 10,
    lastRefreshTime: Date.now(),
    timerInterval: null,
    summary: null,
    egressData: null,
    traceResult: null,
    tracePoller: null,
    fastRefreshing: false,
    refreshing: false,
    speedConfig: {
        broadband_duration_sec: 10,
        local_transfer_duration_sec: 10,
        local_transfer_payload_mb: 32
    },
    settings: {
        refresh_interval_sec: 10,
        broadband_domestic_only: true,
        nic_realtime_enabled: true,
        nic_realtime_interval_sec: 1,
        chart_time_label_interval: 0,
        traffic_sampling_enabled: true,
        traffic_sampling_interval_sec: 60,
        per_app_sampling_interval: {},
        persistent_traffic_bridges: [],
        background_monitor_enabled: false,
        background_monitor_interval_sec: 60,
        notifications_enabled: false,
        client_notification_enabled: true,
        notify_abnormal_traffic: true,
        notify_egress_change: true,
        notify_connectivity_change: true,
        notify_lan_device_change: true,
        abnormal_traffic_threshold_mbps: 100,
        bark_enabled: false,
        bark_server_url: 'https://api.day.app',
        bark_device_key: '',
        bark_group: 'Netwatch',
        pushplus_enabled: false,
        pushplus_token: '',
        pushplus_topic: '',
        dnd_enabled: false,
        dnd_start: '22:00',
        dnd_end: '08:00',
        scheduled_notify_enabled: false,
        scheduled_notify_time: '09:00'
    },
    activeWindow: null,
    runningTest: null,
    broadbandPoller: null,
    transferAbortController: null,
    sse: null,
    initialized: false,
    settingsInitialized: false,
    egressInitialized: false,
    nicRealtimeInitialized: false,
    traceInitialized: false,
    controlsBound: false,
    notificationLastID: Number(localStorage.getItem('netwatch_notification_last_id') || '0') || 0,
    deviceID: localStorage.getItem('netwatch_device_id') || '',
    lzcGatewayPromise: null,
    notificationUnsupported: localStorage.getItem('netwatch_notification_unsupported') === 'true',
    modalScrollY: 0,
    modalLockCount: 0,
    appTrafficSort: {
        key: 'total',
        direction: 'desc'
    }
};
window.__app.state = state;

var els = {
    themeToggle: document.getElementById('theme-toggle'),
    refreshBtn: document.getElementById('refresh-btn'),
    overlay: document.getElementById('loading-overlay'),
    websiteRefreshBtn: document.getElementById('website-refresh-btn'),
    websiteStatus: document.getElementById('website-status'),
    domesticTable: document.querySelector('#domestic-table tbody'),
    globalTable: document.querySelector('#global-table tbody'),
    interfacesTable: document.querySelector('#interfaces-table tbody'),
    valGw4: document.getElementById('val-gw4'),
    valPlatformConnectivity: document.getElementById('val-platform-connectivity'),
    nicRealtimeRefreshBtn: document.getElementById('nic-realtime-refresh-btn'),
    nicRealtimeStatus: document.getElementById('nic-realtime-status'),
    backdrop: document.getElementById('window-backdrop'),
    traceBackdrop: document.getElementById('trace-window-backdrop'),
    openSettingsWindow: document.getElementById('open-settings-window'),
    openBroadbandWindow: document.getElementById('open-broadband-window'),
    openTransferWindow: document.getElementById('open-transfer-window'),
    closeSettingsWindow: document.getElementById('close-settings-window'),
    closeBroadbandWindow: document.getElementById('close-broadband-window'),
    closeTransferWindow: document.getElementById('close-transfer-window'),
    closeTraceWindow: document.getElementById('close-trace-window'),
    settingsWindow: document.getElementById('settings-window'),
    broadbandWindow: document.getElementById('broadband-window'),
    transferWindow: document.getElementById('transfer-window'),
    traceWindow: document.getElementById('trace-window'),
    ipv6DetailWindow: document.getElementById('ipv6-detail-window'),
    ipv6DetailBackdrop: document.getElementById('ipv6-detail-window-backdrop'),
    ipv6RenewWindow: document.getElementById('ipv6-renew-window'),
    ipv6RenewBackdrop: document.getElementById('ipv6-renew-window-backdrop'),
    saveSettings: document.getElementById('save-settings'),
    settingBroadbandDomesticOnly: document.getElementById('setting-broadband-domestic-only'),
    settingNICRealtimeEnabled: document.getElementById('setting-nic-realtime-enabled'),
    settingNICRealtimeIntervalSec: document.getElementById('setting-nic-realtime-interval-sec'),
    settingBackgroundMonitorEnabled: document.getElementById('setting-background-monitor-enabled'),
    settingBackgroundMonitorIntervalSec: document.getElementById('setting-background-monitor-interval-sec'),
    settingNotificationsEnabled: document.getElementById('setting-notifications-enabled'),
    settingClientNotificationEnabled: document.getElementById('setting-client-notification-enabled'),
    settingNotifyAbnormalTraffic: document.getElementById('setting-notify-abnormal-traffic'),
    settingAbnormalTrafficThresholdMbps: document.getElementById('setting-abnormal-traffic-threshold-mbps'),
    settingNotifyEgressChange: document.getElementById('setting-notify-egress-change'),
    settingNotifyConnectivityChange: document.getElementById('setting-notify-connectivity-change'),
    settingNotifyLANDeviceChange: document.getElementById('setting-notify-lan-device-change'),
    settingBarkEnabled: document.getElementById('setting-bark-enabled'),
    settingBarkServerURL: document.getElementById('setting-bark-server-url'),
    settingBarkDeviceKey: document.getElementById('setting-bark-device-key'),
    settingBarkGroup: document.getElementById('setting-bark-group'),
    testBarkNotification: document.getElementById('test-bark-notification'),
    settingPushPlusEnabled: document.getElementById('setting-pushplus-enabled'),
    settingPushPlusToken: document.getElementById('setting-pushplus-token'),
    settingPushPlusTopic: document.getElementById('setting-pushplus-topic'),
    testPushPlusNotification: document.getElementById('test-pushplus-notification'),
    settingDNDEnabled: document.getElementById('setting-dnd-enabled'),
    settingDNDStart: document.getElementById('setting-dnd-start'),
    settingDNDEnd: document.getElementById('setting-dnd-end'),
    settingScheduledNotifyEnabled: document.getElementById('setting-scheduled-notify-enabled'),
    settingScheduledNotifyTime: document.getElementById('setting-scheduled-notify-time'),
    notificationSettingsWindow: document.getElementById('notification-settings-window'),
    openNotificationSettings: document.getElementById('open-notification-settings'),
    closeNotificationSettings: document.getElementById('close-notification-settings'),
    saveNotificationSettings: document.getElementById('save-notification-settings'),
    broadbandNote: document.getElementById('broadband-note'),
    transferNote: document.getElementById('transfer-note'),
    runBroadbandTest: document.getElementById('run-broadband-test'),
    runTransferTest: document.getElementById('run-transfer-test'),
    broadbandPrimaryMode: document.getElementById('broadband-primary-mode'),
    broadbandPrimaryCaption: document.getElementById('broadband-primary-caption'),
    broadbandStage: document.getElementById('broadband-stage'),
    broadbandProgress: document.getElementById('broadband-progress'),
    broadbandDownload: document.getElementById('broadband-download'),
    broadbandUpload: document.getElementById('broadband-upload'),
    broadbandLatency: document.getElementById('broadband-latency'),
    broadbandJitter: document.getElementById('broadband-jitter'),
    broadbandNodeName: document.getElementById('broadband-node-name'),
    broadbandNodeProvider: document.getElementById('broadband-node-provider'),
    broadbandNodeRegion: document.getElementById('broadband-node-region'),
    broadbandNodeSource: document.getElementById('broadband-node-source'),
    broadbandDurationNode: document.getElementById('broadband-duration-node'),
    broadbandDurationLatency: document.getElementById('broadband-duration-latency'),
    broadbandDurationDownload: document.getElementById('broadband-duration-download'),
    broadbandDurationUpload: document.getElementById('broadband-duration-upload'),
    broadbandDurationTotal: document.getElementById('broadband-duration-total'),
    broadbandFailureStage: document.getElementById('broadband-failure-stage'),
    broadbandFailureReason: document.getElementById('broadband-failure-reason'),
    broadbandSteps: document.getElementById('broadband-steps'),
    transferPrimaryMode: document.getElementById('transfer-primary-mode'),
    transferPrimaryCaption: document.getElementById('transfer-primary-caption'),
    transferStage: document.getElementById('transfer-stage'),
    transferProgress: document.getElementById('transfer-progress'),
    transferDownload: document.getElementById('transfer-download'),
    transferUpload: document.getElementById('transfer-upload'),
    transferLatency: document.getElementById('transfer-latency'),
    transferJitter: document.getElementById('transfer-jitter'),
    transferRTTMin: document.getElementById('transfer-rtt-min'),
    transferRTTAvg: document.getElementById('transfer-rtt-avg'),
    transferRTTMax: document.getElementById('transfer-rtt-max'),
    transferRTTJitter: document.getElementById('transfer-rtt-jitter'),
    transferDownloadBytes: document.getElementById('transfer-download-bytes'),
    transferUploadBytes: document.getElementById('transfer-upload-bytes'),
    transferTotalBytes: document.getElementById('transfer-total-bytes'),
    transferDuration: document.getElementById('transfer-duration'),
    broadbandHistory: document.getElementById('broadband-history'),
    transferHistory: document.getElementById('transfer-history')
};
window.__app.els = els;

var i18n = function (key) { return typeof window.__ === 'function' ? window.__(key) : key; };
window.__app.i18n = i18n;

var debounceTimers = {};
function debounce(key, fn, ms) {
    ms = ms || 300;
    if (debounceTimers[key]) clearTimeout(debounceTimers[key]);
    debounceTimers[key] = setTimeout(fn, ms);
}
window.__app.debounce = debounce;

var statusMap = { ok: i18n('ok'), down: i18n('down'), degraded: i18n('degraded'), unknown: i18n('unknown') };
window.__app.statusMap = statusMap;

var broadbandStageMap = { starting: i18n('starting'), latency: i18n('latency'), download: i18n('download'), upload: i18n('upload'), finalizing: i18n('finalizing'), complete: i18n('complete'), canceled: i18n('canceled'), error: i18n('error') };
window.__app.broadbandStageMap = broadbandStageMap;

var broadbandFailureStageMap = {
    node_selection: i18n('select_node'),
    latency: i18n('latency'),
    download: i18n('download'),
    upload: i18n('upload'),
    timeout: i18n('timeout')
};
window.__app.broadbandFailureStageMap = broadbandFailureStageMap;

var broadbandStepIcon = { ok: '\u2713', fail: '\u2717', running: '\u27F3', info: '\u2022' };
window.__app.broadbandStepIcon = broadbandStepIcon;

function getIconUrl(name) {
    var lowerName = String(name || '').toLowerCase();
    var localIcons = ['baidu', 'bilibili', 'github', 'youtube'];
    return localIcons.indexOf(lowerName) !== -1 ? '/icons/' + lowerName + '.ico' : '/icons/default.ico';
}
window.__app.getIconUrl = getIconUrl;

var countryNameZh = {
    'united states': '\u7F8E\u56FD', 'usa': '\u7F8E\u56FD', 'us': '\u7F8E\u56FD',
    'china': '\u4E2D\u56FD', 'cn': '\u4E2D\u56FD',
    'hong kong': '\u4E2D\u56FD\u9999\u6E2F', 'taiwan': '\u4E2D\u56FD\u53F0\u6E7E',
    'japan': '\u65E5\u672C', 'singapore': '\u65B0\u52A0\u5761',
    'south korea': '\u97E9\u56FD', 'korea, republic of': '\u97E9\u56FD',
    'germany': '\u5FB7\u56FD', 'united kingdom': '\u82F1\u56FD',
    'france': '\u6CD5\u56FD', 'netherlands': '\u8377\u5170',
    'canada': '\u52A0\u62FF\u5927', 'australia': '\u6FB3\u5927\u5229\u4E9A'
};

var regionNameZh = {
    'california': '\u52A0\u5229\u798F\u5C3C\u4E9A\u5DDE', 'new york': '\u7EBD\u7EA6\u5DDE',
    'washington': '\u534E\u76DB\u987F\u5DDE', 'oregon': '\u4FC4\u52D2\u5188\u5DDE',
    'texas': '\u5F97\u514B\u8428\u65AF\u5DDE', 'illinois': '\u4F0A\u5229\u8BFA\u4F0A\u5DDE',
    'virginia': '\u5F17\u5409\u5C3C\u4E9A\u5DDE'
};

var cityNameZh = {
    'los angeles': '\u6D1B\u6749\u77F6', 'new york city': '\u7EBD\u7EA6\u5E02',
    'new york': '\u7EBD\u7EA6', 'san jose': '\u5723\u4F55\u585E',
    'san francisco': '\u65E7\u91D1\u5C71', 'seattle': '\u897F\u96C5\u56FE',
    'chicago': '\u829D\u52A0\u54E5', 'ashburn': '\u963F\u4EC0\u672C',
    'tokyo': '\u4E1C\u4EAC', 'osaka': '\u5927\u962A',
    'seoul': '\u9996\u5C14', 'singapore': '\u65B0\u52A0\u5761',
    'frankfurt': '\u6CD5\u5170\u514B\u798F', 'london': '\u4F26\u6566',
    'paris': '\u5DF4\u9ECE', 'amsterdam': '\u963F\u59C6\u65AF\u7279\u4E39',
    'toronto': '\u591A\u4F26\u591A', 'sydney': '\u6089\u5C3C'
};

function translateGeoName(value, kind) {
    var raw = String(value || '').trim();
    if (!raw) return '';
    var key = raw.toLowerCase();
    if (kind === 'country') return countryNameZh[key] || raw;
    if (kind === 'region') return regionNameZh[key] || raw;
    if (kind === 'city') return cityNameZh[key] || raw;
    return countryNameZh[key] || regionNameZh[key] || cityNameZh[key] || raw;
}
window.__app.translateGeoName = translateGeoName;

function formatMbps(value) {
    return Number.isFinite(value) && value > 0 ? value.toFixed(1) : '--';
}
window.__app.formatMbps = formatMbps;

function getStatusClass(status) {
    if (status === 'ok') return 'status-ok';
    if (status === 'down') return 'status-down';
    return 'status-warn';
}
window.__app.getStatusClass = getStatusClass;

function isAbortError(error) {
    return error && error.name === 'AbortError';
}
window.__app.isAbortError = isAbortError;

function formatMS(value) {
    return Number.isFinite(value) && value > 0 ? Math.round(value) + ' ms' : '--';
}
window.__app.formatMS = formatMS;

function formatDurationMS(value) {
    var ms = Number(value);
    if (!Number.isFinite(ms) || ms <= 0) return '--';
    if (ms < 1000) return Math.round(ms) + ' ms';
    return (ms / 1000).toFixed(ms < 10000 ? 1 : 0) + ' s';
}
window.__app.formatDurationMS = formatDurationMS;

function formatMB(value) {
    var mb = Number(value);
    if (!Number.isFinite(mb) || mb <= 0) return '--';
    return mb.toFixed(mb >= 100 ? 0 : 1) + ' MB';
}
window.__app.formatMB = formatMB;

function bytesToMB(value) {
    var bytes = Number(value);
    if (!Number.isFinite(bytes) || bytes <= 0) return 0;
    return bytes / 1024 / 1024;
}
window.__app.bytesToMB = bytesToMB;

function setText(el, value) {
    if (el) el.textContent = value || '--';
}
window.__app.setText = setText;

function finiteNumber(value, fallback) {
    var number = parseFloat(value);
    return Number.isFinite(number) ? number : (fallback || 0);
}
window.__app.finiteNumber = finiteNumber;

function summarizeRTT(samples) {
    samples = samples || [];
    var values = samples.map(Number).filter(function (v) { return Number.isFinite(v) && v > 0; });
    if (!values.length) return { min: 0, avg: 0, max: 0 };
    var sum = values.reduce(function (acc, v) { return acc + v; }, 0);
    return {
        min: Math.round(Math.min.apply(null, values)),
        avg: Math.round(sum / values.length),
        max: Math.round(Math.max.apply(null, values))
    };
}
window.__app.summarizeRTT = summarizeRTT;

function setPrimaryStatus(modeEl, captionEl, mode, caption) {
    if (modeEl) {
        modeEl.textContent = mode;
        modeEl.classList.remove('active', 'done', 'error');
        if (mode === 'Download' || mode === 'Upload' || mode === 'Ping') modeEl.classList.add('active');
        else if (mode === 'Result') modeEl.classList.add('done');
        else if (mode === 'Stopped') modeEl.classList.add('error');
    }
    if (captionEl) captionEl.textContent = caption || '';
}
window.__app.setPrimaryStatus = setPrimaryStatus;

function setSpeedPanelMode(scope, mode) {
    var dlPanel = document.getElementById(scope + '-panel-download');
    var upPanel = document.getElementById(scope + '-panel-upload');
    if (!dlPanel || !upPanel) return;
    dlPanel.classList.toggle('active', mode === 'Download');
    upPanel.classList.toggle('active', mode === 'Upload');
}
window.__app.setSpeedPanelMode = setSpeedPanelMode;

function createSpeedSampler() {
    return {
        startedAt: performance.now(),
        warmupEndAt: performance.now() + 2500,
        lastAt: performance.now(),
        lastBytes: 0,
        samples: [],
        allMbps: [],
        lastMbps: 0,
        isWarmup: true
    };
}
window.__app.createSpeedSampler = createSpeedSampler;

function observeSpeedSampler(sampler, totalBytes) {
    var now = performance.now();
    if (sampler.isWarmup && now >= sampler.warmupEndAt) {
        sampler.isWarmup = false;
        sampler.lastBytes = totalBytes;
        sampler.lastAt = now;
        return sampler.lastMbps;
    }
    var elapsedMs = now - sampler.lastAt;
    if (elapsedMs > 100) {
        var deltaBytes = Math.max(0, totalBytes - sampler.lastBytes);
        var instantMbps = (deltaBytes * 8) / (elapsedMs / 1000) / 1000000;
        if (!sampler.isWarmup && instantMbps > 0) {
            sampler.samples.push(instantMbps);
            sampler.allMbps.push(instantMbps);
            if (sampler.samples.length > 20) sampler.samples = sampler.samples.slice(-20);
        }
        var weight = sampler.isWarmup ? 0.15 : 0.3;
        if (sampler.lastMbps > 0) {
            sampler.lastMbps = instantMbps * weight + sampler.lastMbps * (1 - weight);
        } else {
            sampler.lastMbps = instantMbps;
        }
        sampler.lastAt = now;
        sampler.lastBytes = totalBytes;
    }
    return sampler.lastMbps;
}
window.__app.observeSpeedSampler = observeSpeedSampler;

function stableSpeedFromSampler(sampler, totalBytes) {
    var data = sampler.allMbps.length > 5 ? sampler.allMbps : sampler.samples;
    if (data.length === 0) {
        var totalElapsedSec = Math.max((performance.now() - sampler.startedAt) / 1000, 0.5);
        return (totalBytes * 8) / totalElapsedSec / 1000000;
    }
    var sorted = data.slice().sort(function (a, b) { return a - b; });
    var cut = Math.floor(sorted.length * 0.1);
    var trimmed = sorted.slice(cut, sorted.length - cut);
    var final = trimmed.reduce(function (sum, v) { return sum + v; }, 0) / (trimmed.length || 1);
    return final > 0 ? final : sorted[Math.floor(sorted.length / 2)];
}
window.__app.stableSpeedFromSampler = stableSpeedFromSampler;

function renderPlaceholderTable(tbody, message) {
    tbody.innerHTML = '<tr><td colspan="3" class="placeholder">' + message + '</td></tr>';
}
window.__app.renderPlaceholderTable = renderPlaceholderTable;

function updateConnectivityTable(tbody, items) {
    if (!Array.isArray(items) || items.length === 0) {
        renderPlaceholderTable(tbody, i18n('no_results'));
        return;
    }
    tbody.innerHTML = items.map(function (item) {
        return '<tr><td><div class="target-info"><img class="site-icon" src="' + getIconUrl(item.name) + '" onerror="this.src=\'/icons/default.ico\'"><span>' + NetwatchShared.escapeHtml(item.name) + '</span></div></td><td data-label="' + i18n('status_col') + '"><span class="nat-badge ' + getStatusClass(item.status) + '">' + (statusMap[item.status] || i18n('unknown')) + '</span></td><td data-label="' + i18n('latency_col') + '" class="latency ' + (item.latency_ms > 200 ? 'high' : (item.latency_ms === 0 ? 'down' : '')) + '">' + (item.latency_ms > 0 ? item.latency_ms + ' ms' : i18n('connection_failed')) + '</td></tr>';
    }).join('');
}
window.__app.updateConnectivityTable = updateConnectivityTable;

function ifaceFallbackLabel(linkType) {
    if (linkType === 'wired') return i18n('wired');
    if (linkType === 'wifi') return 'Wi-Fi';
    return '';
}
window.__app.ifaceFallbackLabel = ifaceFallbackLabel;

function formatDeviceStatus(status) {
    switch (status) {
        case 'connected': return i18n('connected');
        case 'disconnected': return i18n('disconnected');
        case 'connecting': return i18n('connecting');
        case 'disconnecting': return i18n('disconnecting');
        case 'disabled': return i18n('disabled');
        case 'unavailable': return i18n('unavailable');
        case 'unknown': return i18n('unknown');
        case '': case undefined: return '---';
        default: return status;
    }
}
window.__app.formatDeviceStatus = formatDeviceStatus;

function formatPlatformConnectivity(networkInfo) {
    var level = networkInfo.platform_connectivity || '';
    switch (level) {
        case 'Full': return i18n('internet_full');
        case 'Limited': return i18n('internet_limited');
        case 'Portal': return i18n('internet_portal');
        case 'None': return i18n('internet_none');
        case 'Unknown': return i18n('unknown');
        case '':
            if (networkInfo.has_internet) return i18n('internet_full');
            return i18n('sdk_status_error');
        default: return level;
    }
}
window.__app.formatPlatformConnectivity = formatPlatformConnectivity;

function formatBitsPerSec(bytesPerSec) {
    var bps = (bytesPerSec || 0) * 8;
    if (bps < 1000) return bps.toFixed(0) + ' bit/s';
    if (bps < 1000000) return (bps / 1000).toFixed(1) + ' kbit/s';
    if (bps < 1000000000) return (bps / 1000000).toFixed(2) + ' Mbit/s';
    return (bps / 1000000000).toFixed(2) + ' Gbit/s';
}
window.__app.formatBitsPerSec = formatBitsPerSec;

function shortAppName(appid) {
    if (!appid) return '';
    var parts = appid.split('.');
    return parts[parts.length - 1] || appid;
}
window.__app.shortAppName = shortAppName;

// renderNetworkInfo used by multiple modules
function renderNetworkInfo(networkInfo) {
    networkInfo = networkInfo || {};
    els.valGw4.textContent = networkInfo.default_ipv4 ? (networkInfo.default_ipv4.gateway || i18n('unknown')) : i18n('unknown');
    if (els.valPlatformConnectivity) {
        els.valPlatformConnectivity.textContent = formatPlatformConnectivity(networkInfo);
    }
    var interfaces = Array.isArray(networkInfo.interfaces) ? networkInfo.interfaces : [];
    var escapeHtml = NetwatchShared.escapeHtml;
    els.interfacesTable.innerHTML = interfaces.map(function (iface) {
        var mainLabel;
        if (iface.link_type === 'wifi' && iface.wifi_ssid) {
            mainLabel = iface.wifi_ssid;
        } else {
            mainLabel = iface.label || ifaceFallbackLabel(iface.link_type) || iface.name || '\u2014\u2014\u2014';
        }
        var subtitle = iface.name && iface.name !== mainLabel ? '<br><small style="color:var(--text-muted)">' + escapeHtml(iface.name) + '</small>' : '';
        var statusCell = formatDeviceStatus(iface.device_status);
        var ipv4List = (iface.ipv4 || []).filter(function (s) { return s; });
        var ipv6List = (iface.ipv6 || []).filter(function (s) { return !/^fe80:/i.test(s); });
        return '<tr><td class="col-iface">' + mainLabel + subtitle + '</td><td class="col-status">' + statusCell + '</td><td class="col-ipv4">' + (ipv4List.length ? ipv4List.map(escapeHtml).join('<br>') : '\u2014\u2014\u2014') + '</td><td class="col-ipv6">' + (ipv6List.length ? ipv6List.map(escapeHtml).join('<br>') : '\u2014\u2014\u2014') + '</td><td class="col-mac"><small>' + (escapeHtml(iface.hardware_addr) || '\u2014\u2014\u2014') + '</small></td></tr>';
    }).join('') || '<tr><td colspan="5" class="placeholder">' + i18n('no_target_nic') + '</td></tr>';
}
window.__app.renderNetworkInfo = renderNetworkInfo;

// Used across modules for updating window controls
function updateWindowControls() {
    var busy = Boolean(state.runningTest);
    els.openSettingsWindow.disabled = busy;
    els.openBroadbandWindow.disabled = busy && state.runningTest !== 'broadband';
    els.openTransferWindow.disabled = busy && state.runningTest !== 'transfer';
    els.runBroadbandTest.disabled = busy;
    els.runTransferTest.disabled = busy;
}
window.__app.updateWindowControls = updateWindowControls;
})();
