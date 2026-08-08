window.__app = window.__app || {};

(function () {
var state = window.__app.state;
var els = window.__app.els;
var i18n = window.__app.i18n;

async function loadSpeedConfig() {
    try {
        var data = window.NetwatchAPI
            ? await window.NetwatchAPI.get('/api/v1/speed/config')
            : await (async function () {
                var response = await fetch('/api/v1/speed/config', { cache: 'no-store' });
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return response.json();
            })();
        state.speedConfig = {
            broadband_duration_sec: data.broadband_duration_sec || 10,
            local_transfer_duration_sec: data.local_transfer_duration_sec || 10,
            local_transfer_payload_mb: data.local_transfer_payload_mb || 32
        };
    } catch (error) {
        console.error(error);
    }
    els.broadbandNote.textContent = i18n('broadband_note_prefix') + state.speedConfig.broadband_duration_sec + i18n('seconds_unit');
    els.transferNote.textContent = i18n('transfer_note_prefix') + state.speedConfig.local_transfer_duration_sec + i18n('seconds_unit');
}

async function loadSpeedHistory() {
    try {
        var results = await Promise.all([
            window.NetwatchAPI ? window.NetwatchAPI.get('/api/v1/speed/broadband/history') : fetch('/api/v1/speed/broadband/history', { cache: 'no-store' }).then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); }),
            window.NetwatchAPI ? window.NetwatchAPI.get('/api/v1/speed/local/history') : fetch('/api/v1/speed/local/history', { cache: 'no-store' }).then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
        ]);
        renderBroadbandHistory(Array.isArray(results[0]) ? results[0] : []);
        renderTransferHistory(Array.isArray(results[1]) ? results[1] : []);
    } catch (error) {
        console.error(error);
    }
}

function renderBroadbandHistory(items) {
    els.broadbandHistory.innerHTML = items.map(function (item) {
        return '<div class="history-item"><div class="history-item-main"><div class="history-item-info"><span class="history-item-value">' + (item.download_mbps && item.download_mbps.toFixed ? item.download_mbps.toFixed(2) : '0.00') + ' / ' + (item.upload_mbps && item.upload_mbps.toFixed ? item.upload_mbps.toFixed(2) : '0.00') + ' <small>Mbit/s</small></span><small>' + NetwatchShared.escapeHtml(item.timestamp || '--') + (item.provider ? ' \u00B7 ' + NetwatchShared.escapeHtml(item.provider) : '') + (item.node_source ? ' \u00B7 ' + NetwatchShared.escapeHtml(item.node_source) : '') + '</small></div>' + historyNoteHTML('broadband', item) + '<div class="history-item-metrics"><small>' + i18n('latency_col') + ' ' + (item.latency_ms || 0) + ' ms</small></div></div></div>';
    }).join('') || '<div class="history-item"><small>' + i18n('no_history') + '</small></div>';
}

function renderTransferHistory(items) {
    els.transferHistory.innerHTML = items.map(function (item) {
        return '<div class="history-item"><div class="history-item-main"><div class="history-item-info"><span class="history-item-value">' + (item.download_mbps && item.download_mbps.toFixed ? item.download_mbps.toFixed(2) : '0.00') + ' / ' + (item.upload_mbps && item.upload_mbps.toFixed ? item.upload_mbps.toFixed(2) : '0.00') + ' <small>Mbit/s</small></span><small>' + NetwatchShared.escapeHtml(item.timestamp || '--') + ' \u00B7 ' + i18n('total') + ' ' + window.__app.formatMB(item.payload_mb || ((item.download_mb || 0) + (item.upload_mb || 0))) + '</small></div>' + historyNoteHTML('local', item) + '<div class="history-item-metrics"><small>RTT ' + (item.rtt_avg_ms || item.round_trip_latency_ms || 0) + ' ms</small></div></div></div>';
    }).join('') || '<div class="history-item"><small>' + i18n('no_history') + '</small></div>';
}

