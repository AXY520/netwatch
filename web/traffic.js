(function () {
    
    function trafficGet(path) {
        if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
        return fetch(path, { cache: 'no-store' }).then(async (r) => {
            if (!r.ok) throw new Error(`HTTP ${r.status}`);
            return r.json();
        });
    }
    function trafficPost(path, body) {
        if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
        return fetch(path, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        }).then(async (r) => {
            const data = await r.json().catch(() => ({}));
            if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
            return data;
        });
    }

const state = {
        theme: localStorage.getItem('theme') || 'dark',
        settings: {
            traffic_sampling_enabled: true,
            chart_time_label_interval: 0
        },
        snapshot: null,
        selectedBridge: new URLSearchParams(window.location.search).get('bridge') || '',
        selectedHistory: [],
        topItems: [],
        range: localStorage.getItem('trafficRange') || '1h',
        liveIntervalSec: Number.parseInt(localStorage.getItem('trafficLiveIntervalSec') || '2', 10) || 2,
        topLoadedAt: 0,
        liveTimer: null,
        resizeTimer: null,
        prevCounters: null,
        prevCountersAt: 0
    };

    const validRanges = new Set(['1m', '5m', '15m', '1h', '6h', '24h', 'all']);
    if (!validRanges.has(state.range)) {
        state.range = '1h';
    }

    const els = {
        themeToggle: document.getElementById('theme-toggle'),
        refreshBtn: document.getElementById('traffic-refresh-btn'),
        disabledBanner: document.getElementById('traffic-disabled-banner'),
        note: document.getElementById('traffic-page-note'),
        listStatus: document.getElementById('traffic-list-status'),
        appList: document.getElementById('traffic-app-list'),
        search: document.getElementById('traffic-search'),
        sort: document.getElementById('traffic-sort'),
        hideIdle: document.getElementById('traffic-hide-idle'),
        rangeControls: document.getElementById('traffic-range-controls'),
        title: document.getElementById('traffic-selected-title'),
        sub: document.getElementById('traffic-selected-sub'),
        summary: document.getElementById('traffic-summary-grid'),
        legend: document.getElementById('traffic-chart-legend'),
        chart: document.getElementById('traffic-chart'),
        topTable: document.getElementById('traffic-top-table'),
        counterTable: document.getElementById('traffic-counter-table'),
        sampleTable: document.getElementById('traffic-sample-table'),
        labelDensity: document.getElementById('traffic-label-density'),
        liveToggle: document.getElementById('traffic-live-refresh'),
        liveInterval: document.getElementById('traffic-live-interval'),
        toast: document.getElementById('toast')
    };

    const i18n = (key) => (typeof window.__ === 'function' ? window.__(key) : key);

    function uploadBytes(item) {
        return Number(item?.upload_bytes ?? item?.rx_bytes) || 0;
    }

    function downloadBytes(item) {
        return Number(item?.download_bytes ?? item?.tx_bytes) || 0;
    }

    function setPageNote(text) {
        // #traffic-page-note was removed from header; keep optional for future restore.
        if (els.note) els.note.textContent = text || '';
    }

    function setText(el, text) {
        if (el) el.textContent = text ?? '';
    }

    function setHTML(el, html) {
        if (el) el.innerHTML = html ?? '';
    }

    function initTheme() {
        NetwatchShared.initTheme(state, els.themeToggle);
    }

    function showToast(message, type) {
        NetwatchShared.showToast(message, type);
    }

    function escapeHtml(value) {
        return NetwatchShared.escapeHtml(value);
    }

    function formatBytes(n) {
        return NetwatchShared.formatBytes(n);
    }

    function formatSpeed(n) {
        return `${formatBytes(n)}/s`;
    }

    function formatSeconds(value) {
        return `${value} ${i18n('seconds_short')}`;
    }

    function niceAxisMax(value) {
        const raw = Number(value) || 0;
        if (raw <= 1) return 1;
        const exponent = Math.floor(Math.log10(raw));
        const base = Math.pow(10, exponent);
        const normalized = raw / base;
        const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
        return step * base;
    }

    function appTitle(item) {
        return item?.app_title || item?.domain || i18n('unknown_app');
    }

    function appSubtitle(item) {
        const parts = [];
        if (item?.status_text) parts.push(item.status_text);
        if (item?.domain) parts.push(item.domain);
        return parts.join(' · ');
    }

    function parseTimestamp(ts) {
        const m = String(ts || '').match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})$/);
        if (!m) return 0;
        return new Date(
            Number(m[1]),
            Number(m[2]) - 1,
            Number(m[3]),
            Number(m[4]),
            Number(m[5]),
            Number(m[6])
        ).getTime();
    }

    function rangeMs() {
        return {
            '1m': 60 * 1000,
            '5m': 5 * 60 * 1000,
            '15m': 15 * 60 * 1000,
            '1h': 60 * 60 * 1000,
            '6h': 6 * 60 * 60 * 1000,
            '24h': 24 * 60 * 60 * 1000
        }[state.range] || 0;
    }

    function historyURL(bridge) {
        const params = new URLSearchParams({
            bridge,
            limit: '1440'
        });
        if (state.range !== 'all') {
            params.set('range', state.range);
        }
        return `/api/v1/network/app-traffic/history?${params.toString()}`;
    }

    function liveURL(bridge) {
        const params = new URLSearchParams({
            bridge,
            limit: '1440'
        });
        if (state.range !== 'all') {
            params.set('range', state.range);
        }
        return `/api/v1/network/app-traffic/live?${params.toString()}`;
    }

    function filteredHistory(points) {
        const list = Array.isArray(points) ? points.filter(p => parseTimestamp(p.timestamp) > 0) : [];
        if (state.range === 'all' || list.length < 2) return list;
        const end = parseTimestamp(list[list.length - 1].timestamp);
        const cutoff = end - rangeMs();
        const firstInside = list.findIndex(p => parseTimestamp(p.timestamp) >= cutoff);
        if (firstInside <= 0) return list.filter(p => parseTimestamp(p.timestamp) >= cutoff);
        return list.slice(firstInside - 1);
    }

    function activeRangeWindow(points) {
        if (state.range === 'all' || !Array.isArray(points) || points.length === 0) return null;
        const end = parseTimestamp(points[points.length - 1].timestamp);
        const ms = rangeMs();
        if (!end || !ms) return null;
        return { start: end - ms, end };
    }

    function overlapSeconds(rate, windowRange) {
        if (!windowRange) return rate.seconds;
        const start = Math.max(rate.start, windowRange.start);
        const end = Math.min(rate.end, windowRange.end);
        return Math.max(0, (end - start) / 1000);
    }

    function computeRates(points) {
        const rates = [];
        for (let i = 1; i < points.length; i++) {
            const prev = points[i - 1];
            const curr = points[i];
            const t1 = parseTimestamp(prev.timestamp);
            const t2 = parseTimestamp(curr.timestamp);
            const seconds = Math.max(1, (t2 - t1) / 1000);
            const previousRx = uploadBytes(prev);
            const previousTx = downloadBytes(prev);
            const currentRx = uploadBytes(curr);
            const currentTx = downloadBytes(curr);
            if (curr.discontinuity || currentRx < previousRx || currentTx < previousTx) continue;
            const rxDelta = currentRx - previousRx;
            const txDelta = currentTx - previousTx;
            rates.push({
                timestamp: curr.timestamp || '',
                start: t1,
                end: t2,
                time: t2,
                seconds,
                rxDelta,
                txDelta,
                rxRate: rxDelta / seconds,
                txRate: txDelta / seconds,
                totalRate: (rxDelta + txDelta) / seconds
            });
        }
        return rates;
    }

    function summarize(points, current) {
        const rates = computeRates(points);
        const windowRange = activeRangeWindow(points);
        const scopedRates = [];
        let rxDelta = 0;
        let txDelta = 0;
        let seconds = 0;
        rates.forEach((item) => {
            const overlap = overlapSeconds(item, windowRange);
            if (overlap <= 0) return;
            const ratio = overlap / item.seconds;
            rxDelta += item.rxDelta * ratio;
            txDelta += item.txDelta * ratio;
            seconds += overlap;
            scopedRates.push(item);
        });
        const totalDelta = rxDelta + txDelta;
        const sampleSeconds = rates.reduce((sum, item) => sum + item.seconds, 0);
        const latest = rates[rates.length - 1];
        const peak = scopedRates.reduce((max, item) => Math.max(max, item.totalRate), 0);
        return {
            rates,
            totalDelta,
            rxDelta,
            txDelta,
            seconds,
            sampleSeconds,
            latestRate: latest ? latest.totalRate : 0,
            avgRate: seconds > 0 ? totalDelta / seconds : 0,
            peakRate: peak,
            currentTotal: uploadBytes(current) + downloadBytes(current)
        };
    }

    function renderSummary(summary) {
        if (!els.summary) return;
        els.summary.innerHTML = [
            [i18n('current_rate'), formatSpeed(summary.latestRate)],
            [i18n('interval_avg'), formatSpeed(summary.avgRate)],
            [i18n('interval_peak'), formatSpeed(summary.peakRate)],
            [i18n('interval_total'), formatBytes(summary.totalDelta)]
        ].map(([label, value]) => `
            <div class="traffic-stat-card">
                <div class="traffic-stat-label">${label}</div>
                <div class="traffic-stat-value">${value}</div>
            </div>
        `).join('');
    }

    function renderSingleLegend() {
        if (!els.legend) return;
        els.legend.innerHTML = `
            <span><span class="legend-rx"></span>${i18n('app_traffic_upload')}</span>
            <span><span class="legend-tx"></span>${i18n('app_traffic_download')}</span>
            <span><span class="legend-total"></span>${i18n('total')}</span>
        `;
    }

    function renderChart(scopedIn, summaryIn) {
        renderSingleLegend();
        if (!els.chart) return;
        const scoped = scopedIn || filteredHistory(state.selectedHistory);
        const rates = computeRates(scoped);
        if (rates.length === 0) {
            els.chart.innerHTML = '<div class="placeholder" style="padding:2rem">' + i18n('data_insufficient') + '</div>';
            return;
        }

        const W = Math.max(320, Math.round(els.chart.clientWidth || 860));
        const H = Math.max(360, Math.min(520, Math.round(W * 0.46)));
        const pad = { top: 18, right: 18, bottom: 54, left: 70 };
        const cw = Math.max(1, W - pad.left - pad.right);
        const ch = Math.max(1, H - pad.top - pad.bottom);
        const values = rates.flatMap(d => [d.rxRate, d.txRate, d.totalRate]).sort((a, b) => a - b);
        const maxRaw = Math.max(1, values[values.length - 1] || 1);
        const latest = rates[rates.length - 1];
        const maxVal = niceAxisMax(maxRaw * 1.08);
        const xS = (i) => pad.left + (i / Math.max(1, rates.length - 1)) * cw;
        const yS = (v) => pad.top + ch - (Math.min(v, maxVal) / maxVal) * ch;
        const line = (key) => rates.map((d, i) => `${xS(i).toFixed(1)},${yS(d[key]).toFixed(1)}`).join(' ');
        const summaryForAnomaly = summaryIn || summarize(scoped, selectedApp());
        const anomalies = detectAnomalies(scoped, summaryForAnomaly);
        const anomalyByTimestamp = new Map(anomalies.map(item => [item.timestamp, item]));

        let svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" style="width:100%;height:auto">`;
        svg += `<rect x="0" y="0" width="${W}" height="${H}" fill="transparent"/>`;

        for (let i = 0; i <= 4; i++) {
            const y = pad.top + (ch / 4) * i;
            const val = maxVal * (1 - i / 4);
            svg += `<line x1="${pad.left}" y1="${y.toFixed(1)}" x2="${(pad.left + cw).toFixed(1)}" y2="${y.toFixed(1)}" stroke="rgba(128,128,128,0.18)" stroke-width="1"/>`;
            svg += `<text x="${pad.left - 8}" y="${(y + 4).toFixed(1)}" fill="#888" font-size="11" text-anchor="end">${formatSpeed(val)}</text>`;
        }

        const configured = Number.parseInt(state.settings.chart_time_label_interval, 10) || 0;
        const minLabelSpacing = W < 680 ? 86 : 72;
        const byWidth = Math.max(1, Math.ceil(rates.length / Math.max(1, Math.floor(cw / minLabelSpacing))));
        const fallback = Math.max(1, Math.ceil(rates.length / 6));
        const step = Math.max(configured > 0 ? configured : fallback, byWidth);
        for (let i = 0; i < rates.length; i += step) {
            const x = xS(i);
            const short = rates[i].timestamp.length > 10 ? rates[i].timestamp.slice(11, 16) : rates[i].timestamp;
            svg += `<line x1="${x.toFixed(1)}" y1="${pad.top}" x2="${x.toFixed(1)}" y2="${(pad.top + ch).toFixed(1)}" stroke="rgba(128,128,128,0.08)" stroke-width="1"/>`;
            svg += `<text x="${x.toFixed(1)}" y="${H - 18}" fill="#888" font-size="11" text-anchor="end" transform="rotate(-35 ${x.toFixed(1)} ${H - 18})">${escapeHtml(short)}</text>`;
        }

        svg += `<polyline points="${line('totalRate')}" fill="none" stroke="#ffb347" stroke-width="2" opacity="0.85"/>`;
        svg += `<polyline points="${line('rxRate')}" fill="none" stroke="#2196F3" stroke-width="2.4"/>`;
        svg += `<polyline points="${line('txRate')}" fill="none" stroke="#4CAF50" stroke-width="2.4"/>`;

        rates.forEach((rate, i) => {
            const x = xS(i);
            const y = yS(rate.totalRate);
            const tip = `${rate.timestamp}\n${i18n('app_traffic_upload')} ${formatSpeed(rate.rxRate)}\n${i18n('app_traffic_download')} ${formatSpeed(rate.txRate)}\n${i18n('total')} ${formatSpeed(rate.totalRate)}`;
            svg += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="8" fill="transparent"><title>${escapeHtml(tip)}</title></circle>`;
            const anomaly = anomalyByTimestamp.get(rate.timestamp);
            if (anomaly) {
                svg += `<path d="M ${x.toFixed(1)} ${(y - 7).toFixed(1)} l 6 6 l -6 6 l -6 -6 z" fill="#ff4d4d"><title>${escapeHtml(`${anomaly.type} ${anomaly.value}`)}</title></path>`;
            }
        });
        if (latest) {
            const x = xS(rates.length - 1);
            const y = yS(latest.totalRate);
            svg += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="4.5" fill="#ffb347" stroke="var(--card-bg)" stroke-width="2"/>`;
            svg += `<text x="${Math.max(pad.left, x - 8).toFixed(1)}" y="${Math.max(pad.top + 12, y - 10).toFixed(1)}" fill="#ffb347" font-size="11" text-anchor="end">${i18n('latest')} ${formatSpeed(latest.totalRate)}</text>`;
        }
        svg += `</svg>`;
        els.chart.innerHTML = svg;
    }

    function renderTables(current, scoped, summary) {
        const f = (n) => (Number(n) || 0).toLocaleString();
        if (els.counterTable) els.counterTable.innerHTML = `
            <tbody>
                <tr><th>${i18n('app_traffic_upload')}</th><td>${formatBytes(uploadBytes(current))}</td></tr>
                <tr><th>${i18n('app_traffic_download')}</th><td>${formatBytes(downloadBytes(current))}</td></tr>
                <tr><th>${i18n('rx_packets')}</th><td>${f(current?.rx_packets)}</td></tr>
                <tr><th>${i18n('tx_packets')}</th><td>${f(current?.tx_packets)}</td></tr>
                <tr><th>${i18n('rx_dropped')}</th><td>${f(current?.rx_dropped)}</td></tr>
                <tr><th>${i18n('tx_dropped')}</th><td>${f(current?.tx_dropped)}</td></tr>
                <tr><th>${i18n('containers')}</th><td>${f(current?.running_count)} / ${f(current?.container_count)}</td></tr>
            </tbody>
        `;

        const first = scoped[0]?.timestamp || '-';
        const last = scoped[scoped.length - 1]?.timestamp || '-';
        const avgInterval = summary.rates.length > 0
            ? formatSeconds(Math.round((summary.sampleSeconds || summary.seconds) / summary.rates.length))
            : '-';
        if (els.sampleTable) els.sampleTable.innerHTML = `
            <tbody>
                <tr><th>${i18n('samples')}</th><td>${scoped.length}</td></tr>
                <tr><th>${i18n('avg_interval')}</th><td>${avgInterval}</td></tr>
                <tr><th>${i18n('first_sample')}</th><td>${escapeHtml(first)}</td></tr>
                <tr><th>${i18n('last_sample')}</th><td>${escapeHtml(last)}</td></tr>
                <tr><th>${i18n('app_traffic_upload')}</th><td>${formatBytes(summary.rxDelta)}</td></tr>
                <tr><th>${i18n('app_traffic_download')}</th><td>${formatBytes(summary.txDelta)}</td></tr>
                <tr><th>${i18n('current_total')}</th><td>${formatBytes(summary.currentTotal)}</td></tr>
            </tbody>
        `;
    }

    function detectAnomalies(points, summary) {
        const scoped = filteredHistory(points);
        const rates = summary.rates || computeRates(scoped);
        const anomalies = [];

        for (let i = 1; i < scoped.length; i++) {
            const prev = scoped[i - 1];
            const curr = scoped[i];
            const t1 = parseTimestamp(prev.timestamp);
            const t2 = parseTimestamp(curr.timestamp);
            const seconds = Math.max(1, (t2 - t1) / 1000);
            if (curr.discontinuity || uploadBytes(curr) < uploadBytes(prev) || downloadBytes(curr) < downloadBytes(prev)) {
                const reason = curr.discontinuity_reason === 'service_restart' ? i18n('service_restart') : i18n('counter_reset');
                anomalies.push({ timestamp: curr.timestamp, type: reason, value: '-' });
            }
            if (summary.rates.length > 1 && seconds > Math.max(180, ((summary.sampleSeconds || summary.seconds) / summary.rates.length) * 3)) {
                anomalies.push({ timestamp: curr.timestamp, type: i18n('sample_gap'), value: formatSeconds(Math.round(seconds)) });
            }
        }

        const avg = summary.avgRate || 0;
        rates.forEach((rate) => {
            if (avg > 0 && rate.totalRate >= Math.max(avg * 4, 1024 * 1024)) {
                anomalies.push({ timestamp: rate.timestamp, type: i18n('rate_spike'), value: formatSpeed(rate.totalRate) });
            }
        });
        return anomalies.slice(0, 8);
    }

    function topRows() {
        const apps = (state.snapshot?.bridges || []).filter(b => !isNetwatchBridge(b));
        var appMap = {};
        for (var i = 0; i < apps.length; i++) {
            appMap[apps[i].bridge] = apps[i];
        }
        return (state.topItems || []).filter(function (item) {
            return !!appMap[item.bridge];
        }).map(function (item) {
            var app = appMap[item.bridge];
            return {
                title: appTitle(app),
                delta: item.total_delta || 0,
                peak: item.peak_bps || 0
            };
        }).slice(0, 5);
    }

    function renderTopTable() {
        if (!els.topTable) return;
        const rows = topRows();
        if (rows.length === 0) {
            els.topTable.innerHTML = `<tbody><tr><td class="placeholder">${i18n('no_ranking_data')}</td><td></td></tr></tbody>`;
            return;
        }
        els.topTable.innerHTML = `<tbody>${rows.map((row, index) => `
            <tr>
                <th>${index + 1}. ${escapeHtml(row.title)}<div class="traffic-table-sub">${i18n('peak')} ${formatSpeed(row.peak)}</div></th>
                <td>${formatBytes(row.delta)}</td>
            </tr>
        `).join('')}</tbody>`;
    }

    function isNetwatchBridge(b) {
        return NetwatchShared.isNetwatchBridge(b);
    }

    function filteredApps() {
        const q = String(els.search?.value || '').trim().toLowerCase();
        const hideIdle = !!els.hideIdle?.checked;
        const sort = els.sort?.value || 'total-desc';
        const list = [...(state.snapshot?.bridges || [])].filter((item) => {
            if (isNetwatchBridge(item)) return false;
            const total = uploadBytes(item) + downloadBytes(item);
            const haystack = `${appTitle(item)} ${appSubtitle(item)}`.toLowerCase();
            if (hideIdle && total === 0) return false;
            return q === '' || haystack.includes(q);
        });
        list.sort((a, b) => {
            if (sort === 'name-asc') return appTitle(a).localeCompare(appTitle(b), 'zh-CN');
            if (sort === 'rx-desc') return uploadBytes(b) - uploadBytes(a);
            if (sort === 'tx-desc') return downloadBytes(b) - downloadBytes(a);
            return (uploadBytes(b) + downloadBytes(b)) - (uploadBytes(a) + downloadBytes(a));
        });
        return list;
    }

    function liveRateForBridge(bridge, item) {
        const prev = state.prevCounters && state.prevCounters[bridge];
        if (!prev || !state.prevCountersAt || !item) return '';
        const dt = (Date.now() - state.prevCountersAt) / 1000;
        if (!(dt > 0.4)) return '';
        const rx = Math.max(0, uploadBytes(item) - (prev.rx || 0));
        const tx = Math.max(0, downloadBytes(item) - (prev.tx || 0));
        const bps = (rx + tx) / dt;
        if (!(bps > 0)) return '0 B/s';
        return formatSpeed(bps);
    }

    function commitSnapshot(next) {
        // Promote current counters to "previous" so list can show live rate.
        if (state.snapshot && Array.isArray(state.snapshot.bridges)) {
            const map = {};
            state.snapshot.bridges.forEach((b) => {
                if (!b || !b.bridge) return;
                map[b.bridge] = { rx: uploadBytes(b), tx: downloadBytes(b) };
            });
            state.prevCounters = map;
            state.prevCountersAt = state._snapshotAt || Date.now();
        }
        state.snapshot = next || null;
        state._snapshotAt = Date.now();
    }

    function setSamplingBanner(enabled) {
        if (!els.disabledBanner) return;
        els.disabledBanner.hidden = !!enabled;
    }

    function renderAppList() {
        const samplingEnabled = state.settings.traffic_sampling_enabled !== false;
        const sidebar = document.querySelector('.traffic-sidebar');
        if (sidebar) {
            sidebar.classList.toggle('sampling-disabled', !samplingEnabled);
        }
        setSamplingBanner(samplingEnabled);
        // When sampling is disabled, hide the app list section
        if (!samplingEnabled) {
            if (els.appList) els.appList.innerHTML = `<div class="placeholder">${i18n('traffic_sampling_disabled')}<br><small>${i18n('traffic_open_settings_hint')}</small></div>`;
            setText(els.listStatus, '');
            return;
        }
        const list = filteredApps();
        setText(els.listStatus, state.snapshot?.generated_at || '');
        if (!state.selectedBridge && list.length > 0) {
            state.selectedBridge = list[0].bridge;
        }
        if (!els.appList) return;
        if (list.length === 0) {
            els.appList.innerHTML = `<div class="placeholder">${i18n('no_app_data')}</div>`;
            return;
        }
        els.appList.innerHTML = list.map((item) => {
            const title = appTitle(item);
            const sub = appSubtitle(item);
            const total = uploadBytes(item) + downloadBytes(item);
            const rate = liveRateForBridge(item.bridge, item);
            const icon = item.icon ? `<img class="app-icon" src="${escapeHtml(item.icon)}" alt="" loading="lazy" onerror="this.style.display='none'">` : '';
            const subHtml = sub ? `<div class="traffic-app-sub">${escapeHtml(sub)}</div>` : '';
            const rateHtml = rate ? `<div class="traffic-app-rate">${escapeHtml(i18n('traffic_live_rate') + ' ' + rate)}</div>` : '';
            const detailURL = `/app-detail.html?bridge=${encodeURIComponent(item.bridge)}`;
            return `
                <div class="traffic-app-item ${item.bridge === state.selectedBridge ? 'active' : ''}" data-bridge="${escapeHtml(item.bridge)}" role="button" tabindex="0">
                    <div class="traffic-app-icon-wrap">${icon}</div>
                    <div class="traffic-app-title"><strong>${escapeHtml(title)}</strong>${subHtml}${rateHtml}</div>
                    <div class="traffic-app-total">${formatBytes(total)}</div>
                    <a class="traffic-app-detail-link" href="${detailURL}" title="应用网络详情" aria-label="查看 ${escapeHtml(title)} 的网络详情">›</a>
                </div>
            `;
        }).join('');
    }


    async function loadSettings() {
        try {
            const data = await trafficGet('/api/v1/settings');
            state.settings = { ...data, traffic_sampling_enabled: data.traffic_sampling_enabled !== false };
            if (els.labelDensity) {
                els.labelDensity.value = String(state.settings.chart_time_label_interval || 0);
                window.syncCustomSelect?.(els.labelDensity);
            }
        } catch (_) {}
    }

    async function saveTrafficPageSettings() {
        const payload = {
            ...state.settings,
            chart_time_label_interval: Number.parseInt(els.labelDensity?.value || '0', 10) || 0
        };
        const saved = await trafficPost('/api/v1/settings', payload);
        state.settings = { ...state.settings, ...saved };
    }

    async function loadSnapshot() {
        const next = await trafficGet('/api/v1/network/app-traffic');
        commitSnapshot(next);
        if (state.snapshot?.note) {
            setPageNote(state.snapshot.note);
        } else if (!state.settings.traffic_sampling_enabled) {
            setPageNote(i18n('traffic_sampling_disabled'));
        } else {
            setPageNote(`${i18n('sampled_at')} ${state.snapshot?.generated_at || '-'}`);
        }
    }

    async function loadHistory(bridge) {
        if (!bridge) {
            state.selectedHistory = [];
            return;
        }
        state.selectedHistory = await trafficGet(historyURL(bridge));
    }

    async function loadTop() {
        const params = new URLSearchParams({ limit: '5' });
        if (state.range !== 'all') {
            params.set('range', state.range);
        }
        state.topItems = await trafficGet(`/api/v1/network/app-traffic/top?${params.toString()}`);
        state.topLoadedAt = Date.now();
    }

    async function maybeLoadTop() {
        if (Date.now() - state.topLoadedAt < 30000) return;
        await loadTop();
    }

    function mergeLiveBridge(liveBridge) {
        if (!liveBridge?.bridge || !state.snapshot?.bridges) return;
        const idx = state.snapshot.bridges.findIndex(item => item.bridge === liveBridge.bridge);
        if (idx < 0) return;
        state.snapshot.bridges[idx] = {
            ...state.snapshot.bridges[idx],
            ...liveBridge
        };
    }

    async function loadLiveSample() {
        if (!state.selectedBridge) return;
        const data = await trafficPost(liveURL(state.selectedBridge));
        mergeLiveBridge(data.bridge);
        state.selectedHistory = Array.isArray(data.history) ? data.history : [];
    }

    function selectedApp() {
        return (state.snapshot?.bridges || []).find(item => item.bridge === state.selectedBridge) || null;
    }

    function renderSelected() {
        const current = selectedApp();
        if (!current) {
            setText(els.title, i18n('app_traffic'));
            setText(els.sub, i18n('no_data'));
            setHTML(els.summary, '');
            setHTML(els.chart, '<div class="placeholder" style="padding:2rem">' + i18n('no_app_data') + '</div>');
            setHTML(els.legend, '');
            setHTML(els.topTable, '');
            setHTML(els.counterTable, '');
            setHTML(els.sampleTable, '');
            setPageNote('');
            return;
        }

        const scoped = filteredHistory(state.selectedHistory);
        const summary = summarize(scoped, current);
        const firstSample = scoped.length > 0 ? scoped[0].timestamp || '' : '';
        setText(els.title, appTitle(current));
        // Primary subtitle is app meta; sampling status rides on the same line as secondary note.
        let sub = appSubtitle(current) || i18n('app_traffic_title');
        if (firstSample) {
            setPageNote(`${i18n('sampling_since')} ${firstSample}`);
        } else if (!state.settings.traffic_sampling_enabled) {
            setPageNote(i18n('traffic_sampling_disabled'));
            sub = i18n('traffic_sampling_disabled');
        } else {
            setPageNote(i18n('waiting_for_first_sample'));
            if (!scoped.length) sub = i18n('waiting_for_first_sample');
        }
        setText(els.sub, sub);
        renderSummary(summary);
        renderChart(scoped, summary);
        renderTopTable();
        renderTables(current, scoped, summary);
        requestAnimationFrame(syncSidebarBottom);
    }

    async function refreshAll() {
        if (els.refreshBtn) els.refreshBtn.disabled = true;
        setText(els.listStatus, i18n('loading'));
        try {
            await loadSettings();
            await loadSnapshot();
            const apps = (state.snapshot?.bridges || []).filter(item => !isNetwatchBridge(item));
            if (!state.selectedBridge && apps.length > 0) {
                state.selectedBridge = apps[0].bridge;
            }
            // If URL/query bridge is gone, fall back to first app
            if (state.selectedBridge && !apps.some(a => a.bridge === state.selectedBridge) && apps.length > 0) {
                state.selectedBridge = apps[0].bridge;
            }
            await Promise.all([
                loadHistory(state.selectedBridge),
                loadTop()
            ]);
            renderAppList();
            renderSelected();
        } catch (e) {
            console.error('[traffic] refreshAll failed', e);
            setText(els.listStatus, i18n('load_failed'));
            showToast(i18n('load_failed') + ': ' + (e && e.message ? e.message : e), 'error');
        } finally {
            if (els.refreshBtn) els.refreshBtn.disabled = false;
        }
    }

    async function selectBridge(bridge) {
        if (!bridge || bridge === state.selectedBridge) return;
        state.selectedBridge = bridge;
        const url = new URL(window.location.href);
        url.searchParams.set('bridge', bridge);
        window.history.replaceState(null, '', url);
        renderAppList();
        if (els.sub) els.sub.textContent = i18n('loading');
        try {
            await loadHistory(bridge);
            renderSelected();
            if (els.liveToggle?.checked && !document.hidden) {
                refreshSelectedOnly();
            }
        } catch (e) {
            showToast(i18n('load_failed') + ': ' + e.message, 'error');
        }
    }

    async function refreshSelectedOnly() {
        if (!state.selectedBridge) return;
        try {
            await loadLiveSample();
            try {
                await maybeLoadTop();
            } catch (_) {}
            renderAppList();
            renderSelected();
        } catch (e) {
            showToast(i18n('refresh_failed') + ': ' + e.message, 'error');
        }
    }

    async function refreshSelectedRange() {
        if (!state.selectedBridge) return;
        if (els.sub) els.sub.textContent = i18n('loading');
        try {
            const historyTask = els.liveToggle?.checked && !document.hidden
                ? loadLiveSample()
                : loadHistory(state.selectedBridge);
            await Promise.all([historyTask, loadTop()]);
            renderAppList();
            renderSelected();
        } catch (e) {
            showToast(i18n('refresh_failed') + ': ' + e.message, 'error');
        }
    }

    function stopLiveRefresh() {
        if (state.liveTimer) {
            clearInterval(state.liveTimer);
            state.liveTimer = null;
        }
    }

    function startLiveRefresh() {
        stopLiveRefresh();
        const intervalSec = Math.max(1, Number.parseInt(els.liveInterval?.value || String(state.liveIntervalSec), 10) || 2);
        state.liveIntervalSec = intervalSec;
        localStorage.setItem('trafficLiveIntervalSec', String(intervalSec));
        state.liveTimer = setInterval(refreshSelectedOnly, intervalSec * 1000);
        refreshSelectedOnly();
    }

    function syncSidebarBottom() {
        const sidebar = document.querySelector('.traffic-sidebar');
        const main = document.querySelector('.traffic-main');
        if (!sidebar || !main) return;
        if (window.matchMedia('(max-width: 900px)').matches) {
            sidebar.style.height = '';
            return;
        }
        sidebar.style.height = `${Math.ceil(main.getBoundingClientRect().height)}px`;
    }

    function bindEvents() {
        els.refreshBtn?.addEventListener('click', refreshAll);
        els.search?.addEventListener('input', renderAppList);
        els.sort?.addEventListener('change', renderAppList);
        els.hideIdle?.addEventListener('change', renderAppList);
        els.appList?.addEventListener('click', (e) => {
            if (e.target.closest('.traffic-app-detail-link')) return;
            const btn = e.target.closest('.traffic-app-item');
            if (btn) selectBridge(btn.dataset.bridge);
        });
        els.appList?.addEventListener('keydown', (e) => {
            if (e.key !== 'Enter' && e.key !== ' ') return;
            const btn = e.target.closest('.traffic-app-item');
            if (btn) {
                e.preventDefault();
                selectBridge(btn.dataset.bridge);
            }
        });
        els.rangeControls?.addEventListener('click', (e) => {
            const btn = e.target.closest('button[data-range]');
            if (!btn) return;
            state.range = btn.dataset.range;
            state._cachedScoped = null;
            state._cachedSummary = null;
            localStorage.setItem('trafficRange', state.range);
            initRangeButtons();
            refreshSelectedRange();
        });
        els.labelDensity?.addEventListener('change', async () => {
            state.settings.chart_time_label_interval = Number.parseInt(els.labelDensity.value || '0', 10) || 0;
            renderSelected();
            try {
                await saveTrafficPageSettings();
            } catch (e) {
                showToast(i18n('label_density_save_failed') + ': ' + e.message, 'error');
            }
        });
        els.liveToggle?.addEventListener('change', () => {
            if (els.liveToggle.checked) {
                startLiveRefresh();
            } else {
                stopLiveRefresh();
            }
        });
        els.liveInterval?.addEventListener('change', () => {
            state.liveIntervalSec = Math.max(1, Number.parseInt(els.liveInterval.value || '2', 10) || 2);
            localStorage.setItem('trafficLiveIntervalSec', String(state.liveIntervalSec));
            if (els.liveToggle?.checked && !document.hidden) {
                refreshSelectedOnly();
            }
        });
        window.addEventListener('resize', () => {
            clearTimeout(state.resizeTimer);
            state.resizeTimer = setTimeout(() => {
                state._cachedScoped = null;
                state._cachedSummary = null;
                renderSelected();
                syncSidebarBottom();
            }, 160);
        });
        document.addEventListener('visibilitychange', () => {
            if (document.hidden) {
                stopLiveRefresh();
                return;
            }
            if (els.liveToggle?.checked) {
                startLiveRefresh();
            }
        });
        window.addEventListener('pagehide', stopLiveRefresh);
        window.addEventListener('beforeunload', stopLiveRefresh);
    }

    function initRangeButtons() {
        els.rangeControls?.querySelectorAll('button[data-range]').forEach(item => {
            item.classList.toggle('active', item.dataset.range === state.range);
        });
    }

    function initLiveControls() {
        if (els.liveInterval) {
            els.liveInterval.value = String(state.liveIntervalSec);
            window.syncCustomSelect?.(els.liveInterval);
        }
    }

    initTheme();
    NetwatchShared.initLazycatFullscreen?.();
    initRangeButtons();
    initLiveControls();
    bindEvents();
    refreshAll();
})();
