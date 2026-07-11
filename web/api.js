/*
 * Netwatch shared API + SSE helpers.
 * Keeps fetch/error handling consistent across pages without a bundler.
 */
window.NetwatchAPI = (function () {
    function buildURL(path, query) {
        var url = path;
        if (query && typeof query === 'object') {
            var params = new URLSearchParams();
            Object.keys(query).forEach(function (key) {
                var value = query[key];
                if (value === undefined || value === null || value === '') return;
                params.set(key, String(value));
            });
            var qs = params.toString();
            if (qs) url += (url.indexOf('?') >= 0 ? '&' : '?') + qs;
        }
        return url;
    }

    async function request(path, options) {
        options = options || {};
        var init = {
            method: options.method || 'GET',
            cache: options.cache || 'no-store',
            headers: Object.assign({}, options.headers || {})
        };
        if (options.body !== undefined && options.body !== null) {
            if (typeof options.body === 'string' || options.body instanceof Blob || options.body instanceof ArrayBuffer) {
                init.body = options.body;
            } else {
                init.headers['Content-Type'] = init.headers['Content-Type'] || 'application/json';
                init.body = JSON.stringify(options.body);
            }
        }
        if (options.signal) init.signal = options.signal;

        var response = await fetch(buildURL(path, options.query), init);
        var contentType = (response.headers.get('content-type') || '').toLowerCase();
        var isJSON = contentType.indexOf('application/json') >= 0;
        var payload = null;
        if (response.status !== 204) {
            if (isJSON) {
                try { payload = await response.json(); } catch (_) { payload = null; }
            } else {
                try { payload = await response.text(); } catch (_) { payload = null; }
            }
        }
        if (!response.ok) {
            var message = 'HTTP ' + response.status;
            if (payload && typeof payload === 'object' && payload.error) message = payload.error;
            else if (typeof payload === 'string' && payload.trim()) message = payload.trim().slice(0, 180);
            var err = new Error(message);
            err.status = response.status;
            err.payload = payload;
            throw err;
        }
        return payload;
    }

    function get(path, query, options) {
        return request(path, Object.assign({}, options || {}, { method: 'GET', query: query }));
    }

    function post(path, body, options) {
        return request(path, Object.assign({}, options || {}, { method: 'POST', body: body }));
    }

    function put(path, body, options) {
        return request(path, Object.assign({}, options || {}, { method: 'PUT', body: body }));
    }

    function createSSE(path, handlers) {
        handlers = handlers || {};
        var es = null;
        var closed = false;
        var retryTimer = null;
        var attempt = 0;
        var status = 'idle';

        function setStatus(next) {
            if (status === next) return;
            status = next;
            if (typeof handlers.onStatus === 'function') handlers.onStatus(next);
        }

        function clearRetry() {
            if (retryTimer) {
                clearTimeout(retryTimer);
                retryTimer = null;
            }
        }

        function bind() {
            if (closed) return;
            clearRetry();
            try {
                if (es) {
                    try { es.close(); } catch (_) {}
                    es = null;
                }
                setStatus(attempt === 0 ? 'connecting' : 'reconnecting');
                es = new EventSource(path);
                es.onopen = function () {
                    attempt = 0;
                    setStatus('live');
                    if (typeof handlers.onOpen === 'function') handlers.onOpen();
                };
                if (handlers.events) {
                    Object.keys(handlers.events).forEach(function (name) {
                        es.addEventListener(name, function (ev) {
                            try {
                                var data = ev.data ? JSON.parse(ev.data) : null;
                                handlers.events[name](data, ev);
                            } catch (err) {
                                if (typeof handlers.onError === 'function') handlers.onError(err);
                            }
                        });
                    });
                }
                es.onerror = function () {
                    setStatus('degraded');
                    if (typeof handlers.onError === 'function') handlers.onError(new Error('sse disconnected'));
                    // EventSource auto-reconnects; keep status visible.
                    if (es && es.readyState === EventSource.CLOSED) {
                        attempt += 1;
                        var delay = Math.min(15000, 800 * Math.pow(1.6, Math.min(attempt, 8)));
                        clearRetry();
                        retryTimer = setTimeout(bind, delay);
                    }
                };
            } catch (err) {
                setStatus('offline');
                if (typeof handlers.onError === 'function') handlers.onError(err);
                attempt += 1;
                clearRetry();
                retryTimer = setTimeout(bind, Math.min(15000, 1000 * attempt));
            }
        }

        bind();
        return {
            close: function () {
                closed = true;
                clearRetry();
                if (es) {
                    try { es.close(); } catch (_) {}
                    es = null;
                }
                setStatus('closed');
            },
            status: function () { return status; }
        };
    }

    return {
        request: request,
        get: get,
        post: post,
        put: put,
        createSSE: createSSE
    };
})();