function historyNoteHTML(kind, item) {
    var note = String(item.note || '');
    return '<div class="history-note" data-kind="' + kind + '" data-id="' + NetwatchShared.escapeHtml(item.id || '') + '"><span class="history-note-label">' + i18n('speed_history_note') + '</span><span class="history-note-text' + (note ? '' : ' placeholder') + '">' + NetwatchShared.escapeHtml(note || i18n('speed_history_add_note')) + '</span><button type="button" class="history-note-edit" data-note="' + NetwatchShared.escapeHtml(note) + '">' + i18n('speed_history_note') + '</button></div>';
}

async function saveSpeedHistoryNote(kind, id, note) {
    var payload = { kind: kind, id: id, note: note };
    if (window.NetwatchAPI) return window.NetwatchAPI.post('/api/v1/speed/history/note', payload);
    var response = await fetch('/api/v1/speed/history/note', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
    var data = await response.json().catch(function () { return {}; });
    if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
    return data;
}

function bindSpeedHistoryNotes() {
    [els.broadbandHistory, els.transferHistory].forEach(function (container) {
        if (!container || container.dataset.noteBound) return;
        container.dataset.noteBound = '1';
        container.addEventListener('click', function (event) {
            var edit = event.target.closest && event.target.closest('.history-note-edit');
            if (!edit) return;
            var row = edit.closest('.history-note');
            if (!row) return;
            var note = edit.dataset.note || '';
            row.innerHTML = '<input class="history-note-input" maxlength="200" value="' + NetwatchShared.escapeHtml(note) + '" placeholder="' + NetwatchShared.escapeHtml(i18n('speed_history_note_placeholder')) + '"><button type="button" class="history-note-save">' + i18n('save_btn') + '</button><button type="button" class="history-note-cancel">' + i18n('close_btn') + '</button>';
            var input = row.querySelector('.history-note-input');
            input.focus();
        });
        container.addEventListener('click', async function (event) {
            var row = event.target.closest && event.target.closest('.history-note');
            if (!row) return;
            if (event.target.closest('.history-note-cancel')) {
                await loadSpeedHistory();
                return;
            }
            if (!event.target.closest('.history-note-save')) return;
            var input = row.querySelector('.history-note-input');
            try {
                await saveSpeedHistoryNote(row.dataset.kind, row.dataset.id, input ? input.value.trim() : '');
                await loadSpeedHistory();
            } catch (error) {
                if (input) input.setCustomValidity(error.message || i18n('save_failed'));
                if (input) input.reportValidity();
            }
        });
    });
}

function resetBroadbandDetails() {
    window.__app.setText(els.broadbandNodeName);
    window.__app.setText(els.broadbandNodeProvider);
    window.__app.setText(els.broadbandNodeRegion);
}

function renderBroadbandDetails(result) {
    result = result || {};
    window.__app.setText(els.broadbandNodeName, result.server_name || result.server_region || '--');
    window.__app.setText(els.broadbandNodeProvider, result.provider || '--');
    window.__app.setText(els.broadbandNodeRegion, result.server_country || result.server_region || '--');
}

function resetTransferDetails() {
}

function renderTransferDetails(stats) {
    stats = stats || {};
    var downloadMB = Number(stats.download_mb) || 0;
    var uploadMB = Number(stats.upload_mb) || 0;
    window.__app.setText(els.transferLatency, window.__app.formatMS(stats.rtt_avg_ms));
    window.__app.setText(els.transferJitter, window.__app.formatMS(stats.jitter_ms));
}

function resetBroadbandMetrics() {
    els.broadbandNote.textContent = i18n('broadband_note_prefix') + state.speedConfig.broadband_duration_sec + i18n('seconds_unit');
    window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Idle', i18n('standby'));
    window.__app.setSpeedPanelMode('broadband', 'Idle');
    els.broadbandDownload.textContent = '--';
    els.broadbandUpload.textContent = '--';
    els.broadbandLatency.textContent = '--';
    els.broadbandJitter.textContent = '--';
    resetBroadbandDetails();
    renderBroadbandSteps([]);
}

function resetTransferMetrics() {
    els.transferNote.textContent = i18n('transfer_note_prefix') + state.speedConfig.local_transfer_duration_sec + i18n('seconds_unit');
    window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, 'Idle', i18n('standby'));
    window.__app.setSpeedPanelMode('transfer', 'Idle');
    els.transferDownload.textContent = '--';
    els.transferUpload.textContent = '--';
    els.transferLatency.textContent = '--';
    els.transferJitter.textContent = '--';
    resetTransferDetails();
}

