window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

function applySettingsToForm() {
    if (els.settingNICRealtimeEnabled) {
        els.settingNICRealtimeEnabled.checked = !!state.settings.nic_realtime_enabled;
    }
    if (els.settingNICRealtimeIntervalSec) {
        els.settingNICRealtimeIntervalSec.value = String(state.settings.nic_realtime_interval_sec || 1);
        els.settingNICRealtimeIntervalSec.disabled = !state.settings.nic_realtime_enabled;
        if (window.syncCustomSelect) window.syncCustomSelect(els.settingNICRealtimeIntervalSec);
    }
    if (els.settingAppTrafficRealtimeEnabled) {
        els.settingAppTrafficRealtimeEnabled.checked = state.settings.app_traffic_realtime_enabled !== false;
    }
	if (els.settingHostNetworkExperimentalEnabled) {
		els.settingHostNetworkExperimentalEnabled.checked = state.settings.host_network_experimental_enabled === true;
	}
    if (els.settingBackgroundMonitorEnabled) {
        els.settingBackgroundMonitorEnabled.checked = !!state.settings.background_monitor_enabled;
    }
    if (els.settingBackgroundMonitorIntervalSec) {
        els.settingBackgroundMonitorIntervalSec.value = String(state.settings.background_monitor_interval_sec || 60);
        els.settingBackgroundMonitorIntervalSec.disabled = !state.settings.background_monitor_enabled;
        if (window.syncCustomSelect) window.syncCustomSelect(els.settingBackgroundMonitorIntervalSec);
    }
    var notificationsDisabled = !state.settings.background_monitor_enabled || !state.settings.notifications_enabled;
    if (els.settingNotificationsEnabled) {
        els.settingNotificationsEnabled.checked = !!state.settings.notifications_enabled;
        els.settingNotificationsEnabled.disabled = !state.settings.background_monitor_enabled;
    }
    if (els.settingNotifyAbnormalTraffic) {
        els.settingNotifyAbnormalTraffic.checked = state.settings.notify_abnormal_traffic !== false;
        els.settingNotifyAbnormalTraffic.disabled = notificationsDisabled;
    }
    if (els.settingAbnormalTrafficThresholdMbps) {
        els.settingAbnormalTrafficThresholdMbps.value = String(state.settings.abnormal_traffic_threshold_mbps || 100);
        els.settingAbnormalTrafficThresholdMbps.disabled = notificationsDisabled || state.settings.notify_abnormal_traffic === false;
        if (window.syncCustomSelect) window.syncCustomSelect(els.settingAbnormalTrafficThresholdMbps);
    }
    if (els.settingNotifyEgressChange) {
        els.settingNotifyEgressChange.checked = state.settings.notify_egress_change !== false;
        els.settingNotifyEgressChange.disabled = notificationsDisabled;
    }
    if (els.settingNotifyConnectivityChange) {
        els.settingNotifyConnectivityChange.checked = state.settings.notify_connectivity_change !== false;
        els.settingNotifyConnectivityChange.disabled = notificationsDisabled;
    }
    if (els.settingNotifyLANDeviceChange) {
        els.settingNotifyLANDeviceChange.checked = state.settings.notify_lan_device_change !== false;
        els.settingNotifyLANDeviceChange.disabled = notificationsDisabled;
    }
    if (els.settingClientNotificationEnabled) {
        els.settingClientNotificationEnabled.checked = state.settings.client_notification_enabled !== false;
        els.settingClientNotificationEnabled.disabled = notificationsDisabled || !state.settings.bark_enabled;
    }
    if (els.settingBarkEnabled) {
        els.settingBarkEnabled.checked = !!state.settings.bark_enabled;
        els.settingBarkEnabled.disabled = notificationsDisabled;
    }
    if (els.settingBarkServerURL) {
        els.settingBarkServerURL.value = state.settings.bark_server_url || 'https://api.day.app';
        els.settingBarkServerURL.disabled = notificationsDisabled;
    }
    if (els.settingBarkDeviceKey) {
        els.settingBarkDeviceKey.value = state.settings.bark_device_key || '';
        els.settingBarkDeviceKey.disabled = notificationsDisabled;
    }
    if (els.settingBarkGroup) {
        els.settingBarkGroup.value = state.settings.bark_group || 'Netwatch';
        els.settingBarkGroup.disabled = notificationsDisabled;
    }
    if (els.testBarkNotification) {
        els.testBarkNotification.disabled = notificationsDisabled || !state.settings.bark_enabled;
    }
    if (els.settingPushPlusEnabled) {
        els.settingPushPlusEnabled.checked = !!state.settings.pushplus_enabled;
        els.settingPushPlusEnabled.disabled = notificationsDisabled;
    }
    if (els.settingPushPlusToken) {
        els.settingPushPlusToken.value = state.settings.pushplus_token || '';
        els.settingPushPlusToken.disabled = notificationsDisabled;
    }
    if (els.settingPushPlusTopic) {
        els.settingPushPlusTopic.value = state.settings.pushplus_topic || '';
        els.settingPushPlusTopic.disabled = notificationsDisabled;
    }
    if (els.testPushPlusNotification) {
        els.testPushPlusNotification.disabled = notificationsDisabled || !state.settings.pushplus_enabled;
    }
    if (els.settingDNDEnabled) {
        els.settingDNDEnabled.checked = !!state.settings.dnd_enabled;
        els.settingDNDEnabled.disabled = notificationsDisabled;
    }
    if (els.settingDNDStart) {
        els.settingDNDStart.value = state.settings.dnd_start || '22:00';
        els.settingDNDStart.disabled = notificationsDisabled || !state.settings.dnd_enabled;
    }
    if (els.settingDNDEnd) {
        els.settingDNDEnd.value = state.settings.dnd_end || '08:00';
        els.settingDNDEnd.disabled = notificationsDisabled || !state.settings.dnd_enabled;
    }
    if (els.settingScheduledNotifyEnabled) {
        els.settingScheduledNotifyEnabled.checked = !!state.settings.scheduled_notify_enabled;
        els.settingScheduledNotifyEnabled.disabled = notificationsDisabled;
    }
    if (els.settingScheduledNotifyTime) {
        els.settingScheduledNotifyTime.value = state.settings.scheduled_notify_time || '09:00';
        els.settingScheduledNotifyTime.disabled = notificationsDisabled || !state.settings.scheduled_notify_enabled;
    }
    if (window.__app.updateNICRealtimeRefreshButton) window.__app.updateNICRealtimeRefreshButton();
}

