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
    },
    hostPortsSort: {
        key: 'port',
        direction: 'asc'
    },
    hostPortsAdvanced: false,
    dashboardPanelCollapseInitialized: false
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
    natRefreshBtn: document.getElementById('nat-refresh-btn'),
    natStatus: document.getElementById('nat-status'),
    natType: document.getElementById('nat-type'),
    natMeta: document.getElementById('nat-meta'),
    natNote: document.getElementById('nat-note'),
    nicRealtimeRefreshBtn: document.getElementById('nic-realtime-refresh-btn'),
    interfacesRefreshBtn: document.getElementById('interfaces-refresh-btn'),
    interfacesStatus: document.getElementById('interfaces-status'),
    nicRealtimeStatus: document.getElementById('nic-realtime-status'),
    backdrop: document.getElementById('window-backdrop'),
    traceBackdrop: document.getElementById('trace-window-backdrop'),
    dnsDiagBackdrop: document.getElementById('dns-diag-window-backdrop'),
    openSettingsWindow: document.getElementById('open-settings-window'),
    openBroadbandWindow: document.getElementById('open-broadband-window'),
    openTransferWindow: document.getElementById('open-transfer-window'),
    openNetworkConfigWindow: document.getElementById('open-network-config-window'),
    closeSettingsWindow: document.getElementById('close-settings-window'),
    closeBroadbandWindow: document.getElementById('close-broadband-window'),
    closeTransferWindow: document.getElementById('close-transfer-window'),
    closeNetworkConfigWindow: document.getElementById('close-network-config-window'),
    closeTraceWindow: document.getElementById('close-trace-window'),
    openDNSDiagWindow: document.getElementById('open-dns-diag-window'),
    closeDNSDiagWindow: document.getElementById('close-dns-diag-window'),
    settingsWindow: document.getElementById('settings-window'),
    broadbandWindow: document.getElementById('broadband-window'),
    transferWindow: document.getElementById('transfer-window'),
    networkConfigWindow: document.getElementById('network-config-window'),
    traceWindow: document.getElementById('trace-window'),
    dnsDiagWindow: document.getElementById('dns-diag-window'),
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
    settingContainerControlEnabled: document.getElementById('setting-container-control-enabled'),
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
    broadbandDownload: document.getElementById('broadband-download'),
    broadbandUpload: document.getElementById('broadband-upload'),
    broadbandLatency: document.getElementById('broadband-latency'),
    broadbandJitter: document.getElementById('broadband-jitter'),
    broadbandNodeName: document.getElementById('broadband-node-name'),
    broadbandNodeProvider: document.getElementById('broadband-node-provider'),
    broadbandNodeRegion: document.getElementById('broadband-node-region'),
    broadbandNodeSource: document.getElementById('broadband-node-source'),
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

