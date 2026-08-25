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
    els.broadbandNote.textContent = i18n('standby');
    els.transferNote.textContent = i18n('standby');
}

function speedAPIGet(path) {
    if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
    return fetch(path, { cache: 'no-store' }).then(function (response) {
        if (!response.ok) throw new Error('HTTP ' + response.status);
        return response.json();
    });
}

function speedAPIPost(path, body) {
    if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
    return fetch(path, {
        method: 'POST',
        headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        cache: 'no-store'
    }).then(async function (response) {
        var data = await response.json().catch(function () { return {}; });
        if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
        return data;
    });
}

function currentBroadbandMode() {
    if (state.broadbandMode === 'port-policy') return 'port-policy';
    return state.broadbandMode === 'server' ? 'server' : 'client';
}

function builtinBroadbandNodes() {
    return (window.NetwatchBroadbandNodes && window.NetwatchBroadbandNodes.nodes) || [];
}

function isBrowserUsableNode(node) {
    return currentBroadbandMode() !== 'client' || (node.browserUsable !== false && (location.protocol !== 'https:' || node.secure !== false));
}

function renderBroadbandNodeOptions() {
    if (!els.broadbandNodeSelect) return;
    var previous = els.broadbandNodeSelect.value;
    var nodes = state.broadbandNodes.length ? state.broadbandNodes : builtinBroadbandNodes();
    var options = nodes.map(function (node) {
        var usable = isBrowserUsableNode(node);
        var reason = usable ? '' : ' (' + (node.browserReason || i18n('broadband_browser_unavailable')) + ')';
        return { value: node.id, label: node.label + reason, disabled: !usable };
    });
    var selected = options.some(function (option) { return String(option.value) === previous && !option.disabled; })
        ? previous
        : ((options.find(function (option) { return !option.disabled; }) || options[0] || {}).value || '');
    NetwatchShared.setSelectOptions(els.broadbandNodeSelect, options, selected, options.length === 0);
}

function renderBroadbandMode() {
    var mode = currentBroadbandMode();
    [els.broadbandModeClient, els.broadbandModeServer, els.broadbandModePortPolicy].forEach(function (button) {
        if (!button) return;
        var active = button.dataset.broadbandMode === mode;
        button.setAttribute('aria-selected', active ? 'true' : 'false');
    });
    var portPolicy = mode === 'port-policy';
    if (els.broadbandPublicControls) els.broadbandPublicControls.hidden = portPolicy;
    if (els.broadbandPortPolicyControls) els.broadbandPortPolicyControls.hidden = !portPolicy;
    if (els.broadbandStandardResults) els.broadbandStandardResults.hidden = portPolicy;
    if (els.broadbandPortPolicyResults) els.broadbandPortPolicyResults.hidden = !portPolicy;
    if (els.broadbandHistorySection) els.broadbandHistorySection.hidden = portPolicy;
    if (!portPolicy) renderBroadbandNodeOptions();
    if (els.broadbandNodeStatus && !els.broadbandNodeStatus.dataset.refreshing) els.broadbandNodeStatus.textContent = '';
    if (portPolicy) resetPortPolicyMetrics();
}

function setBroadbandMode(mode) {
    if (state.runningTest || ['client', 'server', 'port-policy'].indexOf(mode) < 0) return;
    state.broadbandMode = mode;
    localStorage.setItem('netwatch_broadband_mode_v2', mode);
    renderBroadbandMode();
    resetBroadbandMetrics();
}

function selectedBroadbandRequest() {
    var id = String((els.broadbandNodeSelect && els.broadbandNodeSelect.value) || '1');
    return { nodeID: id };
}

async function loadBroadbandNodes() {
    if (state.runningTest) return;
    if (els.broadbandNodeStatus) els.broadbandNodeStatus.textContent = i18n('broadband_nodes_loading');
    try {
        var nodes = await speedAPIGet('/api/v1/speed/broadband/catalog');
        if (!Array.isArray(nodes) || !nodes.length) throw new Error(i18n('broadband_nodes_empty'));
        state.broadbandNodes = nodes.map(function (node) {
            return {
                id: node.id,
                label: node.label,
                category: node.category,
                secure: node.secure !== false,
                browserUsable: node.browser_usable !== false,
                browserReason: node.browser_reason || '',
                pingUrls: node.ping_urls || [],
                downloadUrls: node.download_urls || [],
                uploadUrls: node.upload_urls || []
            };
        });
        renderBroadbandNodeOptions();
        if (els.broadbandNodeStatus) els.broadbandNodeStatus.textContent = i18n('broadband_nodes_loaded').replace('{count}', String(state.broadbandNodes.length));
    } catch (error) {
        console.error(error);
        if (!state.broadbandNodes.length) state.broadbandNodes = builtinBroadbandNodes().slice();
        renderBroadbandMode();
        if (els.broadbandNodeStatus) els.broadbandNodeStatus.textContent = i18n('broadband_nodes_refresh_failed') + '：' + (error.message || i18n('load_failed'));
    }
}

