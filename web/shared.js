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

    function setSelectOptions(select, options, selectedValue, disabled) {
        if (!select) return '';
        options = Array.isArray(options) ? options : [];
        var fragment = document.createDocumentFragment();
        options.forEach(function (item) {
            item = item || {};
            var option = document.createElement('option');
            option.value = String(item.value == null ? '' : item.value);
            option.textContent = String(item.label == null ? option.value : item.label);
            option.disabled = !!item.disabled;
            Object.keys(item.dataset || {}).forEach(function (key) {
                option.dataset[key] = String(item.dataset[key]);
            });
            fragment.appendChild(option);
        });
        select.replaceChildren(fragment);
        var requested = selectedValue == null ? '' : String(selectedValue);
        var hasRequested = options.some(function (item) { return String(item && item.value == null ? '' : item.value) === requested; });
        if (hasRequested) select.value = requested;
        else if (options.length) select.value = String(options[0].value == null ? '' : options[0].value);
        select.disabled = disabled == null ? options.length === 0 : !!disabled;
        if (window.syncCustomSelect) window.syncCustomSelect(select);
        return select.value;
    }

    function parseObservationTime(value) {
        if (!value) return null;
        var normalized = String(value).trim().replace(' ', 'T');
        var parsed = new Date(normalized);
        return Number.isNaN(parsed.getTime()) ? null : parsed;
    }

    function observationTimestampLabel(value) {
        if (!value) return '';
        var raw = String(value).trim();
        var exact = raw.match(/^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})/);
        if (exact) return exact[1] + ' ' + exact[2];
        var parsed = parseObservationTime(raw);
        if (!parsed) return raw;
        var pad = function (number) { return String(number).padStart(2, '0'); };
        return parsed.getFullYear() + '-' + pad(parsed.getMonth() + 1) + '-' + pad(parsed.getDate()) + ' ' +
            pad(parsed.getHours()) + ':' + pad(parsed.getMinutes()) + ':' + pad(parsed.getSeconds());
    }

    function setObservationStatus(el, options) {
        if (!el) return;
        options = options || {};
        var tr = typeof window.__ === 'function' ? window.__ : function (key) { return key; };
        var state = options.state || 'fresh';
        var parts = [];
        if (options.count !== undefined && options.count !== null) {
            parts.push(String(options.count) + (options.countLabel || ' 项'));
        }
        var timestamp = observationTimestampLabel(options.generatedAt);
        var fullTimestamp = timestamp;
        var compactViewport = !!(window.matchMedia && window.matchMedia('(max-width: 640px)').matches);
        if (compactViewport && timestamp) {
            var compactTime = timestamp.match(/(?:^|\s)(\d{2}:\d{2})(?::\d{2})?$/);
            if (compactTime) timestamp = compactTime[1];
        }
        var parsedAt = parseObservationTime(options.generatedAt);
        var inferredAge = parsedAt ? Math.max(0, Math.floor((Date.now() - parsedAt.getTime()) / 1000)) : 0;
        if (state === 'fresh' && (options.stale || (options.staleAfterSeconds && inferredAge > options.staleAfterSeconds))) state = 'stale';
        if (state === 'loading') parts.push(options.loadingText || tr('first_loading'));
        else if (state === 'refreshing') parts.push(tr('refreshing'));
        else if (state === 'error') parts.push(tr('refresh_failed'));
        else if (state === 'unsupported') parts.push(tr('capability_unsupported'));
        else if (state === 'empty') parts.push(tr('no_data'));
        if (timestamp) {
            if (compactViewport && state !== 'error' && state !== 'refreshing') parts.push(timestamp);
            else parts.push((state === 'error' || state === 'refreshing') ? tr('last_success_prefix') + timestamp : tr('sampled_at') + ' ' + timestamp);
        }
        el.textContent = parts.join(' · ');
        el.dataset.state = state;
        if (options.title || options.error) el.title = options.title || options.error;
        else if (compactViewport && fullTimestamp) el.title = tr('sampled_at') + ' ' + fullTimestamp;
        else el.removeAttribute('title');
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
     * Never uses window.confirm — always an in-page modal.
     * options: { title, message, okText, cancelText, danger }
     */
    function ensureConfirmMarkup() {
        var win = document.getElementById('app-confirm-window');
        var backdrop = document.getElementById('app-confirm-backdrop');
        if (win && backdrop) return { win: win, backdrop: backdrop };
        if (!backdrop) {
            backdrop = document.createElement('div');
            backdrop.id = 'app-confirm-backdrop';
            backdrop.className = 'window-backdrop app-confirm-backdrop';
            document.body.appendChild(backdrop);
        }
        if (!win) {
            win = document.createElement('section');
            win.id = 'app-confirm-window';
            win.className = 'floating-window app-confirm-window';
            win.setAttribute('role', 'dialog');
            win.setAttribute('aria-modal', 'true');
            win.setAttribute('aria-labelledby', 'app-confirm-title');
            win.innerHTML =
                '<div class="window-head"><div>' +
                '<div class="window-title" id="app-confirm-title">确认</div>' +
                '<div class="note" id="app-confirm-message"></div>' +
                '</div></div>' +
                '<div class="window-actions app-confirm-actions">' +
                '<button type="button" id="app-confirm-cancel" class="btn-with-icon"><span>取消</span></button>' +
                '<button type="button" id="app-confirm-ok" class="app-confirm-ok btn-with-icon">' +
                '<span class="ui-icon ui-icon--check" aria-hidden="true"></span><span>确定</span></button>' +
                '</div>';
            document.body.appendChild(win);
        }
        return { win: win, backdrop: backdrop };
    }

    function setButtonLabel(btn, text) {
        if (!btn || text == null || text === '') return;
        var label = btn.querySelector('span:not(.ui-icon)');
        if (label) label.textContent = text;
        else btn.textContent = text;
    }

    function confirmDialog(options) {
        options = options || {};
        return new Promise(function (resolve) {
            var nodes = ensureConfirmMarkup();
            var win = nodes.win;
            var backdrop = nodes.backdrop;
            var titleEl = document.getElementById('app-confirm-title');
            var msgEl = document.getElementById('app-confirm-message');
            var okBtn = document.getElementById('app-confirm-ok');
            var cancelBtn = document.getElementById('app-confirm-cancel');
            if (!win || !okBtn || !cancelBtn) {
                // Absolute last resort: never call window.confirm.
                resolve(false);
                return;
            }
            if (titleEl) titleEl.textContent = options.title || '确认';
            if (msgEl) msgEl.textContent = options.message || '';
            setButtonLabel(okBtn, options.okText);
            setButtonLabel(cancelBtn, options.cancelText);
            win.classList.toggle('is-danger', !!options.danger);

            var prevActive = document.activeElement;
            var settled = false;
            function cleanup(result) {
                if (settled) return;
                settled = true;
                win.classList.remove('active');
                win.classList.remove('is-danger');
                if (backdrop) backdrop.classList.remove('active');
                document.removeEventListener('keydown', onKey, true);
                okBtn.removeEventListener('click', onOk);
                cancelBtn.removeEventListener('click', onCancel);
                if (backdrop) backdrop.removeEventListener('click', onCancel);
                if (prevActive && typeof prevActive.focus === 'function') {
                    try { prevActive.focus(); } catch (_) {}
                }
                resolve(!!result);
            }
            function onOk(e) {
                if (e) { e.preventDefault(); e.stopPropagation(); }
                cleanup(true);
            }
            function onCancel(e) {
                if (e) { e.preventDefault(); e.stopPropagation(); }
                cleanup(false);
            }
            function onKey(e) {
                if (e.key === 'Escape') {
                    e.preventDefault();
                    e.stopPropagation();
                    onCancel(e);
                } else if (e.key === 'Enter') {
                    // Only accept Enter when focus is inside the confirm dialog
                    // (avoid hijacking Enter in parent forms).
                    if (win.contains(document.activeElement) || document.activeElement === document.body) {
                        e.preventDefault();
                        e.stopPropagation();
                        onOk(e);
                    }
                }
            }
            okBtn.addEventListener('click', onOk);
            cancelBtn.addEventListener('click', onCancel);
            if (backdrop) backdrop.addEventListener('click', onCancel);
            document.addEventListener('keydown', onKey, true);
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
                await loadLazycatSDK();
                Gateway = window.lzcAPIGateway || (window.LazycatSDK && window.LazycatSDK.lzcAPIGateway);
            }
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

    function loadLazycatSDK() {
        if (window.LazycatSDK || window.lzcAPIGateway) return Promise.resolve();
        if (window.__netwatchLazycatSDKPromise) return window.__netwatchLazycatSDKPromise;
        window.__netwatchLazycatSDKPromise = new Promise(function (resolve, reject) {
            var script = document.createElement('script');
            script.src = '/vendor/lazycat-sdk.js';
            script.async = true;
            script.onload = function () { resolve(); };
            script.onerror = function () {
                window.__netwatchLazycatSDKPromise = null;
                script.remove();
                reject(new Error('Lazycat SDK load failed'));
            };
            document.head.appendChild(script);
        });
        return window.__netwatchLazycatSDKPromise;
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
        // pageshow/focus often arrive together. Never ask the native shell to
        // relayout repeatedly, even when the caller considers the request
        // forced; mobile browser chrome resize events otherwise form a loop.
        if (requestLazycatFullscreen._lastAt && now - requestLazycatFullscreen._lastAt < 1500) return true;
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
        window.addEventListener('pageshow', requestNow);
        window.addEventListener('focus', requestNow);
        window.addEventListener('orientationchange', function () {
            setTimeout(requestNow, 250);
        });
        document.addEventListener('visibilitychange', function () {
            if (!document.hidden) requestNow();
        });
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
        if (!b || typeof b !== 'object') return false;
        // Only hide netwatch's own lzc app bridge — never host nw-* bridges (not in app-traffic).
        var id = String(b.app_id || '').toLowerCase();
        if (id === 'cloud.lazycat.app.netwatch' || id === 'netwatch') return true;
        // Docker compose project is dots stripped: cloudlazycatappnetwatch
        var proj = String(b.project || '').toLowerCase().replace(/[^a-z0-9]/g, '');
        if (proj === 'cloudlazycatappnetwatch' || proj === 'netwatch') return true;
        // Avoid false positives from loose substring matches on unrelated projects.
        return false;
    }

    function themeToggleIconHTML(theme) {
        // Dark UI → show sun (switch to light); light UI → show moon.
        var name = theme === 'dark' ? 'sun' : 'moon';
        return '<span class="ui-icon ui-icon--' + name + '" aria-hidden="true"></span>';
    }

    function applyThemeToggleIcon(themeToggleEl, theme) {
        if (!themeToggleEl) return;
        themeToggleEl.innerHTML = themeToggleIconHTML(theme || document.documentElement.getAttribute('data-theme') || 'dark');
    }

    function initTheme(state, themeToggleEl) {
        var theme = localStorage.getItem('theme');
        if (!theme) {
            theme = 'light';
        }
        if (state) state.theme = theme;
        document.documentElement.setAttribute('data-theme', theme);
        var meta = document.querySelector('meta[name="theme-color"]');
        if (meta) meta.content = theme === 'dark' ? '#202124' : '#F5F6F7';
        applyThemeToggleIcon(themeToggleEl, theme);
        if (themeToggleEl && !themeToggleEl.dataset.themeBound) {
            themeToggleEl.dataset.themeBound = '1';
            themeToggleEl.addEventListener('click', function () {
                var newTheme = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
                var root = document.documentElement;
                root.classList.remove('theme-transition');
                void root.offsetWidth;
                root.classList.add('theme-transition');
                document.documentElement.setAttribute('data-theme', newTheme);
                localStorage.setItem('theme', newTheme);
                if (state) state.theme = newTheme;
                var m = document.querySelector('meta[name="theme-color"]');
                if (m) m.content = newTheme === 'dark' ? '#202124' : '#F5F6F7';
                applyThemeToggleIcon(themeToggleEl, newTheme);
                clearTimeout(state && state.themeTransitionTimer);
                if (state) {
                    state.themeTransitionTimer = setTimeout(function () {
                        root.classList.remove('theme-transition');
                    }, 280);
                }
            });
        }
    }

    return {
        escapeHtml: escapeHtml,
        setSelectOptions: setSelectOptions,
        applyThemeToggleIcon: applyThemeToggleIcon,
        showToast: showToast,
        observationTimestampLabel: observationTimestampLabel,
        setObservationStatus: setObservationStatus,
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
