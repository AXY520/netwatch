(function () {
    const i18n = window.__;
    
    function lanGet(path) {
        if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
        return fetch(path, { cache: 'no-store' }).then(async (r) => {
            const data = await r.json().catch(() => ({}));
            if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
            return data;
        });
    }
    function lanPost(path, body) {
        if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
        return fetch(path, {
            method: 'POST',
            headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
            body: body === undefined ? undefined : JSON.stringify(body),
            cache: 'no-store'
        }).then(async (r) => {
            const data = await r.json().catch(() => ({}));
            if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
            return data;
        });
    }

const state = {
        theme: localStorage.getItem('theme') || 'dark',
        logoClicks: 0,
        logoClickTimer: null,
        lzcGatewayPromise: null,
        notificationUnsupported: localStorage.getItem('netwatch_notification_unsupported') === 'true',
        notificationLastID: Number(localStorage.getItem('netwatch_notification_last_id') || '0') || 0,
        deviceID: localStorage.getItem('netwatch_device_id') || '',
        modalScrollY: 0,
        settings: null,
        sse: null,
        noteMAC: '',
        noteButton: null
    };

    const els = {
        logo: document.getElementById('lan-logo'),
        themeToggle: document.getElementById('theme-toggle'),
        openSettings: document.getElementById('lan-open-settings'),
        refreshBtn: document.getElementById('lan-refresh-btn'),
        count: document.getElementById('lan-device-count'),
        tbody: document.querySelector('#lan-device-table tbody'),
        testBackdrop: document.getElementById('lan-test-backdrop'),
        settingsWindow: document.getElementById('lan-settings-window'),
        settingsClose: document.getElementById('lan-settings-close'),
        ignoredList: document.getElementById('lan-ignored-list'),
        noteWindow: document.getElementById('lan-note-window'),
        noteClose: document.getElementById('lan-note-close'),
        noteCancel: document.getElementById('lan-note-cancel'),
        noteSave: document.getElementById('lan-note-save'),
        noteInput: document.getElementById('lan-note-input'),
        noteDevice: document.getElementById('lan-note-device'),
        testWindow: document.getElementById('lan-notify-test-window'),
        testClose: document.getElementById('lan-notify-test-close'),
        testSend: document.getElementById('lan-notify-test-send'),
        testTitle: document.getElementById('lan-notify-test-title'),
        testBody: document.getElementById('lan-notify-test-body'),
        settingsBackdrop: document.getElementById('lan-settings-backdrop'),
        noteBackdrop: document.getElementById('lan-note-backdrop'),
        settingsStatus: document.getElementById('lan-settings-status'),
        saveSettings: document.getElementById('lan-save-settings'),
        settingNotifyLANDeviceChange: document.getElementById('lan-setting-notify-lan-device-change'),
        settingLANOfflineAfterSec: document.getElementById('lan-setting-offline-after-sec'),
        settingLANOnlineAfterSec: document.getElementById('lan-setting-online-after-sec'),
        settingLANOfflineDelaySec: document.getElementById('lan-setting-offline-notify-delay-sec'),
        settingLANOnlineDelaySec: document.getElementById('lan-setting-online-notify-delay-sec'),
        settingMaxCheckAttempts: document.getElementById('lan-setting-max-check-attempts'),
        settingNotifyCooldownSec: document.getElementById('lan-setting-notify-cooldown-sec'),
        settingFlappingThreshold: document.getElementById('lan-setting-flapping-threshold'),
        settingFlappingWindowSec: document.getElementById('lan-setting-flapping-window-sec'),
        settingAutoRemoveDays: document.getElementById('lan-setting-auto-remove-days'),
        toast: document.getElementById('toast')
    };

    function initTheme() {
        NetwatchShared.initTheme(state, els.themeToggle);
    }

    function showToast(message, type) {
        NetwatchShared.showToast(message, type);
    }

    function escapeHtml(value) {
        return NetwatchShared.escapeHtml(value);
    }

    function statusBadge(status) {
        if (status === 'online') return `<span class="lan-status-badge online">${i18n('status_online')}</span>`;
        if (status === 'offline') return `<span class="lan-status-badge offline">${i18n('status_offline')}</span>`;
        if (status === 'interface_down') return `<span class="lan-status-badge offline">${i18n('status_iface_down')}</span>`;
        if (status === 'interface_pending') return `<span class="lan-status-badge unknown">${i18n('status_pending')}</span>`;
        if (status === 'online_pending') return `<span class="lan-status-badge unknown">${i18n('status_online_confirming')}</span>`;
        return `<span class="lan-status-badge unknown">${i18n('status_unknown')}</span>`;
    }

    function tag(label, tone = 'neutral') {
        return `<span class="lan-tag ${tone}">${escapeHtml(label)}</span>`;
    }

    function editIcon() {
        return '<span aria-hidden="true">✎</span>';
    }

    function ensureModalLock() {
        if (document.body.classList.contains('modal-scroll-locked')) return;
        lockModalScroll();
    }

    function lockModalScroll() {
        NetwatchShared.lockModalScroll();
    }

    function unlockModalScroll() {
        NetwatchShared.unlockModalScroll();
    }

    function openTestWindow() {
        els.testWindow?.classList.add('active');
        els.testBackdrop?.classList.add('active');
        ensureModalLock();
    }

    function closeTestWindow() {
        els.testWindow?.classList.remove('active');
        els.testBackdrop?.classList.remove('active');
        closeBackdropIfIdle();
    }

    function openSettingsWindow() {
        els.settingsWindow?.classList.add('active');
        els.settingsBackdrop?.classList.add('active');
        ensureModalLock();
    }

    function closeSettingsWindow() {
        els.settingsWindow?.classList.remove('active');
        els.settingsBackdrop?.classList.remove('active');
        closeBackdropIfIdle();
    }

    function closeBackdropIfIdle() {
        if (els.testWindow?.classList.contains('active') || els.settingsWindow?.classList.contains('active') || els.noteWindow?.classList.contains('active')) {
            return;
        }
        unlockModalScroll();
    }

    function closeActiveWindows() {
        els.testWindow?.classList.remove('active');
        els.settingsWindow?.classList.remove('active');
        els.noteWindow?.classList.remove('active');
        els.testBackdrop?.classList.remove('active');
        els.settingsBackdrop?.classList.remove('active');
        els.noteBackdrop?.classList.remove('active');
        closeBackdropIfIdle();
    }

    function openNoteWindow(btn) {
        state.noteButton = btn;
        state.noteMAC = btn.dataset.mac || '';
        if (!state.noteMAC) return;
        if (els.noteInput) {
            els.noteInput.value = btn.dataset.note || '';
        }
        if (els.noteDevice) {
            els.noteDevice.textContent = `MAC：${state.noteMAC}`;
        }
        els.noteWindow?.classList.add('active');
        els.noteBackdrop?.classList.add('active');
        ensureModalLock();
        setTimeout(() => els.noteInput?.focus(), 0);
    }

    function closeNoteWindow() {
        els.noteWindow?.classList.remove('active');
        els.noteBackdrop?.classList.remove('active');
        state.noteMAC = '';
        state.noteButton = null;
        closeBackdropIfIdle();
    }

    async function saveNote() {
        if (!state.noteMAC) return;
        if (els.noteSave) els.noteSave.disabled = true;
        try {
            await updateDeviceMeta({ mac: state.noteMAC, note: els.noteInput?.value || '' });
            closeNoteWindow();
        } finally {
            if (els.noteSave) els.noteSave.disabled = false;
        }
    }

    function handleLogoClick() {
        state.logoClicks += 1;
        clearTimeout(state.logoClickTimer);
        state.logoClickTimer = setTimeout(() => {
            state.logoClicks = 0;
        }, 1800);
        if (state.logoClicks >= 5) {
            state.logoClicks = 0;
            openTestWindow();
        }
    }

    async function getLazycatGateway() {
        return NetwatchShared.getLazycatGateway(state);
    }

    async function sendTestNotification() {
        els.testSend.disabled = true;
        try {
            const gateway = await getLazycatGateway();
            const device = await gateway.currentDevice;
            await device.notification.Notify({
                title: els.testTitle?.value || i18n('test_notify_title'),
                body: els.testBody?.value || i18n('test_notify_body'),
                deeplinkUrl: 'lzc://app/cloud.lazycat.app.netwatch'
            });
            state.notificationUnsupported = false;
            localStorage.removeItem('netwatch_notification_unsupported');
            showToast(i18n('test_notify_sent'), 'success');
        } catch (err) {
            console.debug('notification test failed', err);
            if (isNotificationUnsupportedError(err)) {
                state.notificationUnsupported = true;
                localStorage.setItem('netwatch_notification_unsupported', 'true');
            }
            showToast(`${i18n('test_notify_failed')}: ${formatNotificationError(err)}`, 'error');
        } finally {
            els.testSend.disabled = false;
        }
    }

    function markNotificationSeen(id) {
        NetwatchShared.markNotificationSeen(id, state);
    }

    function startSSE() {
        if (state.sse) return;
        try {
            const es = new EventSource('/api/v1/events');
            es.addEventListener('notification', (ev) => {
                try { handleNotificationEvent(JSON.parse(ev.data)); } catch (_) {}
            });
            es.addEventListener('lan_devices', (ev) => {
                try { render(JSON.parse(ev.data)); } catch (_) {}
            });
            es.onerror = () => { /* browser auto-reconnects */ };
            state.sse = es;
        } catch (_) {}
    }

    function isNotificationUnsupportedError(err) {
        return NetwatchShared.isNotificationUnsupportedError(err);
    }

    function formatNotificationError(err) {
        if (isNotificationUnsupportedError(err)) {
            return '当前客户端未注册系统通知服务，请更新懒猫客户端或等待系统通知能力上线';
        }
        return err?.message || String(err || '未知错误');
    }

    function normalizeSettings(settingsData) {
        const data = settingsData || {};
        return {
            ...data,
            refresh_interval_sec: data.refresh_interval_sec || 10,
            broadband_domestic_only: !!data.broadband_domestic_only,
            nic_realtime_enabled: data.nic_realtime_enabled !== false,
            nic_realtime_interval_sec: data.nic_realtime_interval_sec || 1,
            chart_time_label_interval: data.chart_time_label_interval || 0,
            traffic_sampling_enabled: data.traffic_sampling_enabled !== false,
            traffic_sampling_interval_sec: data.traffic_sampling_interval_sec || 60,
            per_app_sampling_interval: data.per_app_sampling_interval || {},
            persistent_traffic_bridges: data.persistent_traffic_bridges || [],
            domestic_sites: data.domestic_sites || [],
            global_sites: data.global_sites || [],
            alert_webhook_url: data.alert_webhook_url || '',
            background_monitor_enabled: !!data.background_monitor_enabled,
            background_monitor_interval_sec: data.background_monitor_interval_sec || 60,
            notifications_enabled: !!data.notifications_enabled,
            client_notification_enabled: data.client_notification_enabled !== false,
            notify_abnormal_traffic: data.notify_abnormal_traffic !== false,
            notify_egress_change: data.notify_egress_change !== false,
            notify_connectivity_change: data.notify_connectivity_change !== false,
            notify_lan_device_change: data.notify_lan_device_change !== false,
            lan_device_offline_after_sec: data.lan_device_offline_after_sec ?? 180,
            lan_device_online_after_sec: data.lan_device_online_after_sec ?? 0,
            lan_device_offline_notify_delay_sec: data.lan_device_offline_notify_delay_sec ?? 120,
            lan_device_online_notify_delay_sec: data.lan_device_online_notify_delay_sec ?? 120,
            abnormal_traffic_threshold_mbps: data.abnormal_traffic_threshold_mbps || 100,
            bark_enabled: !!data.bark_enabled,
            bark_server_url: data.bark_server_url || 'https://api.day.app',
            bark_device_key: data.bark_device_key || '',
            bark_group: data.bark_group || 'Netwatch',
            dnd_enabled: !!data.dnd_enabled,
            dnd_start: data.dnd_start || '22:00',
            dnd_end: data.dnd_end || '08:00',
            scheduled_notify_enabled: !!data.scheduled_notify_enabled,
            scheduled_notify_time: data.scheduled_notify_time || '09:00',
            lan_max_check_attempts: data.lan_max_check_attempts ?? 3,
            lan_notify_cooldown_sec: data.lan_notify_cooldown_sec ?? 600,
            lan_flapping_threshold: data.lan_flapping_threshold ?? 5,
            lan_flapping_window_sec: data.lan_flapping_window_sec ?? 600,
            lan_device_auto_remove_days: data.lan_device_auto_remove_days ?? 30
        };
    }

    function applySettingsToForm() {
        const settings = normalizeSettings(state.settings || {});
        state.settings = settings;
        const notificationsDisabled = !settings.background_monitor_enabled || !settings.notifications_enabled;
        const lanNotifyDisabled = notificationsDisabled || settings.notify_lan_device_change === false;
        if (els.settingNotifyLANDeviceChange) {
            els.settingNotifyLANDeviceChange.checked = settings.notify_lan_device_change !== false;
            els.settingNotifyLANDeviceChange.disabled = notificationsDisabled;
        }
        if (els.settingLANOfflineAfterSec) {
            els.settingLANOfflineAfterSec.value = String(Math.max(10, settings.lan_device_offline_after_sec ?? 180));
            els.settingLANOfflineAfterSec.disabled = !settings.background_monitor_enabled;
            window.syncCustomSelect?.(els.settingLANOfflineAfterSec);
        }
        if (els.settingLANOnlineAfterSec) {
            els.settingLANOnlineAfterSec.value = String(Math.max(0, settings.lan_device_online_after_sec ?? 0));
            els.settingLANOnlineAfterSec.disabled = !settings.background_monitor_enabled;
            window.syncCustomSelect?.(els.settingLANOnlineAfterSec);
        }
        if (els.settingLANOfflineDelaySec) {
            els.settingLANOfflineDelaySec.value = String(Math.max(0, settings.lan_device_offline_notify_delay_sec ?? 120));
            els.settingLANOfflineDelaySec.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingLANOfflineDelaySec);
        }
        if (els.settingLANOnlineDelaySec) {
            els.settingLANOnlineDelaySec.value = String(Math.max(0, settings.lan_device_online_notify_delay_sec ?? 120));
            els.settingLANOnlineDelaySec.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingLANOnlineDelaySec);
        }
        if (els.settingMaxCheckAttempts) {
            els.settingMaxCheckAttempts.value = String(Math.max(1, settings.lan_max_check_attempts ?? 3));
            els.settingMaxCheckAttempts.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingMaxCheckAttempts);
        }
        if (els.settingNotifyCooldownSec) {
            els.settingNotifyCooldownSec.value = String(Math.max(0, settings.lan_notify_cooldown_sec ?? 600));
            els.settingNotifyCooldownSec.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingNotifyCooldownSec);
        }
        if (els.settingFlappingThreshold) {
            els.settingFlappingThreshold.value = String(Math.max(1, settings.lan_flapping_threshold ?? 5));
            els.settingFlappingThreshold.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingFlappingThreshold);
        }
        if (els.settingFlappingWindowSec) {
            els.settingFlappingWindowSec.value = String(Math.max(60, settings.lan_flapping_window_sec ?? 600));
            els.settingFlappingWindowSec.disabled = lanNotifyDisabled;
            window.syncCustomSelect?.(els.settingFlappingWindowSec);
        }
        if (els.settingAutoRemoveDays) {
            els.settingAutoRemoveDays.value = String(Math.max(0, settings.lan_device_auto_remove_days ?? 30));
            els.settingAutoRemoveDays.disabled = !settings.background_monitor_enabled;
            window.syncCustomSelect?.(els.settingAutoRemoveDays);
        }
        if (els.settingsStatus) {
            if (!settings.background_monitor_enabled) {
                els.settingsStatus.textContent = '后台检测已关闭';
            } else if (!settings.notifications_enabled) {
                els.settingsStatus.textContent = '全局通知已关闭';
            } else {
                els.settingsStatus.textContent = settings.notify_lan_device_change !== false ? 'LAN 通知已启用' : 'LAN 通知已关闭';
            }
        }
    }

    function collectSettingsPayload() {
        const current = normalizeSettings(state.settings || {});
        return {
            ...current,
            notify_lan_device_change: !!els.settingNotifyLANDeviceChange?.checked,
            lan_device_offline_after_sec: Math.max(10, Number.parseInt(els.settingLANOfflineAfterSec?.value || '180', 10) || 180),
            lan_device_online_after_sec: Math.max(0, Number.parseInt(els.settingLANOnlineAfterSec?.value || '0', 10) || 0),
            lan_device_offline_notify_delay_sec: Math.max(0, Number.parseInt(els.settingLANOfflineDelaySec?.value || '120', 10) || 0),
            lan_device_online_notify_delay_sec: Math.max(0, Number.parseInt(els.settingLANOnlineDelaySec?.value || '120', 10) || 0),
            lan_max_check_attempts: Math.max(1, Number.parseInt(els.settingMaxCheckAttempts?.value || '3', 10) || 3),
            lan_notify_cooldown_sec: Math.max(0, Number.parseInt(els.settingNotifyCooldownSec?.value || '600', 10) || 600),
            lan_flapping_threshold: Math.max(1, Number.parseInt(els.settingFlappingThreshold?.value || '5', 10) || 5),
            lan_flapping_window_sec: Math.max(60, Number.parseInt(els.settingFlappingWindowSec?.value || '600', 10) || 600),
            lan_device_auto_remove_days: Math.max(0, Number.parseInt(els.settingAutoRemoveDays?.value || '30', 10) || 0)
        };
    }

    async function saveLANSettings({ silent = false } = {}) {
        if (!state.settings) {
            state.settings = normalizeSettings(await lanGet('/api/v1/settings'));
        }
        const payload = collectSettingsPayload();
        if (els.saveSettings) els.saveSettings.disabled = true;
        try {
            state.settings = normalizeSettings(await lanPost('/api/v1/settings', payload));
            applySettingsToForm();
            startSSE();
            if (!silent) showToast(i18n('settings_saved'), 'success');
            return state.settings;
        } catch (err) {
            if (!silent) showToast(`${i18n('settings_save_failed')}: ${err.message}`, 'error');
            throw err;
        } finally {
            if (els.saveSettings) els.saveSettings.disabled = false;
        }
    }

    function render(data) {
        state.lastLANData = data;
        const devices = (data.devices || []).filter(dev => dev.known !== false);
        if (els.count) {
            els.count.textContent = `在线 ${data.online || 0} 台`;
        }
        renderIgnoredDevices(data.ignored_devices || []);

        els.tbody.innerHTML = devices.map(dev => {
            const name = deviceLabel(dev);
            const tags = [];
            if (dev.vendor_hint) tags.push(tag(dev.vendor_hint, 'vendor'));
            if (dev.detection_methods?.length) {
                dev.detection_methods.forEach(m => tags.push(tag(formatDetectionMethod(m), 'method')));
            }
            const reachability = formatReachability(dev.reachability);
            const netRows = [];
            netRows.push(`<div class="lan-net-row"><span class="lan-net-label">IPv4</span><span class="lan-net-value mono">${escapeHtml(dev.ip || '--')}</span></div>`);
            if (dev.ipv6?.length) {
                const shown = dev.ipv6.slice(0, 2);
                shown.forEach(v6 => netRows.push(`<div class="lan-net-row"><span class="lan-net-label">IPv6</span><span class="lan-net-value mono">${escapeHtml(v6)}</span></div>`));
                if (dev.ipv6.length > 2) netRows.push(`<div class="lan-net-row"><span class="lan-net-label"></span><span class="lan-net-value number">+${dev.ipv6.length - 2} more</span></div>`);
            }
            netRows.push(`<div class="lan-net-row"><span class="lan-net-label">MAC</span><span class="lan-net-value mono">${escapeHtml(dev.mac || '--')}</span></div>`);
            if (dev.interface) netRows.push(`<div class="lan-net-row"><span class="lan-net-label">IF</span><span class="lan-net-value">${escapeHtml(dev.interface)}</span></div>`);
            return `
                <tr class="lan-device-row" data-mac="${escapeHtml(dev.mac || '')}">
                    <td>
                        <div class="lan-name-line">
                            <div class="lan-device-name">${escapeHtml(name)}</div>
                            <button class="lan-name-edit" data-action="note" data-mac="${escapeHtml(dev.mac || '')}" data-note="${escapeHtml(dev.note || '')}" title="编辑设备备注">${editIcon()}</button>
                        </div>
                        <div class="lan-tag-row">${tags.join('')}</div>
                    </td>
                    <td title="邻居状态：${escapeHtml(reachability)}">${statusBadge(dev.status)}</td>
                    <td>
                        <div class="lan-network-lines">
                            ${netRows.join('')}
                        </div>
                    </td>
                    <td><span class="number">${escapeHtml(dev.last_seen || '--')}</span></td>
                    <td>
                        <div class="lan-action-group">
                            <button class="lan-action danger" data-action="ignore" data-mac="${escapeHtml(dev.mac || '')}" title="从设备列表隐藏">忽略</button>
                        </div>
                    </td>
                </tr>
            `;
        }).join('') || '<tr><td colspan="5" class="placeholder">暂无设备数据，点击扫描开始发现</td></tr>';
    }

    function renderIgnoredDevices(devices) {
        if (!els.ignoredList) return;
        const ignored = (devices || []).filter(dev => dev.mac);
        if (ignored.length === 0) {
            els.ignoredList.innerHTML = '<span class="placeholder">无</span>';
            return;
        }
        els.ignoredList.innerHTML = ignored.map(dev => {
            const name = deviceLabel(dev);
            return `<span class="lan-ignored-tag">${escapeHtml(name)}<button class="lan-ignored-remove" data-action="unignore" data-mac="${escapeHtml(dev.mac || '')}" title="恢复显示">&times;</button></span>`;
        }).join('');
    }

    function deviceLabel(dev) {
        return dev.note || dev.hostname || dev.ip || dev.mac || '--';
    }

    function formatDetectionMethod(method) {
        const map = {
            arp: 'ARP',
            icmp: 'ICMP Ping',
            dhcp: 'DHCP 租约',
            ndp: 'IPv6 NDP',
            udp: 'UDP 探测',
            mdns: 'mDNS'
        };
        return map[method] || method;
    }

    function formatReachability(value) {
        const map = {
            reachable: '已确认',
            delay: '待复核',
            probe: '探测中',
            stale: '缓存',
            failed: '失败',
            incomplete: '未完成',
            noarp: '静态',
            permanent: '永久',
            'arp-cache': 'ARP 缓存'
        };
        return map[value] || value || '--';
    }

    async function load(scan = false) {
        const originalTitle = els.refreshBtn?.getAttribute('title') || i18n('scan_btn');
        const originalAria = els.refreshBtn?.getAttribute('aria-label') || i18n('scan_btn');
        if (els.refreshBtn) {
            els.refreshBtn.disabled = true;
            const busyLabel = scan ? i18n('scanning') : i18n('loading');
            els.refreshBtn.setAttribute('title', busyLabel);
            els.refreshBtn.setAttribute('aria-label', busyLabel);
        }
        try {
            const devices = scan
                ? await lanPost('/api/v1/lan/devices')
                : await lanGet('/api/v1/lan/devices');
            render(devices);
        } catch (err) {
            showToast(`${i18n('lan_load_failed')}: ${err.message}`, 'error');
        } finally {
            if (els.refreshBtn) {
                els.refreshBtn.disabled = false;
                els.refreshBtn.setAttribute('title', originalTitle);
                els.refreshBtn.setAttribute('aria-label', originalAria);
            }
        }
    }

    async function loadSettingsAndScheduleRefresh() {
        try {
            state.settings = normalizeSettings(await lanGet('/api/v1/settings'));
            applySettingsToForm();
            startSSE();
            loadLazycatDevices();
        } catch (err) {
            console.debug('lan settings load failed', err);
        }
    }

    async function updateDeviceMeta(payload) {
        try {
            render(await lanPost('/api/v1/lan/devices/meta', payload));
            showToast(i18n('device_mark_updated'), 'success');
        } catch (err) {
            showToast(`${i18n('device_mark_failed')}: ${err.message}`, 'error');
        }
    }

    function handleTableClick(ev) {
        const btn = ev.target.closest('.lan-action, .lan-name-edit, .lan-ignored-remove');
        if (!btn) return;
        const mac = btn.dataset.mac;
        if (!mac) return;
        if (btn.dataset.action === 'note') {
            openNoteWindow(btn);
            return;
        }
        if (btn.dataset.action === 'ignore') {
            updateDeviceMeta({ mac, ignored: true });
            return;
        }
        if (btn.dataset.action === 'unignore') {
            updateDeviceMeta({ mac, ignored: false });
        }
    }

    initTheme();
    els.logo?.addEventListener('click', handleLogoClick);
    els.openSettings?.addEventListener('click', openSettingsWindow);
    els.settingsClose?.addEventListener('click', closeSettingsWindow);
    els.noteClose?.addEventListener('click', closeNoteWindow);
    els.noteCancel?.addEventListener('click', closeNoteWindow);
    els.noteSave?.addEventListener('click', saveNote);
    els.noteInput?.addEventListener('keydown', ev => {
        if (ev.key === 'Enter') {
            ev.preventDefault();
            saveNote();
        }
        if (ev.key === 'Escape') {
            ev.preventDefault();
            closeNoteWindow();
        }
    });
    els.testClose?.addEventListener('click', closeTestWindow);
    els.testBackdrop?.addEventListener('click', closeActiveWindows);
    els.testSend?.addEventListener('click', sendTestNotification);
    els.saveSettings?.addEventListener('click', () => saveLANSettings());
    els.ignoredList?.addEventListener('click', handleTableClick);
    els.settingNotifyLANDeviceChange?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), notify_lan_device_change: !!els.settingNotifyLANDeviceChange.checked };
        applySettingsToForm();
        startSSE();
    });
    els.settingLANOfflineAfterSec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_device_offline_after_sec: Math.max(10, Number.parseInt(els.settingLANOfflineAfterSec.value || '180', 10) || 180) };
        applySettingsToForm();
    });
    els.settingLANOnlineAfterSec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_device_online_after_sec: Math.max(0, Number.parseInt(els.settingLANOnlineAfterSec.value || '0', 10) || 0) };
        applySettingsToForm();
    });
    els.settingLANOfflineDelaySec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_device_offline_notify_delay_sec: Math.max(0, Number.parseInt(els.settingLANOfflineDelaySec.value || '120', 10) || 0) };
        applySettingsToForm();
    });
    els.settingLANOnlineDelaySec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_device_online_notify_delay_sec: Math.max(0, Number.parseInt(els.settingLANOnlineDelaySec.value || '120', 10) || 0) };
        applySettingsToForm();
    });
    els.settingMaxCheckAttempts?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_max_check_attempts: Math.max(1, Number.parseInt(els.settingMaxCheckAttempts.value || '3', 10) || 3) };
        applySettingsToForm();
    });
    els.settingNotifyCooldownSec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_notify_cooldown_sec: Math.max(0, Number.parseInt(els.settingNotifyCooldownSec.value || '600', 10) || 600) };
        applySettingsToForm();
    });
    els.settingFlappingThreshold?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_flapping_threshold: Math.max(1, Number.parseInt(els.settingFlappingThreshold.value || '5', 10) || 5) };
        applySettingsToForm();
    });
    els.settingFlappingWindowSec?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_flapping_window_sec: Math.max(60, Number.parseInt(els.settingFlappingWindowSec.value || '600', 10) || 600) };
        applySettingsToForm();
    });
    els.settingAutoRemoveDays?.addEventListener('change', () => {
        state.settings = { ...normalizeSettings(state.settings || {}), lan_device_auto_remove_days: Math.max(0, Number.parseInt(els.settingAutoRemoveDays.value || '30', 10) || 0) };
        applySettingsToForm();
    });
    els.refreshBtn?.addEventListener('click', () => load(true));
    els.tbody?.addEventListener('click', handleTableClick);
    document.addEventListener('visibilitychange', () => {
        if (!document.hidden) {
            load(false);
            loadSettingsAndScheduleRefresh();
        }
    });
    async function loadLazycatDevices() {
        try {
            const gateway = await getLazycatGateway();
            const session = await gateway.session;
            const result = await gateway.devices.ListEndDevices({ uid: session.uid });
            state.lazycatDevices = (result.devices || []).map(d => ({
                id: d.uniqueDeivceId || '',
                name: d.remarkName || d.name || d.model || '',
                model: d.model || '',
                isOnline: !!d.isOnline,
                isMobile: !!d.isMobile,
                isCurrent: d.uniqueDeivceId === session.deviceId
            }));
        } catch (err) {
            console.debug('load lazycat devices failed', err);
            state.lazycatDevices = [];
        }
    }

    async function notifySelectedDevices(event) {
        if (state.notificationUnsupported) return;
        const selectedIDs = state.settings?.notification_device_ids;
        const devices = state.lazycatDevices || [];
        const gateway = await getLazycatGateway();
        const payload = {
            title: event.title || 'Netwatch',
            body: event.body || '',
            deeplinkUrl: event.deeplink_url || 'lzc://app/cloud.lazycat.app.netwatch'
        };

        if (!selectedIDs || selectedIDs.length === 0) {
            const device = await gateway.currentDevice;
            await device.notification.Notify(payload);
            return;
        }

        for (const dev of devices) {
            if (!selectedIDs.includes(dev.id)) continue;
            try {
                const proxy = await gateway.getDeviceProxy(dev.id);
                await proxy.notification.Notify(payload);
            } catch (err) {
                console.debug(`notify device ${dev.id} failed`, err);
            }
        }
    }

    function handleNotificationEvent(event) {
        NetwatchShared.handleNotificationEvent(event, state);
    }

    NetwatchShared.initLazycatFullscreen?.();
    loadSettingsAndScheduleRefresh();
    // First load: try cached data, if empty then scan
    load(false).then(function () {
        var data = state.lastLANData;
        if (!data || !data.devices || data.devices.length === 0) {
            return load(true);
        }
    });
})();