function updateBroadbandControls() {
    var busy = !!state.runningTest;
    [els.broadbandModeClient, els.broadbandModeServer, els.broadbandModePortPolicy, els.broadbandNodeSelect, els.broadbandNodeRefresh].forEach(function (element) {
        if (!element) return;
        element.disabled = busy;
        if (element.tagName === 'SELECT' && window.syncCustomSelect) window.syncCustomSelect(element);
    });
    [els.clearBroadbandHistory, els.clearTransferHistory].forEach(function (element) {
        if (element) element.disabled = busy;
    });
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
        return '<div class="history-item"><div class="history-item-main"><div class="history-item-info"><span class="history-item-value">' + (item.download_mbps && item.download_mbps.toFixed ? item.download_mbps.toFixed(2) : '0.00') + ' / ' + (item.upload_mbps && item.upload_mbps.toFixed ? item.upload_mbps.toFixed(2) : '0.00') + ' <small>Mbit/s</small></span><small>' + NetwatchShared.escapeHtml(item.timestamp || '--') + '</small></div>' + historyNoteHTML('broadband', item) + '<div class="history-item-metrics"><small>' + i18n('latency_col') + ' ' + (item.latency_ms || 0) + ' ms · ' + i18n('latency_jitter') + ' ' + (item.jitter_ms || 0) + ' ms</small></div></div></div>';
    }).join('') || '<div class="history-item"><small>' + i18n('no_history') + '</small></div>';
}

function renderTransferHistory(items) {
    els.transferHistory.innerHTML = items.map(function (item) {
        return '<div class="history-item"><div class="history-item-main"><div class="history-item-info"><span class="history-item-value">' + (item.download_mbps && item.download_mbps.toFixed ? item.download_mbps.toFixed(2) : '0.00') + ' / ' + (item.upload_mbps && item.upload_mbps.toFixed ? item.upload_mbps.toFixed(2) : '0.00') + ' <small>Mbit/s</small></span><small>' + NetwatchShared.escapeHtml(item.timestamp || '--') + ' \u00B7 ' + i18n('total') + ' ' + window.__app.formatMB(item.payload_mb || ((item.download_mb || 0) + (item.upload_mb || 0))) + '</small></div>' + historyNoteHTML('local', item) + '<div class="history-item-metrics"><small>RTT ' + (item.rtt_avg_ms || item.round_trip_latency_ms || 0) + ' ms</small></div></div></div>';
    }).join('') || '<div class="history-item"><small>' + i18n('no_history') + '</small></div>';
}

function historyNoteHTML(kind, item) {
    var note = String(item.note || '');
    return '<div class="history-note" data-kind="' + kind + '" data-id="' + NetwatchShared.escapeHtml(item.id || '') + '"><span class="history-note-text' + (note ? '' : ' placeholder') + '" tabindex="0" role="button" data-note="' + NetwatchShared.escapeHtml(note) + '">' + NetwatchShared.escapeHtml(note || i18n('speed_history_add_note')) + '</span></div>';
}

async function saveSpeedHistoryNote(kind, id, note) {
    var payload = { kind: kind, id: id, note: note };
    if (window.NetwatchAPI) return window.NetwatchAPI.post('/api/v1/speed/history/note', payload);
    var response = await fetch('/api/v1/speed/history/note', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
    var data = await response.json().catch(function () { return {}; });
    if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
    return data;
}

async function clearSpeedHistory(kind) {
    var ok = await NetwatchShared.confirmDialog({
        title: i18n('clear_history_title'),
        message: i18n('clear_history_confirm'),
        okText: i18n('clear_btn'),
        cancelText: i18n('close_btn'),
        danger: true
    });
    if (!ok) return;
    try {
        if (window.NetwatchAPI) {
            await window.NetwatchAPI.post('/api/v1/speed/history/clear', { kind: kind });
        } else {
            var response = await fetch('/api/v1/speed/history/clear', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ kind: kind })
            });
            if (!response.ok) throw new Error('HTTP ' + response.status);
        }
        await loadSpeedHistory();
    } catch (error) {
        NetwatchShared.showToast(error.message || i18n('clear_failed'), 'error');
    }
}

