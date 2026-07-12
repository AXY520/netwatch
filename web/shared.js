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

    /**
     * In-app confirm dialog. Resolves true/false.
     * options: { title, message, okText, cancelText }
     */
    function confirmDialog(options) {
        options = options || {};
        return new Promise(function (resolve) {
            var win = document.getElementById('app-confirm-window');
            var backdrop = document.getElementById('app-confirm-backdrop');
            var titleEl = document.getElementById('app-confirm-title');
            var msgEl = document.getElementById('app-confirm-message');
            var okBtn = document.getElementById('app-confirm-ok');
            var cancelBtn = document.getElementById('app-confirm-cancel');
            if (!win || !okBtn || !cancelBtn) {
                // Fallback only if markup missing
                resolve(window.confirm(options.message || options.title || ''));
                return;
            }
            if (titleEl) titleEl.textContent = options.title || '确认';
            if (msgEl) msgEl.textContent = options.message || '';
            if (options.okText) okBtn.textContent = options.okText;
            if (options.cancelText) cancelBtn.textContent = options.cancelText;

            var prevActive = document.activeElement;
            var settled = false;
            function cleanup(result) {
                if (settled) return;
                settled = true;
                win.classList.remove('active');
                if (backdrop) backdrop.classList.remove('active');
                document.removeEventListener('keydown', onKey);
                okBtn.removeEventListener('click', onOk);
                cancelBtn.removeEventListener('click', onCancel);
                if (backdrop) backdrop.removeEventListener('click', onCancel);
                if (prevActive && typeof prevActive.focus === 'function') {
                    try { prevActive.focus(); } catch (_) {}
                }
                resolve(result);
            }
            function onOk(e) {
                if (e) e.preventDefault();
                cleanup(true);
            }
            function onCancel(e) {
                if (e) e.preventDefault();
                cleanup(false);
            }
            function onKey(e) {
                if (e.key === 'Escape') onCancel(e);
                if (e.key === 'Enter') onOk(e);
            }
            okBtn.addEventListener('click', onOk);
            cancelBtn.addEventListener('click', onCancel);
            if (backdrop) backdrop.addEventListener('click', onCancel);
            document.addEventListener('keydown', onKey);
            if (backdrop) backdrop.classList.add('active');
            win.classList.add('active');
            try { okBtn.focus(); } catch (_) {}
        });
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

    function getLazycatAppCommon() {
        var sdk = window.LazycatSDK || {};
        var nativeAppCommon = window.AppCommon ||
            sdk.AppCommon ||
            (sdk.extentions && sdk.extentions.AppCommon) ||
            (sdk.extensions && sdk.extensions.AppCommon) ||
            null;
        if (nativeAppCommon) return nativeAppCommon;
        return createLazycatAppCommonBridge();
    }

    function isLazycatAndroidShell() {
        return String(navigator.userAgent || '').indexOf('Lazycat_101') !== -1;
    }

    function isLazycatIosShell() {
        return String(navigator.userAgent || '').indexOf('Lazycat_103') !== -1;
    }

    function ensureIosCallbackQueue() {
        if (!window.lzcAppSdk_responseCallBackFuncDict) window.lzcAppSdk_responseCallBackFuncDict = {};
        if (!window.lzcAppSdk_responseCallBackFuncUniqueID) window.lzcAppSdk_responseCallBackFuncUniqueID = 1;
        if (window.lzcAppSdk_sendCallBackFunc) return;
        window.lzcAppSdk_sendCallBackFunc = function (funcUniqueID, responseData) {
            var callback = window.lzcAppSdk_responseCallBackFuncDict && window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID];
            if (!callback) return;
            callback(responseData);
            delete window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID];
        };
    }

    function callIosHandler(name, args) {
        args = args || [];
        var handler = window.webkit &&
            window.webkit.messageHandlers &&
            window.webkit.messageHandlers[name];
        if (!handler || typeof handler.postMessage !== 'function') return Promise.resolve(undefined);
        ensureIosCallbackQueue();
        return new Promise(function (resolve) {
            var settled = false;
            var funcUniqueID = 'lzc_' + (window.lzcAppSdk_responseCallBackFuncUniqueID++) + '_' + Date.now();
            window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID] = function (data) {
                settled = true;
                resolve(data);
            };
            try {
                var ret = handler.postMessage({ funcUniqueID: funcUniqueID, params: args });
                if (ret !== undefined) {
                    settled = true;
                    delete window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID];
                    resolve(ret);
                    return;
                }
            } catch (err) {
                settled = true;
                delete window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID];
                resolve(undefined);
                return;
            }
            setTimeout(function () {
                if (settled) return;
                delete window.lzcAppSdk_responseCallBackFuncDict[funcUniqueID];
                resolve(undefined);
            }, 1200);
        });
    }

    function hasIosHandler(name) {
        return !!(window.webkit &&
            window.webkit.messageHandlers &&
            window.webkit.messageHandlers[name] &&
            typeof window.webkit.messageHandlers[name].postMessage === 'function');
    }

    function createLazycatAppCommonBridge() {
        if (isLazycatAndroidShell() && window.android && typeof window.android.SetFullScreen === 'function') {
            return {
                SetFullScreen: function () {
                    if (typeof window.android.SetFullScreen === 'function') window.android.SetFullScreen();
                    return Promise.resolve();
                },
                CancelFullScreen: function () {
                    if (typeof window.android.CancelFullScreen === 'function') window.android.CancelFullScreen();
                    return Promise.resolve();
                },
                GetFullScreenStatus: function () {
                    if (typeof window.android.GetFullScreenStatus !== 'function') return Promise.resolve(false);
                    return Promise.resolve(window.android.GetFullScreenStatus()).then(Boolean);
                }
            };
        }
        if (isLazycatIosShell() && hasIosHandler('SetFullScreen')) {
            return {
                SetFullScreen: function () { return callIosHandler('SetFullScreen'); },
                CancelFullScreen: function () { return callIosHandler('CancelFullScreen'); },
                GetFullScreenStatus: function () {
                    return callIosHandler('GetFullScreenStatus').then(function (value) {
                        return value === true || value === 'true' || value === 1 || value === '1';
                    });
                }
            };
        }
        return null;
    }

    function requestLazycatFullscreen(force) {
        var isMobileShell = isLazycatAndroidShell() || isLazycatIosShell();
        if (!isMobileShell) return true;
        var appCommon = getLazycatAppCommon();
        if (!appCommon || typeof appCommon.SetFullScreen !== 'function') return false;
        var now = Date.now();
        if (!force && requestLazycatFullscreen._lastAt && now - requestLazycatFullscreen._lastAt < 800) return true;
        requestLazycatFullscreen._lastAt = now;
        Promise.resolve()
            .then(function () { return appCommon.SetFullScreen(); })
            .catch(function (err) {
                console.debug('lazycat fullscreen unavailable', err);
            });
        return true;
    }

    function scheduleLazycatFullscreenRetry(attempt) {
        clearTimeout(scheduleLazycatFullscreenRetry._timer);
        if (attempt >= 12) return;
        scheduleLazycatFullscreenRetry._timer = setTimeout(function () {
            initLazycatFullscreen(attempt + 1);
        }, 250);
    }

    function canRequestLazycatFullscreen() {
        return isLazycatAndroidShell() || isLazycatIosShell();
    }

    function bindLazycatFullscreenWatchers() {
        if (bindLazycatFullscreenWatchers._done) return;
        bindLazycatFullscreenWatchers._done = true;
        var requestNow = function () {
            initLazycatFullscreen(0);
        };
        var requestOnInput = function () {
            if (canRequestLazycatFullscreen()) requestLazycatFullscreen(true);
        };
        window.addEventListener('pageshow', requestNow);
        window.addEventListener('focus', requestNow);
        window.addEventListener('resize', requestNow);
        window.addEventListener('orientationchange', requestNow);
        document.addEventListener('visibilitychange', function () {
            requestNow();
        });
        document.addEventListener('pointerdown', requestOnInput, { passive: true });
        document.addEventListener('touchstart', requestOnInput, { passive: true });
        bindLazycatFullscreenWatchers._timer = setInterval(function () {
            if (canRequestLazycatFullscreen()) requestLazycatFullscreen(false);
        }, 2000);
    }

    function initLazycatFullscreen(attempt) {
        attempt = attempt || 0;
        if (!isLazycatAndroidShell() && !isLazycatIosShell()) return;
        bindLazycatFullscreenWatchers();
        if (!canRequestLazycatFullscreen()) return;
        if (!requestLazycatFullscreen(attempt === 0)) {
            scheduleLazycatFullscreenRetry(attempt);
        }
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
        var theme = localStorage.getItem('theme');
        if (!theme) {
            theme = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
        }
        if (state) state.theme = theme;
        document.documentElement.setAttribute('data-theme', theme);
        var meta = document.querySelector('meta[name="theme-color"]');
        if (meta) meta.content = theme === 'dark' ? '#0a0a0b' : '#f0f2f5';
        if (themeToggleEl) {
            themeToggleEl.addEventListener('click', function () {
                var newTheme = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
                document.documentElement.setAttribute('data-theme', newTheme);
                localStorage.setItem('theme', newTheme);
                if (state) state.theme = newTheme;
                var meta = document.querySelector('meta[name="theme-color"]');
                if (meta) meta.content = newTheme === 'dark' ? '#0a0a0b' : '#f0f2f5';
            });
        }
    }

    return {
        escapeHtml: escapeHtml,
        showToast: showToast,
        lockModalScroll: lockModalScroll,
        unlockModalScroll: unlockModalScroll,
        confirmDialog: confirmDialog,
        formatBytes: formatBytes,
        isNotificationUnsupportedError: isNotificationUnsupportedError,
        markNotificationSeen: markNotificationSeen,
        getLazycatGateway: getLazycatGateway,
        getLazycatAppCommon: getLazycatAppCommon,
        initLazycatFullscreen: initLazycatFullscreen,
        notifySelectedDevices: notifySelectedDevices,
        handleNotificationEvent: handleNotificationEvent,
        isNetwatchBridge: isNetwatchBridge,
        initTheme: initTheme
    };
})();