var inlineSiteIcons = {
    baidu: 'data:image/png;base64,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',
    bilibili: 'data:image/jpeg;base64,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',
    github: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAMAAABEpIrGAAAAb1BMVEX///8kKS74+PgsMTY+Q0f8/Pzw8PGRk5ZNUVXr6+xDSEzCxMUpLjLV1tYuMzc/Q0i3ubuUl5lbX2NTV1uLjpFKTlKxs7Xh4uOFh4rn5+h3en1vcnZ9gIPc3d6/wcJzd3o3PECmqKpjZ2rOz9CfoaRP5W5KAAABU0lEQVQ4jW1T7aKCIAzdEEUTSUuzLMtuvf8zXtiQVDx/lJ3DvgEIEPU9l1rL/F4LiFEMCQYkj2JDC6VxhaRJl3x2xgin7MePMuYRD7cQftrjrcL7ELk9nNt8yXXtyRm5mquzXADKA6LpzgZRlgBPZ1WO7yn/yrmqyGdGn95Zjav2hbNghZHMV3uNG3DZCigEmhRq+pmyrUAcifj4CO+49xciWqAe6p3pCJrNH1ATp5gH6KghQLrDnoB8GxaYPYFkhpONipjrl+wI61hQ8tB5EvYb4eTL5Ebhd8u/2f4EYfhvvWJpw9ZJ0LQfb5uxbMKi3pp5xVp30miexZHbRriHvUnojrKt7isXqWRBFQQNF2w3aYCPUl8/EREWzxtuE+pVI/wbkSGp0a51Nyg1F+L5/nel56xXgt+zcMgeC0FK27gdTzVoPXtI9DBuO2tRhJj94m3/A1GiDZXoM3d5AAAAAElFTkSuQmCC',
    youtube: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAMAAACdt4HsAAAAS1BMVEVHcEz/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/ADP/////AC//DUX/ACD/6+//RGP/ztf/q7n/hpn/H0n/Z35pZ192AAAADXRSTlMAZEr3h8U0rO3dGpd77vd9UAAAAXZJREFUWIXtlsl2hCAQRZkLZHBs7f//0mAn8ZBOF9CSRRa+pZ66Yg3UI+TSpX+lTilGqbAAUhpjtObcH+Jc6/hQSgArKGVKdU/hDoxOAkri2oBLwkV9aCrRGH8QTsd/E87He994AO/3TMoWgIz1Ny0A0xH1Rvl/SyvCWuK9Z4Si7/oaAMWLMFQRBLHY97dlHssISwADDCHc5rEEALQNdkCY1rHPn0ISrA0egIi451NhSoAQliGXCkN0CRBTseGp0ARrxAQQlhU9BEeHOQVExB1DVAL2/3hNqAaEpQ2AlrMOgDcUrynj3tJ4FcqA7FCVO3Fas71s8sMUptuYn2mZByxzYRgjIHMfTBU3CqA3kt9WpPd+yBKHveorLrR9NeG3cpVo615Q7ZupbTfKrnE7Q1zvTUlgu8PAxqlC+mFxzqeRq0+Xdfon2OFS4cQhOKRuVVGQb+RCS6DqpWHuFIuO2TkhhI22+UvW2vjAueiSmeqebfKlS3+gDxqiielmSzQ+AAAAAElFTkSuQmCC'
};

function getIconUrl(name) {
    var lowerName = String(name || '').toLowerCase();
    return inlineSiteIcons[lowerName] || '/icons/default.ico';
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
    var values = samples.map(Number).filter(function (v) { return Number.isFinite(v) && v > 0; }).sort(function (a, b) { return a - b; });
    if (!values.length) return { min: 0, avg: 0, max: 0 };
    // Drop top/bottom outliers when enough samples; avg becomes a robust estimate.
    var start = 0;
    var end = values.length;
    if (values.length >= 8) {
        var drop = Math.max(1, Math.floor(values.length * 0.1));
        start = drop;
        end = values.length - drop;
    }
    var core = values.slice(start, end);
    var sum = core.reduce(function (acc, v) { return acc + v; }, 0);
    return {
        min: Math.round(values[0]),
        avg: Math.round(sum / core.length),
        max: Math.round(values[values.length - 1])
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
        var status = item.status || 'unknown';
        var isDown = status === 'down' || status === 'unknown';
        var latencyMs = Number(item.latency_ms) || 0;
        // Only show a number when the probe actually got HTTP headers.
        // 故障文案只放状态列；延迟列保持短文本，避免列宽被「连接失败」撑开抖动。
        var latencyText;
        var latencyClass;
        if (isDown) {
            latencyText = '—';
            latencyClass = 'down';
        } else if (latencyMs > 0) {
            latencyText = latencyMs + ' ms';
            latencyClass = latencyMs >= 400 ? 'high' : '';
        } else {
            latencyText = '—';
            latencyClass = '';
        }
        var errorTitle = item.error ? ' title="' + NetwatchShared.escapeHtml(item.error) + '"' : '';
        var statusLabel = statusMap[status] || i18n('unknown');
        var rowStatusClass = status === 'ok' ? 'connectivity-row--ok' :
            (status === 'down' || status === 'unknown' ? 'connectivity-row--down' : 'connectivity-row--warn');
        return '<tr class="connectivity-row ' + rowStatusClass + '">' +
            '<td class="col-target"><div class="target-info"><img class="site-icon" src="' + getIconUrl(item.name) + '" onerror="this.src=\'/icons/default.ico\'"><span>' + NetwatchShared.escapeHtml(item.name) + '</span></div></td>' +
            '<td class="col-status" data-label="' + i18n('status_col') + '"><span class="nat-badge connectivity-status ' + getStatusClass(status) + '">' + statusLabel + '</span></td>' +
            '<td class="col-latency latency ' + latencyClass + '" data-label="' + i18n('latency_col') + '"' + errorTitle + '><span class="latency-value">' + latencyText + '</span></td>' +
            '</tr>';
    }).join('');
}
window.__app.updateConnectivityTable = updateConnectivityTable;

