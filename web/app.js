/*
 * Netwatch — frontend boot entry point.
 * app.js has been split into domain modules for maintainability.
 * This file is kept as the canonical boot location.
 */

window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

function initTheme() {
    if (state.themeInitialized) return;
    state.themeInitialized = true;
    NetwatchShared.initTheme(state, els.themeToggle);
}

function closeSpeedHistory(restoreFocus) {
    if (restoreFocus === undefined) restoreFocus = true;
    if (els.broadbandHistoryWindow) els.broadbandHistoryWindow.classList.remove('active');
    if (els.transferHistoryWindow) els.transferHistoryWindow.classList.remove('active');
    if (els.speedHistoryBackdrop) els.speedHistoryBackdrop.classList.remove('active');
    state.activeSpeedHistory = null;
    var trigger = state.speedHistoryTrigger;
    state.speedHistoryTrigger = null;
    if (restoreFocus && trigger && typeof trigger.focus === 'function') {
        try { trigger.focus(); } catch (_) {}
    }
}

function openSpeedHistory(kind) {
    var broadband = kind === 'broadband';
    var parentWindow = broadband ? els.broadbandWindow : els.transferWindow;
    var historyWindow = broadband ? els.broadbandHistoryWindow : els.transferHistoryWindow;
    if (!parentWindow || !parentWindow.classList.contains('active') || !historyWindow) return;
    if (window.closeCustomSelects) window.closeCustomSelects();
    closeSpeedHistory(false);
    state.speedHistoryTrigger = document.activeElement;
    state.activeSpeedHistory = kind;
    historyWindow.classList.add('active');
    if (els.speedHistoryBackdrop) els.speedHistoryBackdrop.classList.add('active');
    if (window.__app.loadSpeedHistory) window.__app.loadSpeedHistory();
    var closeButton = broadband ? els.closeBroadbandHistoryWindow : els.closeTransferHistoryWindow;
    if (closeButton) setTimeout(function () { closeButton.focus(); }, 0);
}

function openWindow(name) {
    if (state.runningTest && state.runningTest !== name) return;
    if (window.closeCustomSelects) window.closeCustomSelects();
    closeSpeedHistory(false);

    els.settingsWindow.classList.remove('active');
    els.broadbandWindow.classList.remove('active');
    els.transferWindow.classList.remove('active');
    if (els.networkConfigWindow) els.networkConfigWindow.classList.remove('active');
    if (els.notificationSettingsWindow) els.notificationSettingsWindow.classList.remove('active');

    if (name === 'settings') {
        els.settingsWindow.classList.add('active');
    } else if (name === 'broadband') {
        els.broadbandWindow.classList.add('active');
    } else if (name === 'transfer') {
        els.transferWindow.classList.add('active');
    } else if (name === 'network-config') {
        if (els.networkConfigWindow) els.networkConfigWindow.classList.add('active');
        if (window.__app.loadNetworkConfigDevices) window.__app.loadNetworkConfigDevices();
        if (window.__app.loadNetworkConfigPending) window.__app.loadNetworkConfigPending(false);
        if (window.__app.bindHostBridgeUI) window.__app.bindHostBridgeUI();
        if (window.__app.loadHostBridges) window.__app.loadHostBridges();
        if (window.__app.bindHostDNSUI) window.__app.bindHostDNSUI();
        if (window.__app.loadHostDNS) window.__app.loadHostDNS();
    } else if (name === 'notification-settings') {
        if (els.notificationSettingsWindow) els.notificationSettingsWindow.classList.add('active');
        if (window.__app.loadLazycatDevices) window.__app.loadLazycatDevices();
    }

    els.backdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
    state.activeWindow = name;
    window.__app.updateWindowControls();
    if (name === 'broadband' || name === 'transfer') {
        if (window.__app.loadSpeedHistory) window.__app.loadSpeedHistory();
        if (name === 'broadband' && window.__app.loadBroadbandNodes) window.__app.loadBroadbandNodes();
    }
}

window.__app.openWindow = openWindow;
window.__app.openSpeedHistory = openSpeedHistory;
window.__app.closeSpeedHistory = closeSpeedHistory;