async function loadSettings() {
    try {
        var settingsData = window.NetwatchAPI
            ? await window.NetwatchAPI.get('/api/v1/settings')
            : await (async function () {
                var settingsResp = await fetch('/api/v1/settings', { cache: 'no-store' });
                if (!settingsResp.ok) throw new Error('HTTP ' + settingsResp.status);
                return settingsResp.json();
            })();
        state.settings = {
            refresh_interval_sec: settingsData.refresh_interval_sec || state.refreshInterval || 10,
            nic_realtime_enabled: settingsData.nic_realtime_enabled !== false,
            nic_realtime_interval_sec: settingsData.nic_realtime_interval_sec || 1,
            app_traffic_realtime_enabled: settingsData.app_traffic_realtime_enabled !== false,
			host_network_experimental_enabled: settingsData.host_network_experimental_enabled === true,
			app_proxy: settingsData.app_proxy || { protocol: 'socks5', host: '127.0.0.1', port: 7890 },
            chart_time_label_interval: settingsData.chart_time_label_interval || 0,
            dashboard_collapsed_sections: settingsData.dashboard_collapsed_sections || [],
            background_monitor_enabled: !!settingsData.background_monitor_enabled,
            background_monitor_interval_sec: settingsData.background_monitor_interval_sec || 60,
            notifications_enabled: !!settingsData.notifications_enabled,
            client_notification_enabled: settingsData.client_notification_enabled !== false,
            notify_abnormal_traffic: settingsData.notify_abnormal_traffic !== false,
            notify_egress_change: settingsData.notify_egress_change !== false,
            notify_connectivity_change: settingsData.notify_connectivity_change !== false,
            notify_lan_device_change: settingsData.notify_lan_device_change !== false,
            lan_device_offline_after_sec: settingsData.lan_device_offline_after_sec !== undefined ? settingsData.lan_device_offline_after_sec : 180,
            lan_device_online_after_sec: settingsData.lan_device_online_after_sec !== undefined ? settingsData.lan_device_online_after_sec : 0,
            lan_device_offline_notify_delay_sec: settingsData.lan_device_offline_notify_delay_sec !== undefined ? settingsData.lan_device_offline_notify_delay_sec : 120,
            lan_device_online_notify_delay_sec: settingsData.lan_device_online_notify_delay_sec !== undefined ? settingsData.lan_device_online_notify_delay_sec : 120,
            lan_max_check_attempts: settingsData.lan_max_check_attempts !== undefined ? settingsData.lan_max_check_attempts : 3,
            lan_notify_cooldown_sec: settingsData.lan_notify_cooldown_sec !== undefined ? settingsData.lan_notify_cooldown_sec : 600,
            lan_flapping_threshold: settingsData.lan_flapping_threshold !== undefined ? settingsData.lan_flapping_threshold : 5,
            lan_flapping_window_sec: settingsData.lan_flapping_window_sec !== undefined ? settingsData.lan_flapping_window_sec : 600,
            lan_device_auto_remove_days: settingsData.lan_device_auto_remove_days !== undefined ? settingsData.lan_device_auto_remove_days : 30,
            abnormal_traffic_threshold_mbps: settingsData.abnormal_traffic_threshold_mbps || 100,
            bark_enabled: !!settingsData.bark_enabled,
            bark_server_url: settingsData.bark_server_url || 'https://api.day.app',
            bark_device_key: settingsData.bark_device_key || '',
            bark_group: settingsData.bark_group || 'Netwatch',
            pushplus_enabled: !!settingsData.pushplus_enabled,
            pushplus_token: settingsData.pushplus_token || '',
            pushplus_topic: settingsData.pushplus_topic || '',
            dnd_enabled: !!settingsData.dnd_enabled,
            dnd_start: settingsData.dnd_start || '22:00',
            dnd_end: settingsData.dnd_end || '08:00',
            scheduled_notify_enabled: !!settingsData.scheduled_notify_enabled,
            scheduled_notify_time: settingsData.scheduled_notify_time || '09:00',
            notification_device_ids: settingsData.notification_device_ids || []
        };
        applySettingsToForm();
        if (window.__app.updateAppTrafficRealtime) window.__app.updateAppTrafficRealtime();
    } catch (error) {
        console.error(error);
    }
}