function ifaceFallbackLabel(linkType) {
    if (linkType === 'wired') return i18n('wired');
    if (linkType === 'wifi') return 'Wi-Fi';
    if (linkType === 'bridge') return i18n('host_bridge_title') || '网桥';
    if (linkType === 'tun') return i18n('proxy_tun_title') || '代理';
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
        return '<tr><td class="col-iface" data-label="' + i18n('iface_col') + '">' + mainLabel + subtitle + '</td><td class="col-status" data-label="' + i18n('status_col') + '">' + statusCell + '</td><td class="col-ipv4" data-label="' + i18n('ipv4_col') + '">' + (ipv4List.length ? ipv4List.map(escapeHtml).join('<br>') : '\u2014\u2014\u2014') + '</td><td class="col-ipv6" data-label="' + i18n('ipv6_col') + '">' + (ipv6List.length ? ipv6List.map(escapeHtml).join('<br>') : '\u2014\u2014\u2014') + '</td><td class="col-mac" data-label="MAC"><small>' + (escapeHtml(iface.hardware_addr) || '\u2014\u2014\u2014') + '</small></td></tr>';
    }).join('') || '<tr><td colspan="5" class="placeholder">' + i18n('no_target_nic') + '</td></tr>';
    NetwatchShared.setObservationStatus(els.interfacesStatus, {
        state: interfaces.length ? 'fresh' : 'empty',
        count: interfaces.length,
        countLabel: i18n('interfaces_unit'),
        generatedAt: networkInfo.generated_at,
        staleAfterSeconds: Math.max(90, Number(state.refreshInterval || 10) * 6)
    });
}
window.__app.renderNetworkInfo = renderNetworkInfo;

function natLabel(type) {
    switch (type) {
        case 'NAT1': return 'NAT1 - Full Cone';
        case 'NAT2': return 'NAT2 - Restricted Cone';
        case 'NAT3': return 'NAT3 - Port Restricted';
        case 'NAT4': return 'NAT4 - Symmetric';
        default: return i18n('unknown');
    }
}

var natExplain = {
    NAT1: ['完全锥形', '极佳', '外部任何主机都可以通过映射的公网地址直接访问内网设备。客户端与微服直连场景最为友好。'],
    NAT2: ['受限锥形', '良好', '外部主机必须先收到内网设备的请求后才能回复。安全性更高，客户端与微服直连仍可正常使用。'],
    NAT3: ['端口受限锥形', '一般', '不仅限制源 IP，还限制源端口。客户端与微服直连可能受到影响。'],
    NAT4: ['对称型', '较差', '每个不同的目标地址和端口组合都会分配不同的映射。P2P 打洞极其困难，严重影响客户端与微服直连体验。']
};

function renderNATInfo(nat) {
    nat = nat || {};
    if (els.natType) els.natType.textContent = natLabel(nat.type);
    var explain = natExplain[nat.type];
    if (els.natMeta) els.natMeta.textContent = explain ? (explain[0] + ' / ' + explain[1]) : '';
    if (els.natNote) els.natNote.textContent = explain ? explain[2] : '';
    NetwatchShared.setObservationStatus(els.natStatus, {
        state: nat.type ? 'fresh' : (nat.error ? 'error' : 'empty'),
        generatedAt: nat.generated_at,
        staleAfterSeconds: 900,
        error: nat.error
    });
}
window.__app.renderNATInfo = renderNATInfo;

// Used across modules for updating window controls
function updateWindowControls() {
    var busy = Boolean(state.runningTest);
    els.openSettingsWindow.disabled = busy;
    els.openBroadbandWindow.disabled = busy && state.runningTest !== 'broadband';
    els.openTransferWindow.disabled = busy && state.runningTest !== 'transfer';
    if (els.openNetworkConfigWindow) els.openNetworkConfigWindow.disabled = busy;
    if (els.interfacesRefreshBtn) els.interfacesRefreshBtn.disabled = busy;
    els.runBroadbandTest.disabled = busy;
    els.runTransferTest.disabled = busy;
}
window.__app.updateWindowControls = updateWindowControls;
})();