async function closeCurrentWindow() {
    if (window.closeCustomSelects) window.closeCustomSelects();
    closeSpeedHistory(false);
    var closingNetworkConfig = !!(els.networkConfigWindow && els.networkConfigWindow.classList.contains('active'));
    if ((state.runningTest === 'broadband' && els.broadbandWindow.classList.contains('active')) ||
        (state.runningTest === 'transfer' && els.transferWindow.classList.contains('active'))) {
        var ok = await NetwatchShared.confirmDialog({
            title: i18n('confirm_cancel_title') || '取消测速',
            message: i18n('confirm_cancel_test'),
            okText: i18n('confirm_cancel_ok') || '确定取消',
            cancelText: i18n('confirm_cancel_keep') || '继续测速'
        });
        if (!ok) return;
        if (state.runningTest === 'broadband' && window.__app.cancelBroadbandTest) {
            window.__app.cancelBroadbandTest(true);
        }
        if (state.runningTest === 'transfer' && window.__app.cancelTransferTest) {
            window.__app.cancelTransferTest(true);
        }
    }

    els.settingsWindow.classList.remove('active');
    els.broadbandWindow.classList.remove('active');
    els.transferWindow.classList.remove('active');
    if (els.networkConfigWindow) els.networkConfigWindow.classList.remove('active');
    if (els.notificationSettingsWindow) els.notificationSettingsWindow.classList.remove('active');
    els.backdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
    state.activeWindow = null;
    window.__app.updateWindowControls();
    if (closingNetworkConfig && window.__app.clearNetworkConfigLogs) {
        window.__app.clearNetworkConfigLogs();
    }
}

function openTraceWindow() {
    if (els.traceWindow) els.traceWindow.classList.add('active');
    if (els.traceBackdrop) els.traceBackdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
}

function closeTraceWindow() {
    // Only close the hop details sheet. Keep background polling so the card
    // status does not freeze on "追踪中". Use the explicit Stop button to cancel.
    if (els.traceWindow) els.traceWindow.classList.remove('active');
    if (els.traceBackdrop) els.traceBackdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
}

function openDNSDiagWindow() {
    if (els.dnsDiagWindow) els.dnsDiagWindow.classList.add('active');
    if (els.dnsDiagBackdrop) els.dnsDiagBackdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
    if (typeof window.initDNSDiagnostics === 'function') window.initDNSDiagnostics();
    var input = document.getElementById('dns-diag-name');
    if (input) setTimeout(function () { input.focus(); }, 0);
}

function closeDNSDiagWindow() {
    if (els.dnsDiagWindow) els.dnsDiagWindow.classList.remove('active');
    if (els.dnsDiagBackdrop) els.dnsDiagBackdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
}

window.__app.openTraceWindow = openTraceWindow;
window.__app.closeTraceWindow = closeTraceWindow;
window.__app.openDNSDiagWindow = openDNSDiagWindow;
window.__app.closeDNSDiagWindow = closeDNSDiagWindow;