function renderBroadbandSteps(steps) {
    var container = els.broadbandSteps;
    if (!container) return;
    if (!steps || steps.length === 0) {
        container.innerHTML = '<div class="st-log-empty">' + i18n('standby') + '</div>';
        container.dataset.sig = '';
        return;
    }
    var last = steps[steps.length - 1];
    var sig = steps.length + ':' + last.seq + ':' + last.status;
    if (container.dataset.sig === sig) return;
    container.innerHTML = steps.map(function (s) {
        var icon = window.__app.broadbandStepIcon[s.status] || '\u2022';
        var time = (s.time || '').split(' ').pop() || '';
        return '<div class="st-log-item ' + (s.status || 'info') + '"><span class="st-log-icon">' + icon + '</span><span class="st-log-msg">' + NetwatchShared.escapeHtml(s.message || '') + '</span><span class="st-log-time">' + NetwatchShared.escapeHtml(time) + '</span></div>';
    }).join('');
    container.dataset.sig = sig;
    container.scrollTop = container.scrollHeight;
}

function renderBroadbandTask(task) {
    task = task || {};
    els.broadbandNote.textContent = task.message || window.__app.broadbandStageMap[task.stage] || i18n('standby');
    els.broadbandLatency.textContent = window.__app.formatMS(task.result && task.result.latency_ms);
    els.broadbandJitter.textContent = window.__app.formatMS(task.result && task.result.jitter_ms);
    els.broadbandDownload.textContent = window.__app.formatMbps(task.result && task.result.download_mbps);
    els.broadbandUpload.textContent = window.__app.formatMbps(task.result && task.result.upload_mbps);
    renderBroadbandDetails(task.result || {});
    renderBroadbandSteps(task.steps);

    if (task.stage === 'latency') {
        window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Ping', task.message || i18n('latency_sampling'));
        window.__app.setSpeedPanelMode('broadband', 'Ping');
        return;
    }
    if (task.stage === 'download') {
        window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Download', task.message || i18n('downloading'));
        window.__app.setSpeedPanelMode('broadband', 'Download');
        return;
    }
    if (task.stage === 'upload') {
        window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Upload', task.message || i18n('uploading'));
        window.__app.setSpeedPanelMode('broadband', 'Upload');
        return;
    }
    window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Result', task.message || i18n('speedtest_complete'));
    window.__app.setSpeedPanelMode('broadband', 'Result');
}

function updateTransferProgress(stage, progress, message) {
    els.transferNote.textContent = stage;
}

function stopBroadbandPolling() {
    if (state.broadbandPoller) {
        clearInterval(state.broadbandPoller);
        state.broadbandPoller = null;
    }
}

async function pollBroadbandTask() {
    try {
        var task = window.NetwatchAPI
            ? await window.NetwatchAPI.get('/api/v1/speed/broadband/task')
            : await (await fetch('/api/v1/speed/broadband/task', { cache: 'no-store' })).json();
        renderBroadbandTask(task);
        if (!task.running) {
            stopBroadbandPolling();
            if (state.runningTest === 'broadband') {
                state.runningTest = null;
            }
            window.__app.updateWindowControls();
            if (task.finished) {
                await loadSpeedHistory();
            }
            var failure = task.result && (task.result.failure_reason || task.result.error);
            if (failure && !state.broadbandFailureShown) {
                state.broadbandFailureShown = true;
                NetwatchShared.confirmDialog({
                    title: i18n('speedtest_failed'),
                    message: failure,
                    okText: i18n('close_btn'),
                    cancelText: i18n('close_btn'),
                    danger: true
                });
            }
        }
    } catch (error) {
        console.error(error);
    }
}

function startBroadbandPolling() {
    stopBroadbandPolling();
    state.broadbandPoller = setInterval(pollBroadbandTask, 500);
}

