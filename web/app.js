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

function openWindow(name) {
    if (state.runningTest && state.runningTest !== name) return;
    if (window.closeCustomSelects) window.closeCustomSelects();

    els.settingsWindow.classList.remove('active');
    els.broadbandWindow.classList.remove('active');
    els.transferWindow.classList.remove('active');
    if (els.networkConfigWindow) els.networkConfigWindow.classList.remove('active');
    if (els.notificationSettingsWindow) els.notificationSettingsWindow.classList.remove('active');
    var tsWin = document.getElementById('traffic-settings-window');
    if (tsWin) tsWin.classList.remove('active');

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
    }
}

window.__app.openWindow = openWindow;

async function closeCurrentWindow() {
    if (window.closeCustomSelects) window.closeCustomSelects();
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
    var tsWin = document.getElementById('traffic-settings-window');
    if (tsWin) tsWin.classList.remove('active');
    els.backdrop.classList.remove('active');
    NetwatchShared.unlockModalScroll();
    state.activeWindow = null;
    window.__app.updateWindowControls();
}

function openTraceWindow() {
    if (els.traceWindow) els.traceWindow.classList.add('active');
    if (els.traceBackdrop) els.traceBackdrop.classList.add('active');
    NetwatchShared.lockModalScroll();
}

function closeTraceWindow() {
    if (els.traceWindow) els.traceWindow.classList.remove('active');
    if (els.traceBackdrop) els.traceBackdrop.classList.remove('active');
    if (state.tracePoller) {
        clearInterval(state.tracePoller);
        state.tracePoller = null;
    }
    NetwatchShared.unlockModalScroll();
}

window.__app.openTraceWindow = openTraceWindow;
window.__app.closeTraceWindow = closeTraceWindow;

function bindControls() {
    if (state.controlsBound) return;
    state.controlsBound = true;

    var A = window.__app;

    els.refreshBtn.addEventListener('click', function () { A.debounce('refresh', function () { if (A.runFastRefresh) A.runFastRefresh(true); }); });
    els.websiteRefreshBtn.addEventListener('click', function () { A.debounce('website', function () { if (A.runWebsiteRefresh) A.runWebsiteRefresh(); }, 80); });
    if (els.natRefreshBtn) els.natRefreshBtn.addEventListener('click', function () { A.debounce('nat', function () { if (A.runNATRefresh) A.runNATRefresh(); }); });
    els.openSettingsWindow.addEventListener('click', function () { openWindow('settings'); });
    els.openBroadbandWindow.addEventListener('click', function () { openWindow('broadband'); });
    els.openTransferWindow.addEventListener('click', function () { openWindow('transfer'); });
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
    if (els.closeTraceWindow) els.closeTraceWindow.addEventListener('click', closeTraceWindow);
    if (els.traceBackdrop) els.traceBackdrop.addEventListener('click', closeTraceWindow);

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
    var networkConfigMethod = document.getElementById('network-config-method');
    if (networkConfigMethod) networkConfigMethod.addEventListener('change', function () {
        if (A.updateNetworkConfigMethodState) A.updateNetworkConfigMethodState();
        if (A.updateNetworkConfigApplyState) A.updateNetworkConfigApplyState();
    });
    var networkConfigApplyBtn = document.getElementById('network-config-apply-btn');
    if (networkConfigApplyBtn) networkConfigApplyBtn.addEventListener('click', function () { if (A.applyNetworkConfig) A.applyNetworkConfig(); });
    var networkConfigConfirmBtn = document.getElementById('network-config-confirm-btn');
    if (networkConfigConfirmBtn) networkConfigConfirmBtn.addEventListener('click', function () { if (A.confirmNetworkConfig) A.confirmNetworkConfig(); });
    var networkConfigRollbackBtn = document.getElementById('network-config-rollback-btn');
    if (networkConfigRollbackBtn) networkConfigRollbackBtn.addEventListener('click', function () { if (A.rollbackNetworkConfig) A.rollbackNetworkConfig(); });

    if (A.bindIPv6TitleEasterEgg) A.bindIPv6TitleEasterEgg();

    els.runBroadbandTest.addEventListener('click', function () { if (A.startBroadbandTest) A.startBroadbandTest(); });
    els.runTransferTest.addEventListener('click', function () { if (A.runTransferTest) A.runTransferTest(); });
    if (els.saveSettings) els.saveSettings.addEventListener('click', function () { if (A.saveSettings) A.saveSettings(); });

    if (els.settingNICRealtimeEnabled) {
        els.settingNICRealtimeEnabled.addEventListener('change', function () {
            var isEnabled = !!els.settingNICRealtimeEnabled.checked;
            state.settings.nic_realtime_enabled = isEnabled;
            if (els.settingNICRealtimeIntervalSec) {
                els.settingNICRealtimeIntervalSec.disabled = !isEnabled;
            }
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
        var appTrafficWasInitialized = state.appTrafficInitialized;
        if (!appTrafficWasInitialized && window.__app.initAppTraffic) {
            window.__app.initAppTraffic();
        }
        window.__app.updateWindowControls();
        if (window.__app.initNICRealtime) window.__app.initNICRealtime();
        if (appTrafficWasInitialized && window.__app.refreshAppTraffic) {
            window.__app.refreshAppTraffic();
        }
        if (window.__app.loadSummary) return window.__app.loadSummary(false, true);
    }).then(function () {
        if (!state.summary || !state.summary.ready) {
            if (maxRetries > 0) {
                setTimeout(function () { initWithRetry(maxRetries - 1); }, 2000);
            }
        }
    }).catch(function () {
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
    }, 800);

    document.addEventListener('visibilitychange', function () {
        if (!document.hidden) {
            if (window.__app.loadSummary) window.__app.loadSummary(false, true);
            // Re-check pending after tab focus / network recovery.
            if (window.__app.loadNetworkConfigPending) window.__app.loadNetworkConfigPending(true);
            if (window.__app.loadHostBridgePending) window.__app.loadHostBridgePending(true);
        }
    });
}

setTimeout(boot, 100);
})();