function bindControls() {
    if (state.controlsBound) return;
    state.controlsBound = true;

    var A = window.__app;

    els.websiteRefreshBtn.addEventListener('click', function () { A.debounce('website', function () { if (A.runWebsiteRefresh) A.runWebsiteRefresh(); }, 80); });
    if (els.natRefreshBtn) els.natRefreshBtn.addEventListener('click', function () { A.debounce('nat', function () { if (A.runNATRefresh) A.runNATRefresh(); }); });
    els.openSettingsWindow.addEventListener('click', function () { openWindow('settings'); });
    els.openBroadbandWindow.addEventListener('click', function () { openWindow('broadband'); });
    els.openTransferWindow.addEventListener('click', function () { openWindow('transfer'); });
    if (els.openBroadbandHistory) els.openBroadbandHistory.addEventListener('click', function () { openSpeedHistory('broadband'); });
    if (els.openTransferHistory) els.openTransferHistory.addEventListener('click', function () { openSpeedHistory('transfer'); });
    if (els.openNetworkConfigWindow) els.openNetworkConfigWindow.addEventListener('click', function () { openWindow('network-config'); });
    if (els.interfacesRefreshBtn) els.interfacesRefreshBtn.addEventListener('click', function () {
        if (els.interfacesRefreshBtn.disabled) return;
        if (window.__app.refreshInterfacesOnly) window.__app.refreshInterfacesOnly();
    });
    var networkPreflightBtn = document.getElementById('network-config-preflight-btn');
    if (networkPreflightBtn) networkPreflightBtn.addEventListener('click', function () {
        if (window.__app.preflightNetworkConfig) window.__app.preflightNetworkConfig();
    });
    ['network-config-method', 'network-config-address', 'network-config-gateway', 'network-config-dns'].forEach(function (id) {
        var field = document.getElementById(id);
        if (field) {
            field.addEventListener('input', function () {
                if (window.__app.updateNetworkConfigApplyState) window.__app.updateNetworkConfigApplyState();
            });
            field.addEventListener('change', function () {
                if (window.__app.updateNetworkConfigApplyState) window.__app.updateNetworkConfigApplyState();
            });
        }
    });
    els.closeSettingsWindow.addEventListener('click', closeCurrentWindow);
    els.closeBroadbandWindow.addEventListener('click', closeCurrentWindow);
    els.closeTransferWindow.addEventListener('click', closeCurrentWindow);
    if (els.closeBroadbandHistoryWindow) els.closeBroadbandHistoryWindow.addEventListener('click', function () { closeSpeedHistory(true); });
    if (els.closeTransferHistoryWindow) els.closeTransferHistoryWindow.addEventListener('click', function () { closeSpeedHistory(true); });
    if (els.closeNetworkConfigWindow) els.closeNetworkConfigWindow.addEventListener('click', closeCurrentWindow);
    if (els.closeNotificationSettings) els.closeNotificationSettings.addEventListener('click', closeCurrentWindow);
    if (els.openNotificationSettings) els.openNotificationSettings.addEventListener('click', function () { openWindow('notification-settings'); });
    if (els.saveNotificationSettings) els.saveNotificationSettings.addEventListener('click', function () { if (A.saveSettings) A.saveSettings(); });

    var openNotifyTemplate = document.getElementById('open-notify-template');
    if (openNotifyTemplate) {
        openNotifyTemplate.addEventListener('click', function () {
            var w = document.getElementById('notify-template-window');
            if (w) w.classList.add('active');
        });
    }

    var deviceTrigger = document.getElementById('notification-device-trigger');
    var devicePanel = document.getElementById('notification-device-panel');
    if (deviceTrigger && devicePanel) {
        deviceTrigger.addEventListener('click', function (e) {
            e.stopPropagation();
            devicePanel.classList.toggle('open');
        });
        document.addEventListener('click', function (e) {
            if (!devicePanel.contains(e.target) && !deviceTrigger.contains(e.target)) {
                devicePanel.classList.remove('open');
            }
        });
    }

    els.backdrop.addEventListener('click', closeCurrentWindow);
    if (els.speedHistoryBackdrop) els.speedHistoryBackdrop.addEventListener('click', function () { closeSpeedHistory(true); });
    if (els.closeTraceWindow) els.closeTraceWindow.addEventListener('click', closeTraceWindow);
    if (els.traceBackdrop) els.traceBackdrop.addEventListener('click', closeTraceWindow);
    if (els.openDNSDiagWindow) els.openDNSDiagWindow.addEventListener('click', openDNSDiagWindow);
    if (els.closeDNSDiagWindow) els.closeDNSDiagWindow.addEventListener('click', closeDNSDiagWindow);
    if (els.dnsDiagBackdrop) els.dnsDiagBackdrop.addEventListener('click', closeDNSDiagWindow);
    document.addEventListener('keydown', function (event) {
        if (event.key === 'Escape' && state.activeSpeedHistory) {
            event.preventDefault();
            event.stopPropagation();
            closeSpeedHistory(true);
            return;
        }
        if (event.key === 'Escape' && els.dnsDiagWindow && els.dnsDiagWindow.classList.contains('active')) {
            closeDNSDiagWindow();
        }
    });

    var ipv6DetailBtn = document.getElementById('ipv6-detail-btn');
    if (ipv6DetailBtn && !ipv6DetailBtn.dataset.bound) {
        ipv6DetailBtn.dataset.bound = '1';
        ipv6DetailBtn.addEventListener('click', function (ev) {
            ev.preventDefault();
            ev.stopPropagation();
            var open = (window.__app && window.__app.openIPv6DetailWindow) || A.openIPv6DetailWindow;
            if (typeof open === 'function') open();
            else console.warn('openIPv6DetailWindow missing');
        });
    }

    var closeIPv6Detail = document.getElementById('close-ipv6-detail-window');
    if (closeIPv6Detail) closeIPv6Detail.addEventListener('click', function () { if (A.closeIPv6DetailWindow) A.closeIPv6DetailWindow(); });

    var ipv6DetailBackdrop = document.getElementById('ipv6-detail-window-backdrop');
    if (ipv6DetailBackdrop) ipv6DetailBackdrop.addEventListener('click', function () { if (A.closeIPv6DetailWindow) A.closeIPv6DetailWindow(); });

    var closeIPv6Renew = document.getElementById('close-ipv6-renew-window');
    if (closeIPv6Renew) closeIPv6Renew.addEventListener('click', function () { if (A.closeIPv6RenewWindow) A.closeIPv6RenewWindow(); });

    var ipv6RenewBackdrop = document.getElementById('ipv6-renew-window-backdrop');
    if (ipv6RenewBackdrop) ipv6RenewBackdrop.addEventListener('click', function () { if (A.closeIPv6RenewWindow) A.closeIPv6RenewWindow(); });

    var ipv6RenewRefreshBtn = document.getElementById('ipv6-renew-refresh-btn');
    if (ipv6RenewRefreshBtn) ipv6RenewRefreshBtn.addEventListener('click', function () { if (A.loadIPv6RenewNICs) A.loadIPv6RenewNICs(); });

    var ipv6RenewExecBtn = document.getElementById('ipv6-renew-exec-btn');
    if (ipv6RenewExecBtn) ipv6RenewExecBtn.addEventListener('click', function () { if (A.runIPv6Renew) A.runIPv6Renew(); });

    var networkConfigRefreshBtn = document.getElementById('network-config-refresh-btn');
    if (networkConfigRefreshBtn) networkConfigRefreshBtn.addEventListener('click', function () { if (A.loadNetworkConfigDevices) A.loadNetworkConfigDevices(); });
    if (A.bindHostBridgeUI) A.bindHostBridgeUI();
    if (A.loadHostBridges) A.loadHostBridges();
    if (A.bindHostDNSUI) A.bindHostDNSUI();
    if (A.loadHostDNS) A.loadHostDNS();
    var networkConfigMethod = document.getElementById('network-config-method');
    if (networkConfigMethod) networkConfigMethod.addEventListener('change', function () {
        if (A.updateNetworkConfigMethodState) A.updateNetworkConfigMethodState();
        if (A.updateNetworkConfigApplyState) A.updateNetworkConfigApplyState();
    });
    var networkConfigApplyBtn = document.getElementById('network-config-apply-btn');
    if (networkConfigApplyBtn) networkConfigApplyBtn.addEventListener('click', function () { if (A.applyNetworkConfig) A.applyNetworkConfig(); });
    var networkMACInput = document.getElementById('network-config-mac');
    if (networkMACInput) networkMACInput.addEventListener('input', function () { if (A.updateNetworkMACApplyState) A.updateNetworkMACApplyState(); });
    var networkMACApplyBtn = document.getElementById('network-mac-apply-btn');
    if (networkMACApplyBtn) networkMACApplyBtn.addEventListener('click', function () { if (A.applyNetworkMAC) A.applyNetworkMAC(); });
    var networkMACRestoreBtn = document.getElementById('network-mac-restore-btn');
    if (networkMACRestoreBtn) networkMACRestoreBtn.addEventListener('click', function () { if (A.restoreOriginalNetworkMAC) A.restoreOriginalNetworkMAC(); });
    var networkMACConfirmBtn = document.getElementById('network-mac-confirm-btn');
    if (networkMACConfirmBtn) networkMACConfirmBtn.addEventListener('click', function () { if (A.confirmNetworkConfig) A.confirmNetworkConfig(); });
    var networkMACRollbackBtn = document.getElementById('network-mac-rollback-btn');
    if (networkMACRollbackBtn) networkMACRollbackBtn.addEventListener('click', function () { if (A.rollbackNetworkConfig) A.rollbackNetworkConfig(); });
    var networkConfigRestartBtn = document.getElementById('network-config-restart-btn');
    if (networkConfigRestartBtn) networkConfigRestartBtn.addEventListener('click', function () { if (A.restartNetworkConfigDevice) A.restartNetworkConfigDevice(); });
    var networkConfigConfirmBtn = document.getElementById('network-config-confirm-btn');
    if (networkConfigConfirmBtn) networkConfigConfirmBtn.addEventListener('click', function () { if (A.confirmNetworkConfig) A.confirmNetworkConfig(); });
    var networkConfigRollbackBtn = document.getElementById('network-config-rollback-btn');
    if (networkConfigRollbackBtn) networkConfigRollbackBtn.addEventListener('click', function () { if (A.rollbackNetworkConfig) A.rollbackNetworkConfig(); });

    if (A.bindIPv6TitleEasterEgg) A.bindIPv6TitleEasterEgg();

    els.runBroadbandTest.addEventListener('click', function () { if (A.startBroadbandTest) A.startBroadbandTest(); });
    if (els.broadbandModeClient) els.broadbandModeClient.addEventListener('click', function () { if (A.setBroadbandMode) A.setBroadbandMode('client'); });
    if (els.broadbandModeServer) els.broadbandModeServer.addEventListener('click', function () { if (A.setBroadbandMode) A.setBroadbandMode('server'); });
    if (els.broadbandModePortPolicy) els.broadbandModePortPolicy.addEventListener('click', function () { if (A.setBroadbandMode) A.setBroadbandMode('port-policy'); });
    if (els.broadbandNodeRefresh) els.broadbandNodeRefresh.addEventListener('click', function () { if (A.loadBroadbandNodes) A.loadBroadbandNodes(); });
    if (els.clearBroadbandHistory) els.clearBroadbandHistory.addEventListener('click', function () { if (A.clearSpeedHistory) A.clearSpeedHistory('broadband'); });
    els.runTransferTest.addEventListener('click', function () { if (A.runTransferTest) A.runTransferTest(); });
    if (els.clearTransferHistory) els.clearTransferHistory.addEventListener('click', function () { if (A.clearSpeedHistory) A.clearSpeedHistory('local'); });
    if (els.saveSettings) els.saveSettings.addEventListener('click', function () { if (A.saveSettings) A.saveSettings(); });

    if (els.settingNICRealtimeEnabled) {
        els.settingNICRealtimeEnabled.addEventListener('change', function () {
            var isEnabled = !!els.settingNICRealtimeEnabled.checked;
            state.settings.nic_realtime_enabled = isEnabled;
            if (els.settingNICRealtimeIntervalSec) {
                els.settingNICRealtimeIntervalSec.disabled = !isEnabled;
                // The visible control is a custom-select wrapper; sync its
                // disabled state immediately instead of waiting for a reload.
                if (window.syncCustomSelect) window.syncCustomSelect(els.settingNICRealtimeIntervalSec);
            }
        });
    }

    if (els.settingAppTrafficRealtimeEnabled) {
        els.settingAppTrafficRealtimeEnabled.addEventListener('change', function () {
            state.settings.app_traffic_realtime_enabled = !!els.settingAppTrafficRealtimeEnabled.checked;
            if (A.updateAppTrafficRealtime) A.updateAppTrafficRealtime();
        });
    }

    if (els.settingHostNetworkExperimentalEnabled) {
        els.settingHostNetworkExperimentalEnabled.addEventListener('change', function () {
            state.settings.host_network_experimental_enabled = !!els.settingHostNetworkExperimentalEnabled.checked;
        });
    }

    if (els.settingBackgroundMonitorEnabled) {
        els.settingBackgroundMonitorEnabled.addEventListener('change', function () {
            state.settings.background_monitor_enabled = !!els.settingBackgroundMonitorEnabled.checked;
            if (A.applySettingsToForm) A.applySettingsToForm();
        });
    }

    if (els.settingNotificationsEnabled) {
        els.settingNotificationsEnabled.addEventListener('change', function () {
            state.settings.notifications_enabled = !!els.settingNotificationsEnabled.checked;
            if (A.applySettingsToForm) A.applySettingsToForm();
        });
    }

    if (els.settingClientNotificationEnabled) {
        els.settingClientNotificationEnabled.addEventListener('change', function () {
            state.settings.client_notification_enabled = !!els.settingClientNotificationEnabled.checked;
            if (A.applySettingsToForm) A.applySettingsToForm();
        });
    }

    if (els.settingNotifyAbnormalTraffic) {
        els.settingNotifyAbnormalTraffic.addEventListener('change', function () {
            state.settings.notify_abnormal_traffic = !!els.settingNotifyAbnormalTraffic.checked;
            if (A.applySettingsToForm) A.applySettingsToForm();
        });
    }

    if (els.settingNotifyEgressChange) {
        els.settingNotifyEgressChange.addEventListener('change', function () {
            state.settings.notify_egress_change = !!els.settingNotifyEgressChange.checked;
        });
    }

    if (els.settingNotifyConnectivityChange) {
        els.settingNotifyConnectivityChange.addEventListener('change', function () {
            state.settings.notify_connectivity_change = !!els.settingNotifyConnectivityChange.checked;
        });
    }

    if (els.settingBarkEnabled) {
        els.settingBarkEnabled.addEventListener('change', function () {
            state.settings.bark_enabled = !!els.settingBarkEnabled.checked;
            if (!state.settings.bark_enabled) {
                state.settings.client_notification_enabled = true;
            }
            if (A.applySettingsToForm) A.applySettingsToForm();
        });
    }

    if (els.testBarkNotification) els.testBarkNotification.addEventListener('click', function () { if (A.testBarkNotification) A.testBarkNotification(); });
    if (els.testPushPlusNotification) els.testPushPlusNotification.addEventListener('click', function () { if (A.testPushPlusNotification) A.testPushPlusNotification(); });

    if (els.settingDNDEnabled) {
        els.settingDNDEnabled.addEventListener('change', function () {
            state.settings.dnd_enabled = !!els.settingDNDEnabled.checked;
        });
    }

    if (els.settingDNDStart) {
        els.settingDNDStart.addEventListener('change', function () {
            state.settings.dnd_start = els.settingDNDStart.value || '22:00';
        });
    }

    if (els.settingDNDEnd) {
        els.settingDNDEnd.addEventListener('change', function () {
            state.settings.dnd_end = els.settingDNDEnd.value || '08:00';
        });
    }
}

