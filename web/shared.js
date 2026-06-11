window.NetwatchShared = (function () {
    function escapeHtml(value) {
        return String(value ?? '')
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    }

    function showToast(message, type, ms) {
        type = type || 'info';
        ms = ms || 3000;
        const toast = document.getElementById('toast');
        if (!toast) return;
        toast.textContent = message;
        toast.className = 'toast show ' + type;
        if (showToast._timer) clearTimeout(showToast._timer);
        showToast._timer = setTimeout(function () {
            toast.classList.remove('show');
        }, ms);
    }

    var _modalScrollY = 0;
    function lockModalScroll() {
        _modalScrollY = window.scrollY || document.documentElement.scrollTop || 0;
        var hasStableGutter = window.CSS && window.CSS.supports && window.CSS.supports('scrollbar-gutter: stable');
        var scrollbarWidth = hasStableGutter ? 0 : Math.max(0, window.innerWidth - document.documentElement.clientWidth);
        document.documentElement.style.setProperty('--scrollbar-compensation', scrollbarWidth + 'px');
        document.body.style.top = '-' + _modalScrollY + 'px';
        document.body.classList.add('modal-scroll-locked');
    }

    function unlockModalScroll() {
        var y = _modalScrollY || 0;
        document.body.classList.remove('modal-scroll-locked');
        document.body.style.top = '';
        document.documentElement.style.removeProperty('--scrollbar-compensation');
        window.scrollTo(0, y);
    }

    function formatBytes(n) {
        var value = Number(n) || 0;
        if (value <= 0) return '0 B';
        var units = ['B', 'KB', 'MB', 'GB', 'TB'];
        var i = 0;
        var v = value;
        while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
        return (i === 0 ? String(Math.round(v)) : v.toFixed(v < 10 ? 2 : 1)) + ' ' + units[i];
    }

    function isNotificationUnsupportedError(err) {
        var message = String(err && err.message || err || '').toLowerCase();
        return message.indexOf('notificationservice') !== -1 && (
            message.indexOf('not registered') !== -1 ||
            message.indexOf('unimplemented') !== -1 ||
            message.indexOf('unknown service') !== -1
        );
    }

    function markNotificationSeen(id, state) {
        if (!Number.isFinite(id) || id <= (state.notificationLastID || 0)) return;
        state.notificationLastID = id;
        localStorage.setItem('netwatch_notification_last_id', String(id));
    }

    function getLazycatGateway(state) {
        if (state && state.lzcGatewayPromise) return state.lzcGatewayPromise;
        var promise = (async function () {
            var Gateway = window.lzcAPIGateway || (window.LazycatSDK && window.LazycatSDK.lzcAPIGateway);
            if (!Gateway) {
                throw new Error('Lazycat SDK not loaded');
            }
            return new Gateway(window.location.origin, false);
        })();
        if (state) {
            state.lzcGatewayPromise = promise;
            promise.catch(function () { state.lzcGatewayPromise = null; });
        }
        return promise;
    }

    async function notifySelectedDevices(event, state) {
        if (state && state.notificationUnsupported) return;
        var selectedIDs = state && state.settings && state.settings.notification_device_ids;
        var devices = (state && state.lazycatDevices) || [];
        var gateway = await getLazycatGateway(state);
        var payload = {
            title: event.title || 'Netwatch',
            body: event.body || '',
            deeplinkUrl: event.deeplink_url || 'lzc://app/cloud.lazycat.app.netwatch'
        };
        if (!selectedIDs || selectedIDs.length === 0) {
            var device = await gateway.currentDevice;
            await device.notification.Notify(payload);
            return;
        }
        for (var i = 0; i < devices.length; i++) {
            var dev = devices[i];
            if (selectedIDs.indexOf(dev.id) === -1) continue;
            try {
                var proxy = await gateway.getDeviceProxy(dev.id);
                await proxy.notification.Notify(payload);
            } catch (err) {
                console.debug('notify device ' + dev.id + ' failed', err);
            }
        }
    }

    function handleNotificationEvent(event, state) {
        var id = Number(event && event.id || 0);
        if (!id || id <= (state.notificationLastID || 0)) return;
        if (!state.settings || !state.settings.notifications_enabled || state.settings.client_notification_enabled === false) {
            markNotificationSeen(id, state);
            return;
        }
        markNotificationSeen(id, state);
        notifySelectedDevices(event, state).catch(function (err) {
            console.debug('lazycat notification unavailable', err);
            if (isNotificationUnsupportedError(err)) {
                state.notificationUnsupported = true;
                localStorage.setItem('netwatch_notification_unsupported', 'true');
            }
        });
    }

    function isNetwatchBridge(b) {
        var id = (b.app_id || '').toLowerCase();
        var proj = (b.project || '').toLowerCase();
        return id === 'cloud.lazycat.app.netwatch' || id === 'netwatch' || proj.indexOf('netwatch') !== -1;
    }

    function initTheme(state, themeToggleEl) {
        var theme = localStorage.getItem('theme') || 'dark';
        if (state) state.theme = theme;
        document.documentElement.setAttribute('data-theme', theme);
        if (themeToggleEl) {
            themeToggleEl.addEventListener('click', function () {
                var newTheme = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
                document.documentElement.setAttribute('data-theme', newTheme);
                localStorage.setItem('theme', newTheme);
                if (state) state.theme = newTheme;
            });
        }
    }

    return {
        escapeHtml: escapeHtml,
        showToast: showToast,
        lockModalScroll: lockModalScroll,
        unlockModalScroll: unlockModalScroll,
        formatBytes: formatBytes,
        isNotificationUnsupportedError: isNotificationUnsupportedError,
        markNotificationSeen: markNotificationSeen,
        getLazycatGateway: getLazycatGateway,
        notifySelectedDevices: notifySelectedDevices,
        handleNotificationEvent: handleNotificationEvent,
        isNetwatchBridge: isNetwatchBridge,
        initTheme: initTheme
    };
})();