async function startBroadbandTest() {
    if (state.runningTest) return;
    state.runningTest = 'broadband';
    state.broadbandFailureShown = false;
    window.__app.updateWindowControls();
    resetBroadbandMetrics();
    els.broadbandNote.textContent = i18n('started_broadband');
    try {
        var task = window.NetwatchAPI
            ? await window.NetwatchAPI.post('/api/v1/speed/broadband/start')
            : await (await fetch('/api/v1/speed/broadband/start', { method: 'POST' })).json();
        renderBroadbandTask(task);
        startBroadbandPolling();
    } catch (error) {
        console.error(error);
        state.runningTest = null;
        window.__app.updateWindowControls();
        els.broadbandNote.textContent = i18n('broadband_start_failed');
    }
}

async function cancelBroadbandTest(showStopped) {
    if (showStopped === undefined) showStopped = true;
    stopBroadbandPolling();
    try {
        if (window.NetwatchAPI) await window.NetwatchAPI.post('/api/v1/speed/broadband/cancel');
        else await fetch('/api/v1/speed/broadband/cancel', { method: 'POST' });
    } catch (error) {
        console.error(error);
    } finally {
        if (state.runningTest === 'broadband') {
            state.runningTest = null;
        }
        window.__app.updateWindowControls();
        if (showStopped) {
            els.broadbandStage.textContent = i18n('canceled');
            els.broadbandNote.textContent = i18n('speedtest_stopped');
            els.broadbandProgress.textContent = '0%';
            window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Stopped', i18n('manual_stop'));
            window.__app.setSpeedPanelMode('broadband', 'Stopped');
        }
    }
}

function finishTransferRun() {
    if (state.runningTest === 'transfer') {
        state.runningTest = null;
    }
    state.transferAbortController = null;
    window.__app.updateWindowControls();
}