function setSSEStatus(status) {
    var el = document.getElementById('sse-status');
    if (!el) return;
    var label = {
        idle: 'IDLE',
        connecting: 'CONNECTING',
        reconnecting: 'RECONNECTING',
        live: 'LIVE',
        degraded: 'DEGRADED',
        offline: 'OFFLINE',
        closed: 'CLOSED'
    }[status] || String(status || 'UNKNOWN').toUpperCase();
    el.textContent = label;
    el.className = 'live-chip is-' + (status || 'idle');
    el.title = '实时连接: ' + label;
}


function applyCapabilities(caps) {
    var el = document.getElementById('capability-strip');
    if (!el || !caps) return;
    var items = [];
    items.push(caps.trace ? 'TRACE' : 'NO-TRACE');
    items.push(caps.network_config ? 'NETCFG' : 'NO-NETCFG');
    items.push(caps.docker_socket ? 'DOCKER' : 'NO-DOCKER');
    items.push(caps.app_traffic ? 'TRAFFIC' : 'NO-TRAFFIC');
    items.push(caps.container_control ? 'CTL' : 'NO-CTL');
    el.textContent = items.join(' · ');
    el.title = (caps.notes || []).join('\n');
    el.classList.toggle('is-degraded', !caps.trace || !caps.network_config);
}