function bindSpeedHistoryNotes() {
    [els.broadbandHistory, els.transferHistory].forEach(function (container) {
        if (!container || container.dataset.noteBound) return;
        container.dataset.noteBound = '1';
        container.addEventListener('click', function (event) {
            var edit = event.target.closest && event.target.closest('.history-note-text');
            if (!edit) return;
            var row = edit.closest('.history-note');
            if (!row) return;
            var note = edit.dataset.note || '';
            row.innerHTML = '<input class="history-note-input" maxlength="200" value="' + NetwatchShared.escapeHtml(note) + '" placeholder="' + NetwatchShared.escapeHtml(i18n('speed_history_note_placeholder')) + '">';
            var input = row.querySelector('.history-note-input');
            input.focus();
            input.addEventListener('blur', function () { saveHistoryNoteRow(row); }, { once: true });
            input.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); input.blur(); } });
        });
        async function saveHistoryNoteRow(row) {
            var input = row.querySelector('.history-note-input');
            if (!input || row.dataset.saving) return;
            row.dataset.saving = '1';
            try {
                await saveSpeedHistoryNote(row.dataset.kind, row.dataset.id, input ? input.value.trim() : '');
                await loadSpeedHistory();
            } catch (error) {
                if (input) input.setCustomValidity(error.message || i18n('save_failed'));
                if (input) input.reportValidity();
                delete row.dataset.saving;
            }
        }
    });
}

function formatMeasuredMS(value, measured) {
    return measured && Number.isFinite(Number(value)) && Number(value) >= 0 ? Math.round(Number(value)) + ' ms' : '--';
}

function resetTransferDetails() {
}

function renderTransferDetails(stats) {
    stats = stats || {};
    var downloadMB = Number(stats.download_mb) || 0;
    var uploadMB = Number(stats.upload_mb) || 0;
    var measured = Number(stats.rtt_avg_ms) > 0;
    window.__app.setText(els.transferLatency, formatMeasuredMS(stats.rtt_avg_ms, measured));
    window.__app.setText(els.transferJitter, formatMeasuredMS(stats.jitter_ms, measured));
}

function resetBroadbandMetrics() {
    els.broadbandNote.textContent = i18n('standby');
    window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Idle', i18n('standby'));
    window.__app.setSpeedPanelMode('broadband', 'Idle');
    els.broadbandDownload.textContent = '--';
    els.broadbandUpload.textContent = '--';
    els.broadbandLatency.textContent = '--';
    els.broadbandJitter.textContent = '--';
}

function resetPortPolicyMetrics() {
    var fields = [
        els.broadbandPortPolicyLowPort, els.broadbandPortPolicyHighPort,
        els.broadbandPortPolicyLowDownload, els.broadbandPortPolicyLowUpload,
        els.broadbandPortPolicyLowLatency, els.broadbandPortPolicyLowJitter,
        els.broadbandPortPolicyHighDownload, els.broadbandPortPolicyHighUpload,
        els.broadbandPortPolicyHighLatency, els.broadbandPortPolicyHighJitter,
        els.broadbandPortPolicyDownloadDelta, els.broadbandPortPolicyUploadDelta
    ];
    fields.forEach(function (element) { if (element) element.textContent = '--'; });
    if (els.broadbandPortPolicyTarget) els.broadbandPortPolicyTarget.textContent = 'ispeedtest.com.cn';
    if (els.broadbandPortPolicyProtocol) els.broadbandPortPolicyProtocol.textContent = 'HTTP';
    if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = i18n('broadband_port_policy_ready');
    if (els.broadbandPortPolicyVerdict) {
        els.broadbandPortPolicyVerdict.textContent = i18n('broadband_port_policy_ready');
        els.broadbandPortPolicyVerdict.className = 'port-policy-verdict';
    }
}

function portPolicySpeed(value) {
    return Number.isFinite(Number(value)) && Number(value) >= 0 ? Number(value) : null;
}