async function runTransferTest() {
    if (state.runningTest) return;
    state.runningTest = 'transfer';
    window.__app.updateWindowControls();
    resetTransferMetrics();

    var s = new Speedtest();
    state.transferAbortController = { abort: function () { s.abort(); } };
    var durationSec = Math.max(1, Math.min(60, Number(state.speedConfig.local_transfer_duration_sec) || 10));

    s.setParameter('url_dl', '/api/v1/speed/local/download?sec=' + durationSec);
    s.setParameter('url_ul', '/api/v1/speed/local/upload');
    s.setParameter('url_ping', '/api/v1/speed/local/ping');
    s.setParameter('url_getIp', '/api/v1/summary');
    s.setParameter('worker_path', '/speedtest_worker.js');
    s.setParameter('test_order', 'P_D_U');
    s.setParameter('time_dl_max', durationSec);
    s.setParameter('time_ul_max', durationSec);
    // Fixed window: do not auto-shorten on fast links (that under-samples steady state).
    s.setParameter('time_auto', false);
    // More pings reduce RTT noise on local loopback / LAN.
    s.setParameter('count_ping', 20);
    // Skip TCP slow-start before measuring.
    s.setParameter('time_dlGraceTime', Math.min(2.5, Math.max(1.5, durationSec * 0.2)));
    s.setParameter('time_ulGraceTime', Math.min(3.5, Math.max(2.5, durationSec * 0.25)));
    // Saturate local multi-gig paths; explicit values also disable browser quirks overrides.
    s.setParameter('xhr_dlMultistream', 8);
    s.setParameter('xhr_ulMultistream', 6);
    s.setParameter('xhr_multistreamDelay', 150);
    s.setParameter('xhr_ignoreErrors', 1);
    // Larger upload blobs keep each stream busy longer between restarts.
    s.setParameter('xhr_ul_blob_megabytes', 32);
    // Local path: report application-layer throughput without ISP-style overhead inflation.
    s.setParameter('overheadCompensationFactor', 1.0);
    s.setParameter('useMebibits', false);
    s.setParameter('enable_quirks', false);
    s.setParameter('ping_allowPerformanceApi', true);

    var lastData = {};
    var transferStartedAt = Date.now();

    s.onupdate = function (data) {
        if (state.runningTest !== 'transfer') return;
        lastData = data;
        var rtt = window.__app.summarizeRTT(data.pingSamples || []);
        var transferStats = {
            rtt_min_ms: rtt.min,
            rtt_avg_ms: rtt.avg || window.__app.finiteNumber(data.pingStatus),
            rtt_max_ms: rtt.max,
            jitter_ms: window.__app.finiteNumber(data.jitterStatus),
            download_mb: window.__app.bytesToMB(data.dlBytes),
            upload_mb: window.__app.bytesToMB(data.ulBytes),
            duration_ms: (Number(data.dlDuration) || 0) + (Number(data.ulDuration) || 0)
        };
        transferStats.payload_mb = transferStats.download_mb + transferStats.upload_mb;
        renderTransferDetails(transferStats);

        var testStateMap = {
            0: { stage: i18n('preparing'), progress: 0, mode: 'Idle' },
            1: { stage: i18n('dl_speedtest'), progress: 15 + (data.dlProgress || 0) * 40, mode: 'Download' },
            2: { stage: i18n('latency'), progress: 5 + (data.pingProgress || 0) * 10, mode: 'Ping' },
            3: { stage: i18n('ul_speedtest'), progress: 55 + (data.ulProgress || 0) * 45, mode: 'Upload' }
        };

        var current = testStateMap[data.testState];
        if (current) {

            var speed = 0;
            var caption = '';
            if (current.mode === 'Download') {
                speed = window.__app.finiteNumber(data.dlStatus);
                els.transferDownload.textContent = speed.toFixed(1);
                caption = i18n('downloading') + '... ' + speed.toFixed(2) + ' Mbit/s';
            } else if (current.mode === 'Upload') {
                speed = window.__app.finiteNumber(data.ulStatus);
                els.transferUpload.textContent = speed.toFixed(1);
                caption = i18n('uploading') + '... ' + speed.toFixed(2) + ' Mbit/s';
            } else if (current.mode === 'Ping') {
                speed = window.__app.finiteNumber(data.pingStatus);
                els.transferLatency.textContent = window.__app.formatMS(speed);
                els.transferJitter.textContent = window.__app.formatMS(window.__app.finiteNumber(data.jitterStatus));
                caption = i18n('latency_sampling') + '... ' + speed.toFixed(0) + ' ms';
            }

            window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, current.mode, caption);
            window.__app.setSpeedPanelMode('transfer', current.mode);
        }
    };

    s.onend = async function (aborted) {
        if (aborted) {
            finishTransferRun();
            return;
        }

        // Prefer byte/time throughput over the last instantaneous sample.
        // LibreSpeed dlStatus/ulStatus are rolling averages that can lag or spike at stop.
        var computeMbps = function (bytes, durationMs, statusFallback) {
            var b = Number(bytes) || 0;
            var ms = Number(durationMs) || 0;
            if (b > 0 && ms > 50) {
                return (b * 8) / (ms / 1000) / 1e6;
            }
            return window.__app.finiteNumber(statusFallback, 0);
        };
        var downloadMbps = computeMbps(lastData.dlBytes, lastData.dlDuration, lastData.dlStatus || els.transferDownload.textContent);
        var uploadMbps = computeMbps(lastData.ulBytes, lastData.ulDuration, lastData.ulStatus || els.transferUpload.textContent);
        // Soft-blend with worker status when both exist and are close (noise reduction).
        var blend = function (computed, statusRaw) {
            var status = window.__app.finiteNumber(statusRaw, 0);
            if (computed <= 0) return status;
            if (status <= 0) return computed;
            var lo = Math.min(computed, status);
            var hi = Math.max(computed, status);
            if (hi > 0 && (hi - lo) / hi <= 0.12) return (computed + status) / 2;
            return computed;
        };
        downloadMbps = blend(downloadMbps, lastData.dlStatus);
        uploadMbps = blend(uploadMbps, lastData.ulStatus);
        els.transferDownload.textContent = downloadMbps > 0 ? downloadMbps.toFixed(1) : '--';
        els.transferUpload.textContent = uploadMbps > 0 ? uploadMbps.toFixed(1) : '--';

        var pingMs = window.__app.finiteNumber(lastData.pingStatus, window.__app.finiteNumber(els.transferLatency.textContent));
        var jitterMs = window.__app.finiteNumber(lastData.jitterStatus, window.__app.finiteNumber(els.transferJitter.textContent));
        var rtt = window.__app.summarizeRTT(lastData.pingSamples || []);

        var transferStats = {
            download_mb: window.__app.bytesToMB(lastData.dlBytes),
            upload_mb: window.__app.bytesToMB(lastData.ulBytes),
            duration_ms: ((Number(lastData.dlDuration) || 0) + (Number(lastData.ulDuration) || 0)) || (Date.now() - transferStartedAt),
            rtt_min_ms: rtt.min || Math.round(pingMs),
            rtt_avg_ms: rtt.avg || Math.round(pingMs),
            rtt_max_ms: rtt.max || Math.round(pingMs),
            jitter_ms: Math.round(jitterMs)
        };
        transferStats.payload_mb = transferStats.download_mb + transferStats.upload_mb;
        renderTransferDetails(transferStats);

        els.transferNote.textContent = i18n('transfer_done');
        window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, 'Result', i18n('speedtest_complete'));
        window.__app.setSpeedPanelMode('transfer', 'Result');

        try {
            var localResultPayload = {
                download_mbps: downloadMbps,
                upload_mbps: uploadMbps,
                payload_mb: transferStats.payload_mb,
                download_mb: transferStats.download_mb,
                upload_mb: transferStats.upload_mb,
                duration_ms: Math.round(transferStats.duration_ms),
                round_trip_latency_ms: Math.round(pingMs),
                rtt_min_ms: transferStats.rtt_min_ms,
                rtt_avg_ms: transferStats.rtt_avg_ms,
                rtt_max_ms: transferStats.rtt_max_ms,
                jitter_ms: transferStats.jitter_ms
            };
            if (window.NetwatchAPI) {
                await window.NetwatchAPI.post('/api/v1/speed/local/result', localResultPayload);
            } else {
                await fetch('/api/v1/speed/local/result', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(localResultPayload)
                });
            }
            await loadSpeedHistory();
        } catch (e) {
            console.error('Failed to save transfer result:', e);
        }

        finishTransferRun();
    };

    updateTransferProgress(i18n('preparing'), 0, i18n('starting_transfer'));
    try {
        s.start();
    } catch (error) {
        console.error(error);
        els.transferNote.textContent = i18n('transfer_start_failed');
        window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, 'Error', i18n('speedtest_start_failed'));
        window.__app.setSpeedPanelMode('transfer', 'Error');
        finishTransferRun();
    }
}