async function loadCapabilities() {
    try {
        var caps = window.NetwatchAPI
            ? await window.NetwatchAPI.get('/api/v1/capabilities')
            : await (await fetch('/api/v1/capabilities', { cache: 'no-store' })).json();
        state.capabilities = caps;
        applyCapabilities(caps);
    } catch (err) {
        console.debug('capabilities unavailable', err);
    }
}

function initSSE() {
    if (state.sse) return;
    setSSEStatus('connecting');
    try {
        if (window.NetwatchAPI && typeof window.NetwatchAPI.createSSE === 'function') {
            state.sse = window.NetwatchAPI.createSSE('/api/v1/events', {
                onStatus: setSSEStatus,
                getSince: function () {
                    return Number(state.notificationLastID || 0) || 0;
                },
                events: {
                    summary: function (summary) {
                        if (!summary) return;
                        if (window.__app.applyIncomingSummary) {
                            window.__app.applyIncomingSummary(summary);
                        } else if (window.__app.renderSummary) {
                            window.__app.renderSummary(summary);
                        } else {
                            state.summary = summary;
                        }
                    },
                    notification: function (payload) {
                        if (payload && window.__app.handleNotificationEvent) window.__app.handleNotificationEvent(payload);
                    },
                    nic_realtime: function (payload) {
                        if (!payload) return;
                        if (state.settings.nic_realtime_enabled && typeof window.renderNICRealtime === 'function') {
                            window.renderNICRealtime(payload);
                        }
                    },
                    lan_devices: function (payload) {
                        if (payload && window.__app.renderLANDevices) window.__app.renderLANDevices(payload);
                    }
                }
            });
        } else {
            var since = Number(state.notificationLastID || 0) || 0;
            var es = new EventSource('/api/v1/events?since=' + encodeURIComponent(String(since)));
            es.addEventListener('summary', function (ev) {
                try {
                    var summary = JSON.parse(ev.data);
                    if (window.__app.applyIncomingSummary) {
                        window.__app.applyIncomingSummary(summary);
                    } else if (window.__app.renderSummary) {
                        window.__app.renderSummary(summary);
                    } else {
                        state.summary = summary;
                    }
                } catch (_) {}
            });
            es.addEventListener('notification', function (ev) {
                try {
                    if (window.__app.handleNotificationEvent) window.__app.handleNotificationEvent(JSON.parse(ev.data));
                } catch (_) {}
            });
            es.addEventListener('nic_realtime', function (ev) {
                try {
                    if (state.settings.nic_realtime_enabled && typeof window.renderNICRealtime === 'function') {
                        window.renderNICRealtime(JSON.parse(ev.data));
                    }
                } catch (_) {}
            });
            es.onopen = function () { setSSEStatus('live'); };
            es.onerror = function () { setSSEStatus('degraded'); };
            state.sse = es;
        }
        window.addEventListener('pagehide', function () {
            if (state.sse && state.sse.close) state.sse.close();
            state.sse = null;
            setSSEStatus('closed');
        });
        window.addEventListener('beforeunload', function () {
            if (state.sse && state.sse.close) state.sse.close();
            state.sse = null;
        });
    } catch (_) {
        setSSEStatus('offline');
    }
}