function portPolicyLatency(value) {
    return Number.isFinite(Number(value)) && Number(value) >= 0 ? Math.round(Number(value)) + ' ms' : '--';
}

function portPolicyDelta(high, low) {
    high = portPolicySpeed(high);
    low = portPolicySpeed(low);
    if (high === null || low === null || low <= 0) return '--';
    var percent = (high - low) / low * 100;
    return (percent > 0 ? '+' : '') + percent.toFixed(1) + '%';
}

function renderPortPolicyResults(results) {
    results = results || {};
    var low = results.low || {};
    var high = results.high || {};
    var value = function (object, camel, snake) {
        return object[camel] !== undefined && object[camel] !== null ? object[camel] : object[snake];
    };
    var lowDownload = value(low, 'downloadMbps', 'download_mbps');
    var lowUpload = value(low, 'uploadMbps', 'upload_mbps');
    var lowLatency = value(low, 'latencyMs', 'latency_ms');
    var lowJitter = value(low, 'jitterMs', 'jitter_ms');
    var highDownload = value(high, 'downloadMbps', 'download_mbps');
    var highUpload = value(high, 'uploadMbps', 'upload_mbps');
    var highLatency = value(high, 'latencyMs', 'latency_ms');
    var highJitter = value(high, 'jitterMs', 'jitter_ms');
    if (els.broadbandPortPolicyLowPort && low.port) els.broadbandPortPolicyLowPort.textContent = ':' + low.port;
    if (els.broadbandPortPolicyHighPort && high.port) els.broadbandPortPolicyHighPort.textContent = ':' + high.port;
    if (els.broadbandPortPolicyLowDownload) els.broadbandPortPolicyLowDownload.textContent = portPolicySpeed(lowDownload) === null ? '--' : portPolicySpeed(lowDownload).toFixed(2);
    if (els.broadbandPortPolicyLowUpload) els.broadbandPortPolicyLowUpload.textContent = portPolicySpeed(lowUpload) === null ? '--' : portPolicySpeed(lowUpload).toFixed(2);
    if (els.broadbandPortPolicyLowLatency) els.broadbandPortPolicyLowLatency.textContent = portPolicyLatency(lowLatency);
    if (els.broadbandPortPolicyLowJitter) els.broadbandPortPolicyLowJitter.textContent = portPolicyLatency(lowJitter);
    if (els.broadbandPortPolicyHighDownload) els.broadbandPortPolicyHighDownload.textContent = portPolicySpeed(highDownload) === null ? '--' : portPolicySpeed(highDownload).toFixed(2);
    if (els.broadbandPortPolicyHighUpload) els.broadbandPortPolicyHighUpload.textContent = portPolicySpeed(highUpload) === null ? '--' : portPolicySpeed(highUpload).toFixed(2);
    if (els.broadbandPortPolicyHighLatency) els.broadbandPortPolicyHighLatency.textContent = portPolicyLatency(highLatency);
    if (els.broadbandPortPolicyHighJitter) els.broadbandPortPolicyHighJitter.textContent = portPolicyLatency(highJitter);
    if (els.broadbandPortPolicyDownloadDelta) els.broadbandPortPolicyDownloadDelta.textContent = portPolicyDelta(highDownload, lowDownload);
    if (els.broadbandPortPolicyUploadDelta) els.broadbandPortPolicyUploadDelta.textContent = portPolicyDelta(highUpload, lowUpload);
}

function renderPortPolicyVerdict(results, final) {
    results = results || {};
    var low = results.low || {};
    var high = results.high || {};
    var verdict = i18n('broadband_port_policy_running');
    var className = 'port-policy-verdict';
    if (final) {
        if (high.error && !high.ok) {
            verdict = i18n('broadband_port_policy_high_failed');
            className += ' error';
        } else if (low.error && !low.ok) {
            verdict = i18n('broadband_port_policy_low_failed');
            className += ' error';
        } else {
            var field = function (object, camel, snake) {
                return object[camel] !== undefined && object[camel] !== null ? object[camel] : object[snake];
            };
            var lowDownload = portPolicySpeed(field(low, 'downloadMbps', 'download_mbps'));
            var highDownload = portPolicySpeed(field(high, 'downloadMbps', 'download_mbps'));
            var lowUpload = portPolicySpeed(field(low, 'uploadMbps', 'upload_mbps'));
            var highUpload = portPolicySpeed(field(high, 'uploadMbps', 'upload_mbps'));
            var downloadDrop = lowDownload > 0 && highDownload !== null ? (highDownload - lowDownload) / lowDownload : 0;
            var uploadDrop = lowUpload > 0 && highUpload !== null ? (highUpload - lowUpload) / lowUpload : 0;
            if (downloadDrop <= -0.3 || uploadDrop <= -0.3) {
                verdict = i18n('broadband_port_policy_likely_limited');
                className += ' warn';
            } else {
                verdict = i18n('broadband_port_policy_no_obvious_limit');
                className += ' ok';
            }
        }
    }
    if (els.broadbandPortPolicyVerdict) {
        els.broadbandPortPolicyVerdict.textContent = verdict;
        els.broadbandPortPolicyVerdict.className = className;
    }
}

