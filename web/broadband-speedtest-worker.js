/* Browser-side public broadband test. The worker intentionally never talks to
 * the Netwatch server for test bytes; it connects directly to the selected
 * public endpoint family. */
importScripts('/broadband-node-config.js');

(function () {
    'use strict';
    var active = false;
    var abortController = null;
    var nodeCatalog = (self.NetwatchBroadbandNodes && self.NetwatchBroadbandNodes.nodes) || [];
    var cfg = { durationSec: 10, downloadStreams: 15, uploadStreams: 5, pingCount: 5, graceSec: 2 };

    function emit(type, data) { self.postMessage(Object.assign({ type: type }, data || {})); }
    function now() { return (self.performance && performance.now) ? performance.now() : Date.now(); }
    function randomItem(items) { return items[Math.floor(Math.random() * items.length)]; }
    function cacheBust(url) { return url + (url.indexOf('?') >= 0 ? '&' : '?') + 'nocache=' + Math.random().toString(36).slice(2); }
    function secureUrl(url) { return location.protocol !== 'https:' || /^https:/i.test(url); }
    function resolveNode(request) {
        var id = String((request && request.nodeId) || '1');
        if (request && request.node && request.node.id === id) return request.node;
        for (var i = 0; i < nodeCatalog.length; i++) if (nodeCatalog[i].id === id) return nodeCatalog[i];
        throw new Error('未知测速节点: ' + id);
    }
    function canUse(url) { return secureUrl(url); }
    function chooseUrl(urls) {
        var usable = (urls || []).filter(canUse);
        if (!usable.length) throw new Error('当前页面协议无法访问该节点');
        return randomItem(usable);
    }
    function requestPing(url, signal) {
        return fetch(cacheBust(url), { method: 'GET', cache: 'no-store', mode: 'cors', signal: signal }).then(function (response) {
            // Reaching the server is enough for latency measurement. Public
            // CDN ping paths may intentionally answer 404 while remaining usable.
            return response.body && response.body.cancel ? response.body.cancel() : undefined;
        });
    }
    async function ping(node, signal) {
        var samples = [];
        var url = chooseUrl(node.pingUrls);
        for (var i = 0; i < cfg.pingCount; i++) {
            if (signal.aborted) throw new DOMException('aborted', 'AbortError');
            var start = now();
            try {
                await requestPing(url, signal);
                if (i > 0) samples.push(now() - start);
            } catch (e) {
                if (e && e.name === 'AbortError') throw e;
            }
        }
        if (!samples.length) throw new Error('延迟节点不可用或未返回 CORS');
        var sum = samples.reduce(function (a, b) { return a + b; }, 0);
        var avg = sum / samples.length;
        var jitter = samples.length > 1 ? samples.slice(1).reduce(function (a, value, index) { return a + Math.abs(value - samples[index]); }, 0) / (samples.length - 1) : 0;
        return { latencyMs: avg, jitterMs: jitter, samples: samples };
    }
    async function download(node, signal) {
        var started = now(), graceAt = started + cfg.graceSec * 1000, endAt = started + cfg.durationSec * 1000;
        var total = 0, lastEmit = 0, workers = [], failedUrls = Object.create(null);
        var availableUrls = (node.downloadUrls || []).filter(canUse);
        var stageController = new AbortController();
        var stageSignal = stageController.signal;
        function abortStage() { stageController.abort(); }
        var stageTimer = setTimeout(abortStage, Math.max(0, endAt - now()));
        signal.addEventListener('abort', abortStage, { once: true });
        function nextUrl() {
            var candidates = availableUrls.filter(function (url) { return !failedUrls[url]; });
            return candidates.length ? randomItem(candidates) : '';
        }
        function rejectUrl(url) { if (url) failedUrls[url] = true; }
        async function stream() {
            while (!stageSignal.aborted && now() < endAt) {
                var url = nextUrl();
                if (!url) return;
                var response;
                try { response = await fetch(cacheBust(url), { cache: 'no-store', mode: 'cors', signal: stageSignal }); }
                catch (e) {
                    if (e.name === 'AbortError') {
                        if (signal.aborted) throw e;
                        return;
                    }
                    rejectUrl(url);
                    continue;
                }
                if (!response.ok || !response.body) {
                    if (response.body && response.body.cancel) await response.body.cancel().catch(function () {});
                    rejectUrl(url);
                    continue;
                }
                var reader = response.body.getReader();
                try {
                    while (!stageSignal.aborted && now() < endAt) {
                        var part = await reader.read();
                        if (part.done) break;
                        var t = now();
                        if (t > graceAt) {
                            total += part.value.byteLength;
                            if (t - lastEmit > 200) {
                                lastEmit = t;
                                emit('progress', { stage: 'download', bytes: total, elapsedMs: t - graceAt });
                            }
                        }
                    }
                } catch (e) {
                    if (e.name === 'AbortError') {
                        if (signal.aborted) throw e;
                        return;
                    }
                    rejectUrl(url);
                } finally {
                    await reader.cancel().catch(function () {});
                }
            }
        }
        for (var i = 0; i < cfg.downloadStreams; i++) workers.push(stream());
        try {
            await Promise.all(workers);
        } finally {
            clearTimeout(stageTimer);
            signal.removeEventListener('abort', abortStage);
            stageController.abort();
        }
        if (signal.aborted) throw new DOMException('aborted', 'AbortError');
        if (total === 0) throw new Error('下载节点不可用或未返回 CORS');
        var elapsed = Math.max(1, Math.min(endAt, now()) - graceAt);
        return { mbps: total * 8 * 1.06 / (elapsed / 1000) / 1000000, bytes: total, elapsedMs: elapsed };
    }
    function randomBlob(size) {
        var bytes = new Uint8Array(size);
        if (self.crypto && crypto.getRandomValues) {
            for (var offset = 0; offset < bytes.length; offset += 65536) crypto.getRandomValues(bytes.subarray(offset, Math.min(offset + 65536, bytes.length)));
        } else { for (var i = 0; i < bytes.length; i++) bytes[i] = (Math.random() * 256) | 0; }
        return new Blob([bytes], { type: 'application/octet-stream' });
    }
    async function upload(node, signal) {
        var started = now(), graceAt = started + cfg.graceSec * 1000, endAt = started + cfg.durationSec * 1000;
        var total = 0, lastEmit = 0, blob = randomBlob(20 * 1024 * 1024), failedUrls = Object.create(null);
        var availableUrls = (node.uploadUrls || []).filter(canUse);
        function nextUrl() {
            var candidates = availableUrls.filter(function (url) { return !failedUrls[url]; });
            return candidates.length ? randomItem(candidates) : '';
        }
        function one() {
            return new Promise(function (resolve) {
                if (signal.aborted || now() >= endAt) return resolve();
                var url = nextUrl();
                if (!url) return resolve();
                var xhr = new XMLHttpRequest();
                var settled = false;
                var deadlineTimer;
                function abortRequest() { try { xhr.abort(); } catch (e) {} }
                function finish(failed) {
                    if (settled) return;
                    settled = true;
                    clearTimeout(deadlineTimer);
                    signal.removeEventListener('abort', abortRequest);
                    if (failed) failedUrls[url] = true;
                    if (!signal.aborted && now() < endAt && nextUrl()) {
                        setTimeout(function () { one().then(resolve); }, failed ? 20 : 0);
                    } else {
                        resolve();
                    }
                }
                xhr.open('POST', cacheBust(url), true);
                xhr.upload.onprogress = function (event) {
                    if (event.lengthComputable) {
                        var delta = Math.max(0, event.loaded - (xhr.__lastLoaded || 0)); xhr.__lastLoaded = event.loaded;
                        var t = now();
                        if (t > graceAt) {
                            total += delta;
                            if (t - lastEmit > 200) { lastEmit = t; emit('progress', { stage: 'upload', bytes: total, elapsedMs: t - graceAt }); }
                        }
                    }
                };
                xhr.onloadend = function () { finish(xhr.status < 200 || xhr.status >= 400); };
                xhr.onerror = function () { finish(true); };
                xhr.onabort = function () { finish(false); };
                signal.addEventListener('abort', abortRequest, { once: true });
                deadlineTimer = setTimeout(abortRequest, Math.max(0, endAt - now()));
                xhr.send(blob);
            });
        }
        var workers = []; for (var i = 0; i < cfg.uploadStreams; i++) workers.push(one());
        await Promise.all(workers);
        if (signal.aborted) throw new DOMException('aborted', 'AbortError');
        if (total === 0) throw new Error('上传节点不可用或未返回 CORS');
        var elapsed = Math.max(1, Math.min(endAt, now()) - graceAt);
        return { mbps: total * 8 * 1.06 / (elapsed / 1000) / 1000000, bytes: total, elapsedMs: elapsed };
    }
    async function run(request) {
        cfg.durationSec = Math.max(4, Math.min(40, Number(request.durationSec) || 10));
        cfg.downloadStreams = Math.max(1, Math.min(15, Number(request.downloadStreams) || 15));
        cfg.uploadStreams = Math.max(1, Math.min(5, Number(request.uploadStreams) || 5));
        var node = resolveNode(request || {});
        var controller = new AbortController(); abortController = controller;
        emit('state', { stage: 'starting', node: node, progress: 0 });
        var pingResult = await ping(node, controller.signal);
        emit('state', { stage: 'ping', node: node, progress: 15, latencyMs: pingResult.latencyMs, jitterMs: pingResult.jitterMs });
        var dl = await download(node, controller.signal);
        emit('state', { stage: 'download', node: node, progress: 55, downloadMbps: dl.mbps, downloadBytes: dl.bytes });
        var ul = await upload(node, controller.signal);
        emit('state', { stage: 'upload', node: node, progress: 95, uploadMbps: ul.mbps, uploadBytes: ul.bytes });
        emit('complete', { result: { node_id: node.id, node_name: node.label, node_category: node.category, download_mbps: dl.mbps, upload_mbps: ul.mbps, latency_ms: Math.round(pingResult.latencyMs), jitter_ms: Math.round(pingResult.jitterMs), download_bytes: dl.bytes, upload_bytes: ul.bytes } });
    }
    self.onmessage = function (event) {
        var message = event.data || {};
        if (message.type === 'stop') { if (abortController) abortController.abort(); active = false; emit('stopped'); return; }
        if (message.type !== 'start' || active) return;
        active = true;
        run(message).catch(function (error) { if (error && error.name === 'AbortError') emit('stopped'); else emit('error', { message: error && error.message ? error.message : String(error) }); }).finally(function () { active = false; abortController = null; });
    };
})();