function initDashboardPanelCollapse() {
    if (state.dashboardPanelCollapseInitialized) return;
    state.dashboardPanelCollapseInitialized = true;
    var allowed = { app_traffic: true, host_ports: true };
    var collapsed = new Set((state.settings.dashboard_collapsed_sections || []).filter(function (key) {
        return !!allowed[key];
    }));
    var compactViewport = !!(window.matchMedia && window.matchMedia('(max-width: 640px)').matches);
    var mobileHostPortsPreferenceKey = 'netwatch_mobile_host_ports_panel_v1';
    var mobileHostPortsPreference = compactViewport ? localStorage.getItem(mobileHostPortsPreferenceKey) : null;
    if (compactViewport) {
        if (mobileHostPortsPreference === 'expanded') collapsed.delete('host_ports');
        else collapsed.add('host_ports');
    }
    var saveQueue = Promise.resolve();

    var paint = function () {
        document.querySelectorAll('[data-dashboard-section]').forEach(function (section) {
            var key = section.dataset.dashboardSection;
            var isCollapsed = collapsed.has(key);
            section.classList.toggle('collapsed', isCollapsed);
            var button = section.querySelector('[data-collapse-section="' + key + '"]');
            if (!button) return;
            var label = i18n(isCollapsed ? 'expand_panel' : 'collapse_panel');
            button.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true');
            button.setAttribute('title', label);
            button.setAttribute('aria-label', label);
        });
    };

    var persist = function () {
        var sections = Array.from(collapsed);
        state.settings.dashboard_collapsed_sections = sections;
        saveQueue = saveQueue.then(function () {
            return window.NetwatchAPI.post('/api/v1/settings', {
                dashboard_collapsed_sections: sections
            });
        }).then(function (saved) {
            var confirmed = saved && Array.isArray(saved.dashboard_collapsed_sections)
                ? saved.dashboard_collapsed_sections.filter(function (key) { return !!allowed[key]; })
                : sections;
            collapsed = new Set(confirmed);
            state.settings.dashboard_collapsed_sections = confirmed;
            paint();
        }).catch(function (error) {
            console.error('save dashboard collapsed sections:', error);
            return window.NetwatchAPI.get('/api/v1/settings').then(function (settings) {
                var confirmed = settings && Array.isArray(settings.dashboard_collapsed_sections)
                    ? settings.dashboard_collapsed_sections.filter(function (key) { return !!allowed[key]; })
                    : [];
                collapsed = new Set(confirmed);
                state.settings.dashboard_collapsed_sections = confirmed;
                paint();
            }).catch(function () {});
        });
    };

    document.querySelectorAll('[data-collapse-section]').forEach(function (button) {
        button.addEventListener('click', function () {
            var key = button.dataset.collapseSection;
            if (!allowed[key]) return;
            if (collapsed.has(key)) collapsed.delete(key);
            else collapsed.add(key);
            if (compactViewport && key === 'host_ports') {
                localStorage.setItem(mobileHostPortsPreferenceKey, collapsed.has(key) ? 'collapsed' : 'expanded');
            }
            paint();
            persist();
        });
    });
    paint();
}