function finishPortPolicyTest() {
    stopBroadbandPortPolicyPolling();
    if (state.runningTest === 'broadband') state.runningTest = null;
    window.__app.updateWindowControls();
}

function portPolicyTaskResults(task) {
    var results = {};
    (task && Array.isArray(task.targets) ? task.targets : []).forEach(function (target) {
        if (target && target.id) results[target.id] = target;
    });
    return results;
}

function renderBroadbandPortPolicyTask(task) {
    task = task || {};
    var results = portPolicyTaskResults(task);
    renderPortPolicyResults(results);
    if (els.broadbandPortPolicyTarget && task.host) els.broadbandPortPolicyTarget.textContent = task.host;
    if (els.broadbandPortPolicyProtocol && task.protocol) els.broadbandPortPolicyProtocol.textContent = String(task.protocol).toUpperCase();
    if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = task.message || i18n('broadband_port_policy_running');
    renderPortPolicyVerdict(results, !!task.finished || !!task.error || !!task.canceled);
    if (task.error && els.broadbandPortPolicyVerdict) {
        els.broadbandPortPolicyVerdict.textContent = task.error;
        els.broadbandPortPolicyVerdict.className = 'port-policy-verdict error';
    } else if (task.canceled && els.broadbandPortPolicyVerdict) {
        els.broadbandPortPolicyVerdict.textContent = i18n('speedtest_stopped');
        els.broadbandPortPolicyVerdict.className = 'port-policy-verdict';
    }
}

function stopBroadbandPortPolicyPolling() {
    state.broadbandPortPolicyPollGeneration += 1;
    if (state.broadbandPortPolicyPoller) {
        clearInterval(state.broadbandPortPolicyPoller);
        state.broadbandPortPolicyPoller = null;
    }
    state.broadbandPortPolicyPollInFlight = false;
}

async function pollBroadbandPortPolicyTask(generation) {
    if (generation !== state.broadbandPortPolicyPollGeneration || state.broadbandPortPolicyPollInFlight) return;
    state.broadbandPortPolicyPollInFlight = true;
    try {
        var task = await speedAPIGet('/api/v1/speed/broadband/port-policy/task');
        if (generation !== state.broadbandPortPolicyPollGeneration) return;
        renderBroadbandPortPolicyTask(task);
        if (!task.running) {
            finishPortPolicyTest();
        }
    } catch (error) {
        if (generation !== state.broadbandPortPolicyPollGeneration) return;
        console.error(error);
        if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = error.message || i18n('broadband_start_failed');
    } finally {
        if (generation === state.broadbandPortPolicyPollGeneration) state.broadbandPortPolicyPollInFlight = false;
    }
}

function startBroadbandPortPolicyPolling() {
    stopBroadbandPortPolicyPolling();
    var generation = state.broadbandPortPolicyPollGeneration;
    pollBroadbandPortPolicyTask(generation);
    state.broadbandPortPolicyPoller = setInterval(function () { pollBroadbandPortPolicyTask(generation); }, 500);
}

async function startBroadbandPortPolicyTest() {
    resetPortPolicyMetrics();
    if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = i18n('broadband_port_policy_allocating');
    var task = await speedAPIPost('/api/v1/speed/broadband/port-policy/start');
    renderBroadbandPortPolicyTask(task);
    startBroadbandPortPolicyPolling();
}

function resetTransferMetrics() {
    els.transferNote.textContent = i18n('standby');
    window.__app.setPrimaryStatus(els.transferPrimaryMode, els.transferPrimaryCaption, 'Idle', i18n('standby'));
    window.__app.setSpeedPanelMode('transfer', 'Idle');
    els.transferDownload.textContent = '--';
    els.transferUpload.textContent = '--';
    els.transferLatency.textContent = '--';
    els.transferJitter.textContent = '--';
    resetTransferDetails();
}