async function saveSettings() {
    var payload = {
        refresh_interval_sec: state.settings.refresh_interval_sec,
        nic_realtime_enabled: !!(els.settingNICRealtimeEnabled && els.settingNICRealtimeEnabled.checked),
        nic_realtime_interval_sec: parseInt((els.settingNICRealtimeIntervalSec && els.settingNICRealtimeIntervalSec.value) || '1', 10) || 1,
        app_traffic_realtime_enabled: !!(els.settingAppTrafficRealtimeEnabled && els.settingAppTrafficRealtimeEnabled.checked),
        host_network_experimental_enabled: !!(els.settingHostNetworkExperimentalEnabled && els.settingHostNetworkExperimentalEnabled.checked),
        chart_time_label_interval: state.settings.chart_time_label_interval,
        background_monitor_enabled: !!(els.settingBackgroundMonitorEnabled && els.settingBackgroundMonitorEnabled.checked),
        background_monitor_interval_sec: parseInt((els.settingBackgroundMonitorIntervalSec && els.settingBackgroundMonitorIntervalSec.value) || '60', 10) || 60,
        notifications_enabled: !!(els.settingNotificationsEnabled && els.settingNotificationsEnabled.checked),
        client_notification_enabled: !!(els.settingClientNotificationEnabled && els.settingClientNotificationEnabled.checked) || !(els.settingBarkEnabled && els.settingBarkEnabled.checked),
        notify_abnormal_traffic: !!(els.settingNotifyAbnormalTraffic && els.settingNotifyAbnormalTraffic.checked),
        notify_egress_change: !!(els.settingNotifyEgressChange && els.settingNotifyEgressChange.checked),
        notify_connectivity_change: !!(els.settingNotifyConnectivityChange && els.settingNotifyConnectivityChange.checked),
        notify_lan_device_change: state.settings.notify_lan_device_change !== false,
        lan_device_offline_after_sec: state.settings.lan_device_offline_after_sec || 180,
        lan_device_online_after_sec: state.settings.lan_device_online_after_sec !== undefined ? state.settings.lan_device_online_after_sec : 0,
        lan_device_offline_notify_delay_sec: state.settings.lan_device_offline_notify_delay_sec !== undefined ? state.settings.lan_device_offline_notify_delay_sec : 120,
        lan_device_online_notify_delay_sec: state.settings.lan_device_online_notify_delay_sec !== undefined ? state.settings.lan_device_online_notify_delay_sec : 120,
        lan_max_check_attempts: state.settings.lan_max_check_attempts !== undefined ? state.settings.lan_max_check_attempts : 3,
        lan_notify_cooldown_sec: state.settings.lan_notify_cooldown_sec !== undefined ? state.settings.lan_notify_cooldown_sec : 600,
        lan_flapping_threshold: state.settings.lan_flapping_threshold !== undefined ? state.settings.lan_flapping_threshold : 5,
        lan_flapping_window_sec: state.settings.lan_flapping_window_sec !== undefined ? state.settings.lan_flapping_window_sec : 600,
        lan_device_auto_remove_days: state.settings.lan_device_auto_remove_days !== undefined ? state.settings.lan_device_auto_remove_days : 30,
        abnormal_traffic_threshold_mbps: parseInt((els.settingAbnormalTrafficThresholdMbps && els.settingAbnormalTrafficThresholdMbps.value) || '100', 10) || 100,
        bark_enabled: !!(els.settingBarkEnabled && els.settingBarkEnabled.checked),
        bark_server_url: (els.settingBarkServerURL && els.settingBarkServerURL.value && els.settingBarkServerURL.value.trim()) || 'https://api.day.app',
        bark_device_key: (els.settingBarkDeviceKey && els.settingBarkDeviceKey.value && els.settingBarkDeviceKey.value.trim()) || '',
        bark_group: (els.settingBarkGroup && els.settingBarkGroup.value && els.settingBarkGroup.value.trim()) || 'Netwatch',
        pushplus_enabled: !!(els.settingPushPlusEnabled && els.settingPushPlusEnabled.checked),
        pushplus_token: (els.settingPushPlusToken && els.settingPushPlusToken.value && els.settingPushPlusToken.value.trim()) || '',
        pushplus_topic: (els.settingPushPlusTopic && els.settingPushPlusTopic.value && els.settingPushPlusTopic.value.trim()) || '',
        dnd_enabled: !!(els.settingDNDEnabled && els.settingDNDEnabled.checked),
        dnd_start: (els.settingDNDStart && els.settingDNDStart.value) || '22:00',
        dnd_end: (els.settingDNDEnd && els.settingDNDEnd.value) || '08:00',
        scheduled_notify_enabled: !!(els.settingScheduledNotifyEnabled && els.settingScheduledNotifyEnabled.checked),
        scheduled_notify_time: (els.settingScheduledNotifyTime && els.settingScheduledNotifyTime.value) || '09:00',
        notification_device_ids: state.settings.notification_device_ids || []
    };


    try {
        var saved;
        if (window.NetwatchAPI) {
            saved = await window.NetwatchAPI.post('/api/v1/settings', payload);
        } else {
            var settingsResp = await fetch('/api/v1/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            if (!settingsResp.ok) throw new Error('settings save failed');
            saved = await settingsResp.json();
        }
        // Always prefer server-normalized settings (clamps, defaults, LAN fields).
        if (saved && typeof saved === 'object') {
            state.settings = Object.assign({}, state.settings, saved);
            if (saved.refresh_interval_sec) {
                state.refreshInterval = saved.refresh_interval_sec;
            }
        } else {
            state.settings = Object.assign({}, state.settings, payload);
            state.refreshInterval = payload.refresh_interval_sec;
        }
        applySettingsToForm();
        state.nicRealtimeInitialized = false;
        if (window.__app.initNICRealtime) window.__app.initNICRealtime();
        if (window.__app.refreshAppTraffic) window.__app.refreshAppTraffic();
        if (window.__app.updateAppTrafficRealtime) window.__app.updateAppTrafficRealtime();
        NetwatchShared.showToast(i18n('save_settings_success'), 'success');
	} catch (error) {
		console.error(error);
		NetwatchShared.showToast(i18n('save_settings_fail') + ': ' + (error.message || ''), 'error');
    }
}

async function testBarkNotification() {
    try {
        await saveSettings();
        if (window.NetwatchAPI) {
            await window.NetwatchAPI.post('/api/v1/notifications/bark/test');
        } else {
            var resp = await fetch('/api/v1/notifications/bark/test', { method: 'POST' });
            if (!resp.ok) {
                var data = await resp.json().catch(function () { return {}; });
                throw new Error(data.error || 'HTTP ' + resp.status);
            }
        }
        NetwatchShared.showToast('Bark \u6D4B\u8BD5\u63A8\u9001\u5DF2\u53D1\u9001', 'success');
    } catch (err) {
        NetwatchShared.showToast('Bark \u6D4B\u8BD5\u5931\u8D25: ' + err.message, 'error');
    }
}

async function testPushPlusNotification() {
    try {
        await saveSettings();
        if (window.NetwatchAPI) {
            await window.NetwatchAPI.post('/api/v1/notifications/pushplus/test');
        } else {
            var resp = await fetch('/api/v1/notifications/pushplus/test', { method: 'POST' });
            if (!resp.ok) {
                var data = await resp.json().catch(function () { return {}; });
                throw new Error(data.error || 'HTTP ' + resp.status);
            }
        }
        NetwatchShared.showToast(i18n('test_pushplus') + ' \u2713', 'success');
    } catch (err) {
        NetwatchShared.showToast('PushPlus \u6D4B\u8BD5\u5931\u8D25: ' + err.message, 'error');
    }
}

async function loadLazycatDevices() {
    try {
        var gateway = await NetwatchShared.getLazycatGateway(state);
        var session = await gateway.session;
        var result = await gateway.devices.ListEndDevices({ uid: session.uid });
        var devices = (result.devices || []).map(function (d) {
            return {
                id: d.uniqueDeivceId || '',
                name: d.remarkName || d.name || d.model || '',
                model: d.model || '',
                isOnline: !!d.isOnline,
                isMobile: !!d.isMobile,
                isCurrent: d.uniqueDeivceId === session.deviceId
            };
        });
        state.lazycatDevices = devices;
        renderNotificationDeviceList();
        return devices;
    } catch (err) {
        console.debug('load lazycat devices failed', err);
        state.lazycatDevices = [];
        renderNotificationDeviceList();
        return [];
    }
}

function renderNotificationDeviceList() {
    var container = document.getElementById('notification-device-list');
    var summaryEl = document.getElementById('notification-device-summary');
    if (!container) return;
    var devices = state.lazycatDevices || [];
    var selectedIDs = new Set(state.settings.notification_device_ids || []);

    if (devices.length === 0) {
        container.innerHTML = '<div class="placeholder">' + i18n('no_devices_registered') + '</div>';
        if (summaryEl) summaryEl.textContent = i18n('no_devices_registered');
        return;
    }

    container.innerHTML = devices.map(function (dev) {
        var isSelected = selectedIDs.size === 0 || selectedIDs.has(dev.id);
        var label = NetwatchShared.escapeHtml(dev.name || dev.model || dev.id);
        return '<label class="notification-device-item ' + (isSelected ? 'selected' : '') + '"><input type="checkbox" data-device-id="' + NetwatchShared.escapeHtml(dev.id) + '" ' + (isSelected ? 'checked' : '') + '><div class="notification-device-status ' + (dev.isOnline ? 'online' : '') + '"></div><div class="notification-device-name">' + label + (dev.isCurrent ? ' (\u5F53\u524D)' : '') + '</div></label>';
    }).join('');

    container.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        cb.addEventListener('change', function () {
            var checked = Array.from(container.querySelectorAll('input[type="checkbox"]:checked')).map(function (el) { return el.dataset.deviceId; });
            state.settings.notification_device_ids = checked;
            updateDeviceSummary();
        });
    });

    updateDeviceSummary();

    function updateDeviceSummary() {
        if (!summaryEl) return;
        var total = devices.length;
        var checkedCount = container.querySelectorAll('input[type="checkbox"]:checked').length;
        if (checkedCount === total || checkedCount === 0) {
            summaryEl.textContent = '\u5168\u90E8\u8BBE\u5907 (' + total + ')';
        } else {
            summaryEl.textContent = checkedCount + ' / ' + total + ' \u53F0';
        }
    }
}