function initWithRetry(maxRetries) {
    if (maxRetries === undefined) maxRetries = 3;
    bindControls();
    if (window.__app.resetBroadbandMetrics) window.__app.resetBroadbandMetrics();
    if (window.__app.resetTransferMetrics) window.__app.resetTransferMetrics();

    Promise.all([
        window.__app.loadSpeedConfig ? window.__app.loadSpeedConfig() : Promise.resolve(),
        window.__app.loadSummary ? window.__app.loadSummary(false, false) : Promise.resolve(),
        window.__app.loadSpeedHistory ? window.__app.loadSpeedHistory() : Promise.resolve(),
        window.__app.loadSettings ? window.__app.loadSettings() : Promise.resolve()
    ]).then(function () {
        initDashboardPanelCollapse();
        var appTrafficWasInitialized = state.appTrafficInitialized;
        if (!appTrafficWasInitialized && window.__app.initAppTraffic) {
            window.__app.initAppTraffic();
        }
        window.__app.updateWindowControls();
        if (window.__app.initNICRealtime) window.__app.initNICRealtime();
        if (appTrafficWasInitialized && window.__app.refreshAppTraffic) {
            window.__app.refreshAppTraffic();
        }
    }).then(function () {
        if (!state.summary || !state.summary.ready) {
            if (maxRetries > 0) {
                setTimeout(function () { initWithRetry(maxRetries - 1); }, 2000);
            }
        }
    }).catch(function () {
        initDashboardPanelCollapse();
        if (!state.appTrafficInitialized && window.__app.initAppTraffic) {
            window.__app.initAppTraffic();
        }
    });

    initSSE();
    loadCapabilities();
    if (window.__app.initTrace) window.__app.initTrace();
    if (window.__app.initEgressLookups) window.__app.initEgressLookups();
    if (window.__app.initHostPorts) window.__app.initHostPorts();
}