function renderBroadbandResult(result, stage, caption) {
    result = result || {};
    var latencyMeasured = Number.isFinite(Number(result.latency_ms)) && Number(result.latency_ms) >= 0;
    els.broadbandLatency.textContent = formatMeasuredMS(result.latency_ms, latencyMeasured);
    els.broadbandJitter.textContent = formatMeasuredMS(result.jitter_ms, latencyMeasured);
    els.broadbandDownload.textContent = window.__app.formatMbps(result.download_mbps);
    els.broadbandUpload.textContent = window.__app.formatMbps(result.upload_mbps);
    var mode = stage === 'latency' ? 'Ping' : (stage === 'download' ? 'Download' : (stage === 'upload' ? 'Upload' : 'Result'));
    window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, mode, caption || i18n('standby'));
    window.__app.setSpeedPanelMode('broadband', mode);
}

function renderBroadbandTask(task) {
    task = task || {};
    els.broadbandNote.textContent = task.message || window.__app.broadbandStageMap[task.stage] || i18n('standby');
    renderBroadbandResult(task.result || {}, task.stage, task.message);
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
        var task = await speedAPIGet('/api/v1/speed/broadband/server/task');
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

function finishClientBroadbandTest() {
    if (state.broadbandWorker) {
        state.broadbandWorker.terminate();
        state.broadbandWorker = null;
    }
    if (state.runningTest === 'broadband') state.runningTest = null;
    window.__app.updateWindowControls();
}

function startClientBroadbandTest(request) {
    if (!window.Worker) throw new Error(i18n('broadband_worker_unsupported'));
    var node = state.broadbandNodes.concat(builtinBroadbandNodes()).find(function (item) { return item.id === request.nodeID; });
    if (!node) throw new Error(i18n('broadband_node_invalid'));
    var result = { test_mode: 'client', node_id: request.nodeID, node_name: node.label, node_category: node.category || 'public', download_mbps: 0, upload_mbps: 0, latency_ms: 0, jitter_ms: 0 };
    var worker = new Worker('/broadband-speedtest-worker.js?r=' + Math.random());
    state.broadbandWorker = worker;
    worker.onmessage = async function (event) {
        if (worker !== state.broadbandWorker) return;
        var data = event.data || {};
        if (data.type === 'progress') {
            var elapsed = Math.max(1, Number(data.elapsedMs) || 1);
            var speed = (Number(data.bytes) || 0) * 8 * 1.06 / (elapsed / 1000) / 1000000;
            if (data.stage === 'download') result.download_mbps = speed;
            if (data.stage === 'upload') result.upload_mbps = speed;
            renderBroadbandResult(result, data.stage, i18n(data.stage === 'download' ? 'downloading' : 'uploading') + ' ' + speed.toFixed(2) + ' Mbit/s');
            return;
        }
        if (data.type === 'state') {
            if (Number.isFinite(Number(data.latencyMs))) result.latency_ms = Math.round(Number(data.latencyMs));
            if (Number.isFinite(Number(data.jitterMs))) result.jitter_ms = Math.round(Number(data.jitterMs));
            if (Number.isFinite(Number(data.downloadMbps))) result.download_mbps = Number(data.downloadMbps);
            if (Number.isFinite(Number(data.uploadMbps))) result.upload_mbps = Number(data.uploadMbps);
            if (data.node) {
                result.node_id = data.node.id || result.node_id;
                result.node_name = data.node.label || result.node_name;
                result.node_category = data.node.category || result.node_category;
            }
            var stageText = data.stage === 'ping' ? i18n('latency_sampling') : (data.stage === 'download' ? i18n('downloading') : (data.stage === 'upload' ? i18n('uploading') : i18n('broadband_client_starting')));
            els.broadbandNote.textContent = stageText;
            renderBroadbandResult(result, data.stage === 'ping' ? 'latency' : data.stage, stageText);
            return;
        }
        if (data.type === 'complete') {
            result = Object.assign(result, data.result || {}, { test_mode: 'client' });
            els.broadbandNote.textContent = i18n('speedtest_complete');
            renderBroadbandResult(result, 'complete', i18n('speedtest_complete'));
            try {
                await speedAPIPost('/api/v1/speed/broadband/client/result', result);
                await loadSpeedHistory();
            } catch (error) {
                console.error('Failed to save client broadband result:', error);
            }
            finishClientBroadbandTest();
            return;
        }
        if (data.type === 'error') {
            var message = data.message || i18n('speedtest_failed');
            els.broadbandNote.textContent = message;
            window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Error', message);
            window.__app.setSpeedPanelMode('broadband', 'Error');
            finishClientBroadbandTest();
        }
    };
    worker.onerror = function () {
        if (worker !== state.broadbandWorker) return;
        els.broadbandNote.textContent = i18n('broadband_start_failed');
        finishClientBroadbandTest();
    };
    worker.postMessage({ type: 'start', nodeId: request.nodeID, node: node, durationSec: state.speedConfig.broadband_duration_sec, downloadStreams: 15, uploadStreams: 5 });
}

async function startBroadbandTest() {
    if (state.runningTest) return;
    state.runningTest = 'broadband';
    state.broadbandFailureShown = false;
    window.__app.updateWindowControls();
    resetBroadbandMetrics();
    els.broadbandNote.textContent = i18n('started_broadband');
    try {
        if (currentBroadbandMode() === 'port-policy') {
            await startBroadbandPortPolicyTest();
            return;
        }
        var request = selectedBroadbandRequest();
        if (currentBroadbandMode() === 'server') {
            var task = await speedAPIPost('/api/v1/speed/broadband/server/start', { node_id: request.nodeID });
            renderBroadbandTask(task);
            startBroadbandPolling();
        } else {
            startClientBroadbandTest(request);
        }
    } catch (error) {
        console.error(error);
        state.runningTest = null;
        window.__app.updateWindowControls();
        if (currentBroadbandMode() === 'port-policy') {
            var message = error && error.message ? error.message : i18n('broadband_start_failed');
            if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = message;
            if (els.broadbandPortPolicyVerdict) {
                els.broadbandPortPolicyVerdict.textContent = message;
                els.broadbandPortPolicyVerdict.className = 'port-policy-verdict error';
            }
        } else {
            els.broadbandNote.textContent = i18n('broadband_start_failed');
        }
    }
}

async function cancelBroadbandTest(showStopped) {
    if (showStopped === undefined) showStopped = true;
    stopBroadbandPolling();
    try {
        if (currentBroadbandMode() === 'port-policy') {
            stopBroadbandPortPolicyPolling();
            await speedAPIPost('/api/v1/speed/broadband/port-policy/cancel');
        } else if (currentBroadbandMode() === 'server') {
            await speedAPIPost('/api/v1/speed/broadband/server/cancel');
        } else if (state.broadbandWorker) {
            state.broadbandWorker.postMessage({ type: 'stop' });
            state.broadbandWorker.terminate();
            state.broadbandWorker = null;
        }
    } catch (error) {
        console.error(error);
    } finally {
        if (state.runningTest === 'broadband') {
            state.runningTest = null;
        }
        window.__app.updateWindowControls();
        if (showStopped) {
            if (currentBroadbandMode() === 'port-policy') {
                if (els.broadbandPortPolicyStatus) els.broadbandPortPolicyStatus.textContent = i18n('speedtest_stopped');
                if (els.broadbandPortPolicyVerdict) {
                    els.broadbandPortPolicyVerdict.textContent = i18n('speedtest_stopped');
                    els.broadbandPortPolicyVerdict.className = 'port-policy-verdict';
                }
            } else {
                els.broadbandNote.textContent = i18n('speedtest_stopped');
                window.__app.setPrimaryStatus(els.broadbandPrimaryMode, els.broadbandPrimaryCaption, 'Stopped', i18n('manual_stop'));
                window.__app.setSpeedPanelMode('broadband', 'Stopped');
            }
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
window.__app.loadBroadbandNodes = loadBroadbandNodes;
window.__app.setBroadbandMode = setBroadbandMode;
window.__app.updateBroadbandControls = updateBroadbandControls;
window.__app.startBroadbandTest = startBroadbandTest;
window.__app.cancelBroadbandTest = cancelBroadbandTest;
window.__app.runTransferTest = runTransferTest;
window.__app.cancelTransferTest = cancelTransferTest;
window.__app.clearSpeedHistory = clearSpeedHistory;
renderBroadbandMode();
bindSpeedHistoryNotes();
})();