function disableClientNotifications() {
    state.notificationUnsupported = true;
    localStorage.setItem('netwatch_notification_unsupported', 'true');
}

function markNotificationSeen(id) {
    NetwatchShared.markNotificationSeen(id, state);
}

function notifySelectedDevices(event) {
    return NetwatchShared.notifySelectedDevices(event, state);
}

function handleNotificationEvent(event) {
    NetwatchShared.handleNotificationEvent(event, state);
}

function isNotificationUnsupportedError(err) {
    return NetwatchShared.isNotificationUnsupportedError(err);
}

window.__app.applySettingsToForm = applySettingsToForm;
window.__app.loadSettings = loadSettings;
window.__app.saveSettings = saveSettings;
window.__app.testBarkNotification = testBarkNotification;
window.__app.testPushPlusNotification = testPushPlusNotification;
window.__app.loadLazycatDevices = loadLazycatDevices;
window.__app.renderNotificationDeviceList = renderNotificationDeviceList;
window.__app.disableClientNotifications = disableClientNotifications;
window.__app.markNotificationSeen = markNotificationSeen;
window.__app.notifySelectedDevices = notifySelectedDevices;
window.__app.handleNotificationEvent = handleNotificationEvent;
window.__app.isNotificationUnsupportedError = isNotificationUnsupportedError;
})();