function boot() {
    if (state.initialized) return;
    state.initialized = true;
    initTheme();
    if (NetwatchShared.initLazycatFullscreen) NetwatchShared.initLazycatFullscreen();
    initWithRetry();
    setTimeout(function () {
        // After reconnect/reload: auto-open confirm UI if a change is still pending
        // (same contract for NIC config and host bridge).
        if (window.__app.loadNetworkConfigPending) window.__app.loadNetworkConfigPending(true);
        if (window.__app.loadHostBridgePending) window.__app.loadHostBridgePending(true);
        if (window.__app.loadHostDNSPending) window.__app.loadHostDNSPending(true);
    }, 800);

    document.addEventListener('visibilitychange', function () {
        if (!document.hidden) {
            // Refresh the view from the server's current snapshot without
            // starting a new website/network probe when returning to the tab.
            if (window.__app.loadSummary) window.__app.loadSummary(false, false);
            // Re-check pending after tab focus / network recovery.
            if (window.__app.loadNetworkConfigPending) window.__app.loadNetworkConfigPending(true);
            if (window.__app.loadHostBridgePending) window.__app.loadHostBridgePending(true);
            if (window.__app.loadHostDNSPending) window.__app.loadHostDNSPending(true);
        }
    });
}

setTimeout(boot, 100);
})();