function cancelTransferTest(showStopped) {
    if (showStopped === undefined) showStopped = true;
    if (state.transferAbortController) {
        state.transferAbortController.abort();
        state.transferAbortController = null;
    }
    if (state.runningTest === 'transfer') {
        state.runningTest = null;
    }
    window.__app.updateWindowControls();
    if (showStopped) {
        els.transferNote.textContent = i18n('transfer_stopped');
        window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, 'Stopped', i18n('manual_stop'));
        window.__app.setSpeedPanelMode('transfer', 'Stopped');
    }
}

window.__app.loadSpeedConfig = loadSpeedConfig;
window.__app.loadSpeedHistory = loadSpeedHistory;
window.__app.renderBroadbandHistory = renderBroadbandHistory;
window.__app.renderTransferHistory = renderTransferHistory;
window.__app.resetBroadbandMetrics = resetBroadbandMetrics;
window.__app.resetTransferMetrics = resetTransferMetrics;
window.__app.renderBroadbandTask = renderBroadbandTask;
window.__app.startBroadbandTest = startBroadbandTest;
window.__app.cancelBroadbandTest = cancelBroadbandTest;
window.__app.runTransferTest = runTransferTest;
window.__app.cancelTransferTest = cancelTransferTest;
bindSpeedHistoryNotes();
})();
