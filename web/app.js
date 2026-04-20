const state = {
            theme: localStorage.getItem('theme') || 'dark',
            refreshInterval: 10,
            lastRefreshTime: Date.now(),
            timerInterval: null,
            summary: null,
            egressData: null,
            traceResult: null,
            tracePoller: null,
            fastRefreshing: false,
            refreshing: false,
            speedConfig: {
                broadband_duration_sec: 10,
                local_transfer_duration_sec: 10,
                local_transfer_payload_mb: 32
            },
            settings: {
                refresh_interval_sec: 10,
                auto_refresh_enabled: true,
                nic_realtime_enabled: true,
                nic_realtime_interval_sec: 1
            },
            activeWindow: null,
            runningTest: null,
            broadbandPoller: null,
            transferAbortController: null,
            nicRealtimeInterval: null,
            sse: null,
            initialized: false,
            settingsInitialized: false,
            egressInitialized: false,
            nicRealtimeInitialized: false,
            autoRefreshInitialized: false,
            traceInitialized: false,
            controlsBound: false
        };

        const elements = {
            themeToggle: document.getElementById('theme-toggle'),
            lastUpdate: document.getElementById('last-update'),
            timerBar: document.getElementById('timer-bar'),
            refreshBtn: document.getElementById('refresh-btn'),
            overlay: document.getElementById('loading-overlay'),
            websiteRefreshBtn: document.getElementById('website-refresh-btn'),
            websiteStatus: document.getElementById('website-status'),
            networkStatus: document.getElementById('network-status'),
            domesticTable: document.querySelector('#domestic-table tbody'),
            globalTable: document.querySelector('#global-table tbody'),
            interfacesTable: document.querySelector('#interfaces-table tbody'),
            valHostname: document.getElementById('val-hostname'),
            valIPv4: document.getElementById('val-ipv4'),
            valIPv4Region: document.getElementById('val-ipv4-region'),
            valIPv6: document.getElementById('val-ipv6'),
            valIPv6Region: document.getElementById('val-ipv6-region'),
            valGw4: document.getElementById('val-gw4'),
            valGw6: document.getElementById('val-gw6'),
            backdrop: document.getElementById('window-backdrop'),
            traceBackdrop: document.getElementById('trace-window-backdrop'),
            openSettingsWindow: document.getElementById('open-settings-window'),
            openBroadbandWindow: document.getElementById('open-broadband-window'),
            openTransferWindow: document.getElementById('open-transfer-window'),
            closeSettingsWindow: document.getElementById('close-settings-window'),
            closeBroadbandWindow: document.getElementById('close-broadband-window'),
            closeTransferWindow: document.getElementById('close-transfer-window'),
            closeTraceWindow: document.getElementById('close-trace-window'),
            settingsWindow: document.getElementById('settings-window'),
            broadbandWindow: document.getElementById('broadband-window'),
            transferWindow: document.getElementById('transfer-window'),
            traceWindow: document.getElementById('trace-window'),
            saveSettings: document.getElementById('save-settings'),
            settingAutoRefreshEnabled: document.getElementById('setting-auto-refresh-enabled'),
            settingRefreshIntervalSec: document.getElementById('setting-refresh-interval-sec'),
            settingNICRealtimeEnabled: document.getElementById('setting-nic-realtime-enabled'),
            settingNICRealtimeIntervalSec: document.getElementById('setting-nic-realtime-interval-sec'),
            broadbandNote: document.getElementById('broadband-note'),
            transferNote: document.getElementById('transfer-note'),
            runBroadbandTest: document.getElementById('run-broadband-test'),
            runTransferTest: document.getElementById('run-transfer-test'),
            broadbandPrimaryMode: document.getElementById('broadband-primary-mode'),
            broadbandPrimarySpeed: document.getElementById('broadband-primary-speed'),
            broadbandPrimaryUnit: document.getElementById('broadband-primary-unit'),
            broadbandPrimaryCaption: document.getElementById('broadband-primary-caption'),
            broadbandStage: document.getElementById('broadband-stage'),
            broadbandProgress: document.getElementById('broadband-progress'),
            broadbandDownload: document.getElementById('broadband-download'),
            broadbandUpload: document.getElementById('broadband-upload'),
            broadbandLatency: document.getElementById('broadband-latency'),
            broadbandJitter: document.getElementById('broadband-jitter'),
            transferPrimaryMode: document.getElementById('transfer-primary-mode'),
            transferPrimarySpeed: document.getElementById('transfer-primary-speed'),
            transferPrimaryUnit: document.getElementById('transfer-primary-unit'),
            transferPrimaryCaption: document.getElementById('transfer-primary-caption'),
            transferStage: document.getElementById('transfer-stage'),
            transferProgress: document.getElementById('transfer-progress'),
            transferDownload: document.getElementById('transfer-download'),
            transferUpload: document.getElementById('transfer-upload'),
            transferLatency: document.getElementById('transfer-latency'),
            transferJitter: document.getElementById('transfer-jitter'),
            broadbandHistory: document.getElementById('broadband-history'),
            transferHistory: document.getElementById('transfer-history')
        };

function initTheme() {
            if (state.themeInitialized) return;
            state.themeInitialized = true;
            document.documentElement.setAttribute('data-theme', state.theme);
            elements.themeToggle.addEventListener('click', () => {
                state.theme = state.theme === 'dark' ? 'light' : 'dark';
                document.documentElement.setAttribute('data-theme', state.theme);
                localStorage.setItem('theme', state.theme);
            });
        }

        let debounceTimers = {};
        function debounce(key, fn, ms = 300) {
            if (debounceTimers[key]) clearTimeout(debounceTimers[key]);
            debounceTimers[key] = setTimeout(fn, ms);
        }

        function showToast(message, type = 'info', ms = 3000) {
            const toast = document.getElementById('toast');
            toast.textContent = message;
            toast.className = 'toast show ' + type;
            setTimeout(() => toast.classList.remove('show'), ms);
        }

        const statusMap = { ok: '正常', down: '故障', degraded: '降级', unknown: '未知' };
        const natLabelMap = {
            full_cone: '全锥形',
            restricted_cone: '受限锥形',
            port_restricted_cone: '端口受限锥形',
            symmetric: '对称型',
            unknown: '未知'
        };
        const broadbandStageMap = {
            starting: '准备中',
            latency: '延迟采样',
            download: '下载测速',
            upload: '上传测速',
            finalizing: '整理结果',
            complete: '已完成',
            canceled: '已停止',
            error: '失败'
        };

        function getIconUrl(name) {
            const lowerName = String(name || '').toLowerCase();
            const localIcons = ['baidu', 'bilibili', 'github', 'youtube'];
            return localIcons.includes(lowerName) ? `/icons/${lowerName}.ico` : '/icons/default.ico';
        }

        function formatRegion(region) {
            if (!region) return '未知';
            const parts = [region.country, region.region, region.city].filter(Boolean);
            const place = parts.join(' / ');
            if (region.isp) return place ? `${place} (${region.isp})` : region.isp;
            return place || '未知';
        }

        function formatMbps(value) {
            return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)}` : '--';
        }

        function getStatusClass(status) {
            if (status === 'ok') return 'status-ok';
            if (status === 'down') return 'status-down';
            return 'status-warn';
        }

        function isAbortError(error) {
            return error?.name === 'AbortError';
        }

        function formatMS(value) {
            return Number.isFinite(value) && value > 0 ? `${Math.round(value)} ms` : '--';
        }

        function gaugeMax(value, unit) {
            if (unit === 'ms') {
                if (!Number.isFinite(value) || value <= 0) return 200;
                if (value <= 50) return 50;
                if (value <= 100) return 100;
                if (value <= 200) return 200;
                return 500;
            }
            if (!Number.isFinite(value) || value <= 0) return 1000;
            if (value <= 100) return 100;
            if (value <= 200) return 200;
            if (value <= 500) return 500;
            if (value <= 1000) return 1000;
            if (value <= 2000) return 2000;
            return 5000;
        }

        function setPrimaryGauge(ringEl, modeEl, speedEl, unitEl, captionEl, value, unit, mode, caption) {
            const max = gaugeMax(value, unit);
            const safeValue = Number.isFinite(value) && value > 0 ? value : 0;
            const ratio = Math.max(0, Math.min(1, safeValue / max));
            if (ringEl) ringEl.style.setProperty('--dial-progress', `${ratio * 100}%`);
            if (modeEl) {
                modeEl.textContent = mode;
                modeEl.classList.remove('active', 'done', 'error');
                if (mode === 'Download' || mode === 'Upload' || mode === 'Ping') modeEl.classList.add('active');
                else if (mode === 'Result') modeEl.classList.add('done');
                else if (mode === 'Stopped') modeEl.classList.add('error');
            }
            if (speedEl) speedEl.textContent = Number.isFinite(value) && value > 0 ? value.toFixed(unit === 'ms' ? 0 : 1) : '--';
            if (unitEl) unitEl.textContent = unit.toUpperCase();
            if (captionEl) captionEl.textContent = caption || '';

            const scope = ringEl && ringEl.id && ringEl.id.startsWith('broadband') ? 'broadband' : 'transfer';
            const dlPanel = document.getElementById(`${scope}-panel-download`);
            const upPanel = document.getElementById(`${scope}-panel-upload`);
            if (dlPanel && upPanel) {
                dlPanel.classList.toggle('active', mode === 'Download');
                upPanel.classList.toggle('active', mode === 'Upload');
            }

            if (unit === 'Mbps' && (mode === 'Download' || mode === 'Upload')) {
                const barId = `${scope}-${mode.toLowerCase()}-bar`;
                const bar = document.getElementById(barId);
                if (bar) bar.style.setProperty('--fill', `${ratio * 100}%`);
            }
        }

        function createSpeedSampler() {
            return {
                startedAt: performance.now(),
                lastAt: performance.now(),
                lastBytes: 0,
                samples: []
            };
        }

        function observeSpeedSampler(sampler, totalBytes) {
            const now = performance.now();
            const elapsedMs = now - sampler.lastAt;
            if (elapsedMs > 0) {
                const deltaBytes = Math.max(0, totalBytes - sampler.lastBytes);
                const instantMbps = (deltaBytes * 8) / (elapsedMs / 1000) / 1_000_000;
                if (now - sampler.startedAt >= 2000 && instantMbps > 0) {
                    sampler.samples.push(instantMbps);
                    if (sampler.samples.length > 80) {
                        sampler.samples = sampler.samples.slice(-80);
                    }
                }
            }
            sampler.lastAt = now;
            sampler.lastBytes = totalBytes;

            const visible = sampler.samples.slice(-6);
            if (visible.length > 0) {
                return visible.reduce((sum, value) => sum + value, 0) / visible.length;
            }
            const totalElapsedSec = Math.max((now - sampler.startedAt) / 1000, 0.001);
            return (totalBytes * 8) / totalElapsedSec / 1_000_000;
        }

        function stableSpeedFromSampler(sampler, totalBytes) {
            observeSpeedSampler(sampler, totalBytes);
            if (sampler.samples.length === 0) {
                const totalElapsedSec = Math.max((performance.now() - sampler.startedAt) / 1000, 0.001);
                return (totalBytes * 8) / totalElapsedSec / 1_000_000;
            }
            const sorted = [...sampler.samples].sort((a, b) => a - b);
            const cut = Math.floor(sorted.length * 0.2);
            const trimmed = cut * 2 < sorted.length ? sorted.slice(cut, sorted.length - cut) : sorted;
            return trimmed.reduce((sum, value) => sum + value, 0) / Math.max(trimmed.length, 1);
        }

        function renderPlaceholderTable(tbody, message) {
            tbody.innerHTML = `<tr><td colspan="3" class="placeholder">${message}</td></tr>`;
        }

        function updateConnectivityTable(tbody, items) {
            if (!Array.isArray(items) || items.length === 0) {
                renderPlaceholderTable(tbody, '暂无检测结果');
                return;
            }

            tbody.innerHTML = items.map(item => {
                return `
                <tr>
                    <td>
                        <div class="target-info">
                            <img class="site-icon" src="${getIconUrl(item.name)}" onerror="this.src='/icons/default.ico'">
                            <span>${item.name}</span>
                        </div>
                    </td>
                    <td><span class="nat-badge ${getStatusClass(item.status)}">${statusMap[item.status] || '未知'}</span></td>
                    <td class="latency ${item.latency_ms > 200 ? 'high' : (item.latency_ms === 0 ? 'down' : '')}">
                        ${item.latency_ms > 0 ? `${item.latency_ms} ms` : '连接失败'}
                    </td>
                </tr>`;
            }).join('');
        }

        function renderNetworkInfo(networkInfo = {}) {
            elements.valIPv4.textContent = networkInfo.egress_ipv4 || '无公网 IPv4';
            elements.valIPv4Region.textContent = formatRegion(networkInfo.egress_ipv4_region);
            elements.valIPv6.textContent = networkInfo.egress_ipv6 || '无公网 IPv6';
            elements.valIPv6Region.textContent = formatRegion(networkInfo.egress_ipv6_region);
            elements.valGw4.textContent = networkInfo.default_ipv4?.gateway || '未知';
            elements.valGw6.textContent = networkInfo.default_ipv6?.gateway || '未知';

            const interfaces = Array.isArray(networkInfo.interfaces) ? networkInfo.interfaces : [];
            elements.interfacesTable.innerHTML = interfaces.map(iface => `
                <tr>
                    <td>${iface.name || '---'}${iface.label ? `<br><small style="color:var(--text-muted)">${iface.label}</small>` : ''}</td>
                    <td>${iface.hardware_addr || '---'}</td>
                    <td>${iface.mtu || '---'}</td>
                    <td>${Array.isArray(iface.ipv4) && iface.ipv4.length ? iface.ipv4.join('<br>') : '---'}</td>
                    <td>${Array.isArray(iface.ipv6) && iface.ipv6.length ? iface.ipv6.join('<br>') : '---'}</td>
                </tr>
            `).join('') || '<tr><td colspan="5" class="placeholder">未找到目标网卡</td></tr>';
        }

        function renderNAT() { /* NAT card removed */ }

        function detectProxyState() {
            const lookups = state.egressData?.lookups || [];
            const dom = lookups.find(l => l.scope === 'domestic' && l.ip);
            const glb = lookups.find(l => l.scope === 'global' && l.ip);
            if (!dom || !glb) {
                return {
                    mode: 'unknown',
                    label: '代理环境状态暂不可判定'
                };
            }

            const hasProxy = dom.ip !== glb.ip;

            return {
                mode: hasProxy ? 'proxy' : 'direct',
                label: hasProxy ? '检测到代理环境' : '未检测到代理环境'
            };
        }

        function renderProxyBanner() {
            const inlineEl = document.getElementById('proxy-inline-status');
            const s = detectProxyState();
            if (inlineEl) inlineEl.textContent = s.label;
        }

        function refreshProxyDisplay() {
            renderProxyBanner();
            if (state.summary?.website_connectivity) {
                updateConnectivityTable(elements.domesticTable, state.summary.website_connectivity.domestic || []);
                updateConnectivityTable(elements.globalTable, state.summary.website_connectivity.global || []);
            }
        }

        function renderSummary(summary) {
            state.summary = summary;
            state.refreshInterval = summary.refresh_interval_sec || 10;
            state.settings.refresh_interval_sec = state.refreshInterval;
            state.lastRefreshTime = Date.now();
            if (elements.websiteStatus) elements.websiteStatus.textContent = '';
            if (elements.networkStatus) elements.networkStatus.textContent = '';

            updateConnectivityTable(elements.domesticTable, summary.website_connectivity?.domestic || []);
            updateConnectivityTable(elements.globalTable, summary.website_connectivity?.global || []);
            renderNetworkInfo(summary.network_info || {});
            refreshProxyDisplay();
        }

        function startTimer() { /* timer bar removed; auto-refresh handled by backend toggle */ }

        async function loadSummary(showOverlay = false) {
            if (showOverlay) elements.overlay.style.display = 'flex';
            try {
                const response = await fetch('/api/v1/summary', { cache: 'no-store' });
                if (!response.ok) {
                    throw new Error('HTTP ' + response.status);
                }
                const data = await response.json();
                renderSummary(data);
            } catch (error) {
                console.error(error);
                elements.lastUpdate && (elements.lastUpdate.textContent = '连接服务器失败: ' + error.message);
                showToast('加载数据失败: ' + error.message, 'error');
            } finally {
                if (showOverlay) elements.overlay.style.display = 'none';
            }
        }

        async function runFastRefresh(showOverlay = true) {
            if (state.fastRefreshing) return;
            state.fastRefreshing = true;
            elements.refreshBtn.disabled = true;
            if (showOverlay) elements.overlay.style.display = 'flex';
            try {
                const response = await fetch('/api/v1/probe/run', { method: 'POST' });
                const data = await response.json();
                renderSummary(data);
            } catch (error) {
                console.error(error);
                showToast('刷新失败', 'error');
            } finally {
                state.fastRefreshing = false;
                if (showOverlay) elements.overlay.style.display = 'none';
                elements.refreshBtn.disabled = false;
            }
        }

        async function runWebsiteRefresh() {
            elements.websiteRefreshBtn.disabled = true;
            elements.websiteStatus.textContent = '检测中...';
            try {
                const response = await fetch('/api/v1/connectivity/websites/run', { method: 'POST' });
                const websiteData = await response.json();
                updateConnectivityTable(elements.domesticTable, websiteData.domestic || []);
                updateConnectivityTable(elements.globalTable, websiteData.global || []);
                elements.websiteStatus.textContent = '';
                if (state.summary) {
                    state.summary.website_connectivity = websiteData;
                }
            } catch (error) {
                console.error(error);
                elements.websiteStatus.textContent = '检测失败';
                showToast('网站检测失败', 'error');
            } finally {
                elements.websiteRefreshBtn.disabled = false;
            }
        }

        async function runNATRefresh() { /* NAT card removed */ }

        async function loadSpeedConfig() {
            try {
                const response = await fetch('/api/v1/speed/config', { cache: 'no-store' });
                const data = await response.json();
                state.speedConfig = {
                    broadband_duration_sec: data.broadband_duration_sec || 10,
                    local_transfer_duration_sec: data.local_transfer_duration_sec || 10,
                    local_transfer_payload_mb: data.local_transfer_payload_mb || 32
                };
            } catch (error) {
                console.error(error);
            }

            elements.broadbandNote.textContent =
                `下行使用国内多家镜像（清华/中科大/华为云/南大/网易 等）并发测试，上行走 Cloudflare（回程通常经 HK），每阶段 ${state.speedConfig.broadband_duration_sec} 秒。`;
            elements.transferNote.textContent =
                `浏览器与本机服务间并发下载/上传 ${state.speedConfig.local_transfer_duration_sec} 秒，实时显示速率。`;
        }

        async function loadSpeedHistory() {
            try {
                const [broadband, localTransfer] = await Promise.all([
                    fetch('/api/v1/speed/broadband/history', { cache: 'no-store' }).then(r => r.json()),
                    fetch('/api/v1/speed/local/history', { cache: 'no-store' }).then(r => r.json())
                ]);
                renderBroadbandHistory(Array.isArray(broadband) ? broadband : []);
                renderTransferHistory(Array.isArray(localTransfer) ? localTransfer : []);
            } catch (error) {
                console.error(error);
            }
        }

        function renderBroadbandHistory(items) {
            elements.broadbandHistory.innerHTML = items.map(item => `
                <div class="history-item">
                    <div class="history-item-info">
                        <span class="history-item-value">${item.download_mbps?.toFixed?.(2) || '0.00'} / ${item.upload_mbps?.toFixed?.(2) || '0.00'} <small>Mbps</small></span>
                        <small>${item.timestamp || '--'}${item.provider ? ' · ' + item.provider : ''}</small>
                    </div>
                    <div style="text-align: right">
                        <small>延迟 ${item.latency_ms || 0} ms</small><br>
                        <small>抖动 ${item.jitter_ms || 0} ms</small>
                    </div>
                </div>
            `).join('') || '<div class="history-item"><small>暂无记录</small></div>';
        }

        function renderTransferHistory(items) {
            elements.transferHistory.innerHTML = items.map(item => `
                <div class="history-item">
                    <div class="history-item-info">
                        <span class="history-item-value">${item.download_mbps?.toFixed?.(2) || '0.00'} / ${item.upload_mbps?.toFixed?.(2) || '0.00'} <small>Mbps</small></span>
                        <small>${item.timestamp || '--'}</small>
                    </div>
                    <div style="text-align: right">
                        <small>延迟 ${item.round_trip_latency_ms || 0} ms</small><br>
                        <small>抖动 ${item.jitter_ms || 0} ms</small>
                    </div>
                </div>
            `).join('') || '<div class="history-item"><small>暂无记录</small></div>';
        }

        function resetBroadbandMetrics() {
            elements.broadbandStage.textContent = '待启动';
            elements.broadbandProgress.textContent = '0%';
            elements.broadbandNote.textContent = `下行使用国内多家镜像并发测试，上行走 Cloudflare，每阶段 ${state.speedConfig.broadband_duration_sec} 秒。`;
            setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, 0, 'Mbps', 'Idle', '等待测速开始');
            elements.broadbandDownload.textContent = '--';
            elements.broadbandUpload.textContent = '--';
            elements.broadbandLatency.textContent = '--';
            elements.broadbandJitter.textContent = '--';
        }

        function resetTransferMetrics() {
            elements.transferStage.textContent = '待启动';
            elements.transferProgress.textContent = '0%';
            elements.transferNote.textContent = `浏览器与本机服务间并发下载/上传 ${state.speedConfig.local_transfer_duration_sec} 秒。`;
            setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, 0, 'Mbps', 'Idle', '等待测速开始');
            elements.transferDownload.textContent = '--';
            elements.transferUpload.textContent = '--';
            elements.transferLatency.textContent = '--';
            elements.transferJitter.textContent = '--';
        }

        function renderBroadbandTask(task = {}) {
            elements.broadbandStage.textContent = broadbandStageMap[task.stage] || '待启动';
            elements.broadbandProgress.textContent = `${Math.max(0, Math.min(100, Math.round(task.progress_percent || 0)))}%`;
            elements.broadbandNote.textContent = broadbandStageMap[task.stage] || '等待测速开始';
            elements.broadbandLatency.textContent = formatMS(task.result?.latency_ms);
            elements.broadbandJitter.textContent = formatMS(task.result?.jitter_ms);
            elements.broadbandDownload.textContent = formatMbps(task.result?.download_mbps);
            elements.broadbandUpload.textContent = formatMbps(task.result?.upload_mbps);

            if (task.stage === 'latency') {
                setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, task.result?.latency_ms || 0, 'ms', 'Ping', task.message || '延迟采样中');
                return;
            }
            if (task.stage === 'download') {
                setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, task.result?.download_mbps || 0, 'Mbps', 'Download', task.message || '下载测速中');
                return;
            }
            if (task.stage === 'upload') {
                setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, task.result?.upload_mbps || 0, 'Mbps', 'Upload', task.message || '上传测速中');
                return;
            }
            setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, task.result?.download_mbps || task.result?.upload_mbps || 0, 'Mbps', 'Result', task.message || '测速完成');
        }

        function updateTransferProgress(stage, progress, message) {
            elements.transferStage.textContent = stage;
            elements.transferProgress.textContent = `${Math.max(0, Math.min(100, Math.round(progress)))}%`;
            elements.transferNote.textContent = stage;
        }

        function stopBroadbandPolling() {
            if (state.broadbandPoller) {
                clearInterval(state.broadbandPoller);
                state.broadbandPoller = null;
            }
        }

        async function pollBroadbandTask() {
            try {
                const response = await fetch('/api/v1/speed/broadband/task', { cache: 'no-store' });
                const task = await response.json();
                renderBroadbandTask(task);

                if (!task.running) {
                    stopBroadbandPolling();
                    if (state.runningTest === 'broadband') {
                        state.runningTest = null;
                    }
                    updateWindowControls();
                    if (task.finished) {
                        await loadSpeedHistory();
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
            updateWindowControls();
            resetBroadbandMetrics();
            elements.broadbandNote.textContent = '正在启动宽带测速任务';

            try {
                const response = await fetch('/api/v1/speed/broadband/start', { method: 'POST' });
                const task = await response.json();
                renderBroadbandTask(task);
                startBroadbandPolling();
            } catch (error) {
                console.error(error);
                state.runningTest = null;
                updateWindowControls();
                elements.broadbandNote.textContent = '宽带测速启动失败';
            }
        }

        async function cancelBroadbandTest(showStopped = true) {
            stopBroadbandPolling();
            try {
                await fetch('/api/v1/speed/broadband/cancel', { method: 'POST' });
            } catch (error) {
                console.error(error);
            } finally {
                if (state.runningTest === 'broadband') {
                    state.runningTest = null;
                }
                updateWindowControls();
                if (showStopped) {
                    elements.broadbandStage.textContent = '已停止';
                    elements.broadbandNote.textContent = '宽带测速已停止';
                    elements.broadbandProgress.textContent = '0%';
                    setPrimaryGauge(null, elements.broadbandPrimaryMode, elements.broadbandPrimarySpeed, elements.broadbandPrimaryUnit, elements.broadbandPrimaryCaption, 0, 'Mbps', 'Stopped', '测速已手动停止');
                }
            }
        }

        async function measureAveragePing(signal, samples = 8) {
            const raw = [];
            for (let i = 0; i < samples; i += 1) {
                const start = performance.now();
                try {
                    await fetch(`/api/v1/speed/local/ping?ts=${Date.now()}-${i}`, { cache: 'no-store', signal });
                } catch (e) {
                    if (signal?.aborted) throw e;
                    continue;
                }
                raw.push(performance.now() - start);
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, raw[raw.length - 1], 'ms', 'Ping', `延迟采样 ${i + 1}/${samples}`);
                updateTransferProgress('延迟采样', 5 + ((i + 1) / samples) * 10, `正在测量往返延迟 ${i + 1}/${samples}`);
            }
            // 丢掉首个样本（含 TCP 握手开销），用后面的作为 RTT
            const values = raw.length > 1 ? raw.slice(1) : raw;
            if (values.length === 0) return { latency: 0, jitter: 0 };
            const avg = values.reduce((s, v) => s + v, 0) / values.length;
            let jitter = 0;
            if (values.length > 1) {
                let diffSum = 0;
                for (let i = 1; i < values.length; i += 1) {
                    diffSum += Math.abs(values[i] - values[i - 1]);
                }
                jitter = diffSum / (values.length - 1);
            }
            return {
                latency: Math.round(avg),
                jitter: Math.round(jitter)
            };
        }

        async function runParallelDownload(durationSec, signal) {
            const workerCount = 4;
            const deadline = performance.now() + durationSec * 1000;
            const shared = { bytes: 0 };
            const sampler = createSpeedSampler();

            const progressTimer = setInterval(() => {
                const mbps = observeSpeedSampler(sampler, shared.bytes);
                const phaseRatio = Math.min(1, (performance.now() - sampler.startedAt) / (durationSec * 1000));
                elements.transferDownload.textContent = formatMbps(mbps);
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, mbps, 'Mbps', 'Download', `下载阶段 ${Math.round(phaseRatio * 100)}%`);
                updateTransferProgress('下载测速', 15 + phaseRatio * 40, `浏览器到本机下载测速中 ${mbps.toFixed(2)} Mbps`);
            }, 150);

            const worker = async () => {
                while (performance.now() < deadline - 500) {
                    if (signal?.aborted) return;
                    try {
                        const remaining = Math.max(2, Math.ceil((deadline - performance.now()) / 1000) + 2);
                        const response = await fetch(`/api/v1/speed/local/download?sec=${remaining}&ts=${Date.now()}`, {
                            cache: 'no-store',
                            signal
                        });
                        const reader = response.body.getReader();
                        try {
                            while (performance.now() < deadline) {
                                const { done, value } = await reader.read();
                                if (done) break;
                                shared.bytes += value.byteLength;
                            }
                        } finally {
                            try { await reader.cancel(); } catch (_) {}
                        }
                    } catch (e) {
                        if (signal?.aborted) return;
                        // 网络中断 / 连接被服务端关闭：短暂等待后重开连接
                        await new Promise(r => setTimeout(r, 150));
                    }
                }
            };

            try {
                await Promise.all(Array.from({ length: workerCount }, worker));
            } finally {
                clearInterval(progressTimer);
            }

            return stableSpeedFromSampler(sampler, shared.bytes);
        }

        async function runParallelUpload(durationSec, signal) {
            const workerCount = 4;
            const chunkBytes = 512 * 1024; // 512 KB：采样频率更高
            const payload = new Uint8Array(chunkBytes);
            const deadline = performance.now() + durationSec * 1000;
            const shared = { bytes: 0 };
            const sampler = createSpeedSampler();

            const progressTimer = setInterval(() => {
                const mbps = observeSpeedSampler(sampler, shared.bytes);
                const phaseRatio = Math.min(1, (performance.now() - sampler.startedAt) / (durationSec * 1000));
                elements.transferUpload.textContent = formatMbps(mbps);
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, mbps, 'Mbps', 'Upload', `上传阶段 ${Math.round(phaseRatio * 100)}%`);
                updateTransferProgress('上传测速', 55 + phaseRatio * 45, `浏览器到本机上传测速中 ${mbps.toFixed(2)} Mbps`);
            }, 150);

            const worker = async () => {
                while (performance.now() < deadline) {
                    if (signal?.aborted) return;
                    try {
                        await fetch(`/api/v1/speed/local/upload?ts=${Date.now()}`, {
                            method: 'POST',
                            body: payload,
                            headers: { 'Content-Type': 'application/octet-stream' },
                            cache: 'no-store',
                            signal
                        });
                        shared.bytes += chunkBytes;
                    } catch (e) {
                        if (signal?.aborted) return;
                        await new Promise(r => setTimeout(r, 150));
                    }
                }
            };

            try {
                await Promise.all(Array.from({ length: workerCount }, worker));
            } finally {
                clearInterval(progressTimer);
            }

            return stableSpeedFromSampler(sampler, shared.bytes);
        }

        async function runTransferTest() {
            if (state.runningTest) return;
            state.runningTest = 'transfer';
            state.transferAbortController = new AbortController();
            updateWindowControls();
            resetTransferMetrics();

            const { signal } = state.transferAbortController;
            const durationSec = state.speedConfig.local_transfer_duration_sec;

            try {
                updateTransferProgress('准备中', 0, '正在启动网页到本机传输测速');
                const pingStats = await measureAveragePing(signal, 8);
                elements.transferLatency.textContent = formatMS(pingStats.latency);
                elements.transferJitter.textContent = formatMS(pingStats.jitter);
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, pingStats.latency, 'ms', 'Ping', '延迟采样完成');

                const downloadMbps = await runParallelDownload(durationSec, signal);
                elements.transferDownload.textContent = formatMbps(downloadMbps);

                const uploadMbps = await runParallelUpload(durationSec, signal);
                elements.transferUpload.textContent = formatMbps(uploadMbps);

                updateTransferProgress('已完成', 100, '网页到本机传输测速完成');
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, downloadMbps, 'Mbps', 'Result', '测速完成');

                await fetch('/api/v1/speed/local/result', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        download_mbps: downloadMbps,
                        upload_mbps: uploadMbps,
                        round_trip_latency_ms: pingStats.latency,
                        jitter_ms: pingStats.jitter
                    })
                });

                await loadSpeedHistory();
            } catch (error) {
                if (!isAbortError(error)) {
                    console.error(error);
                    updateTransferProgress('失败', 0, '网页到本机传输测速失败');
                }
            } finally {
                state.transferAbortController = null;
                if (state.runningTest === 'transfer') {
                    state.runningTest = null;
                }
                updateWindowControls();
            }
        }

        function cancelTransferTest(showStopped = true) {
            if (state.transferAbortController) {
                state.transferAbortController.abort();
                state.transferAbortController = null;
            }
            if (state.runningTest === 'transfer') {
                state.runningTest = null;
            }
            updateWindowControls();
            if (showStopped) {
                elements.transferStage.textContent = '已停止';
                elements.transferNote.textContent = '网页到本机传输测速已停止';
                elements.transferProgress.textContent = '0%';
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, 0, 'Mbps', 'Stopped', '测速已手动停止');
            }
        }

        async function openWindow(name) {
            if (state.runningTest && state.runningTest !== name) return;

            elements.settingsWindow.classList.remove('active');
            elements.broadbandWindow.classList.remove('active');
            elements.transferWindow.classList.remove('active');

            if (name === 'settings') {
                elements.settingsWindow.classList.add('active');
            } else if (name === 'broadband') {
                elements.broadbandWindow.classList.add('active');
            } else if (name === 'transfer') {
                elements.transferWindow.classList.add('active');
            }

            elements.backdrop.classList.add('active');
            state.activeWindow = name;
            updateWindowControls();
            await loadSpeedHistory();
        }

        function closeCurrentWindow() {
            if (state.runningTest === 'broadband') {
                cancelBroadbandTest(true);
            }
            if (state.runningTest === 'transfer') {
                cancelTransferTest(true);
            }

            elements.settingsWindow.classList.remove('active');
            elements.broadbandWindow.classList.remove('active');
            elements.transferWindow.classList.remove('active');
            elements.backdrop.classList.remove('active');
            state.activeWindow = null;
            updateWindowControls();
        }

        function openTraceWindow() {
            elements.traceWindow?.classList.add('active');
            elements.traceBackdrop?.classList.add('active');
        }

        function closeTraceWindow() {
            elements.traceWindow?.classList.remove('active');
            elements.traceBackdrop?.classList.remove('active');
            if (state.tracePoller) {
                clearInterval(state.tracePoller);
                state.tracePoller = null;
            }
        }

        function updateWindowControls() {
            const busy = Boolean(state.runningTest);
            elements.openSettingsWindow.disabled = busy;
            elements.openBroadbandWindow.disabled = busy && state.runningTest !== 'broadband';
            elements.openTransferWindow.disabled = busy && state.runningTest !== 'transfer';
            elements.runBroadbandTest.disabled = busy;
            elements.runTransferTest.disabled = busy;
            if (state.runningTest === 'broadband') {
                elements.runBroadbandTest.disabled = true;
                elements.runTransferTest.disabled = true;
            }
            if (state.runningTest === 'transfer') {
                elements.runBroadbandTest.disabled = true;
                elements.runTransferTest.disabled = true;
            }
        }

        function bindControls() {
            if (state.controlsBound) return;
            state.controlsBound = true;
            elements.refreshBtn.addEventListener('click', () => debounce('refresh', () => runFastRefresh(true)));
            elements.websiteRefreshBtn.addEventListener('click', () => debounce('website', runWebsiteRefresh));
            elements.openSettingsWindow.addEventListener('click', () => openWindow('settings'));
            elements.openBroadbandWindow.addEventListener('click', () => openWindow('broadband'));
            elements.openTransferWindow.addEventListener('click', () => openWindow('transfer'));
            elements.closeSettingsWindow.addEventListener('click', closeCurrentWindow);
            elements.closeBroadbandWindow.addEventListener('click', closeCurrentWindow);
            elements.closeTransferWindow.addEventListener('click', closeCurrentWindow);
            elements.backdrop.addEventListener('click', closeCurrentWindow);
            elements.closeTraceWindow?.addEventListener('click', closeTraceWindow);
            elements.traceBackdrop?.addEventListener('click', closeTraceWindow);
            elements.runBroadbandTest.addEventListener('click', startBroadbandTest);
            elements.runTransferTest.addEventListener('click', runTransferTest);
            elements.saveSettings?.addEventListener('click', saveSettings);
            elements.settingNICRealtimeEnabled?.addEventListener('change', () => {
                state.settings.nic_realtime_enabled = !!elements.settingNICRealtimeEnabled.checked;
                applySettingsToForm();
            });
        }

        function initWithRetry(maxRetries = 3) {
            bindControls();
            resetBroadbandMetrics();
            resetTransferMetrics();
            loadSpeedConfig().then(() => loadSummary(true)).then(() => loadSpeedHistory()).finally(() => {
                startTimer();
                updateWindowControls();
                if (!state.summary || !state.summary.ready) {
                    if (maxRetries > 0) {
                        setTimeout(() => initWithRetry(maxRetries - 1), 2000);
                    }
                }
            });
            initSSE();
            initTrace();
            initAutoRefreshToggle();
            initNICRealtime();
            initEgressLookups();
        }

        function formatBitsPerSec(bytesPerSec) {
            const bps = (bytesPerSec || 0) * 8;
            if (bps < 1e3) return `${bps.toFixed(0)} bps`;
            if (bps < 1e6) return `${(bps / 1e3).toFixed(1)} Kbps`;
            if (bps < 1e9) return `${(bps / 1e6).toFixed(2)} Mbps`;
            return `${(bps / 1e9).toFixed(2)} Gbps`;
        }

        function formatBytes(n) {
            const v = n || 0;
            if (v < 1024) return `${v} B`;
            if (v < 1048576) return `${(v / 1024).toFixed(1)} KB`;
            if (v < 1073741824) return `${(v / 1048576).toFixed(1)} MB`;
            if (v < 1099511627776) return `${(v / 1073741824).toFixed(2)} GB`;
            return `${(v / 1099511627776).toFixed(2)} TB`;
        }

        function renderEgressLookups(data) {
            state.egressData = data;
            const domesticEl = document.getElementById('egress-domestic-list');
            const globalEl = document.getElementById('egress-global-list');
            const statusEl = document.getElementById('egress-status');
            if (!domesticEl || !globalEl) return;

            const itemHTML = (lu) => {
                if (lu.error) {
                    return `
                        <div class="egress-item error">
                            <span class="egress-provider">${lu.provider}</span>
                            <span class="egress-duration">${lu.duration_ms} ms</span>
                            <span class="egress-meta">错误：${lu.error}</span>
                        </div>`;
                }
                const geo = [lu.country, lu.region, lu.city].filter(Boolean).join(' · ');
                const meta = [geo, lu.asn, lu.isp].filter(Boolean).join('  ');
                return `
                    <div class="egress-item">
                        <span class="egress-provider">${lu.provider}</span>
                        <span class="egress-ip">${lu.ip || '--'}</span>
                        <span class="egress-duration">${lu.duration_ms} ms</span>
                        <span class="egress-meta">${meta || '—'}</span>
                    </div>`;
            };

            const dom = (data.lookups || []).filter(x => x.scope === 'domestic').map(itemHTML).join('');
            const glb = (data.lookups || []).filter(x => x.scope === 'global').map(itemHTML).join('');
            domesticEl.innerHTML = dom || '<div class="egress-item"><small>无数据</small></div>';
            globalEl.innerHTML = glb || '<div class="egress-item"><small>无数据</small></div>';
            statusEl.textContent = data.generated_at ? `查询于 ${data.generated_at}` : '等待查询';
            renderDomesticIPSnapshot(data.domestic_ip || {});
            refreshProxyDisplay();
        }

        function setIdentityBadge(el, text, mode) {
            if (!el) return;
            el.textContent = text;
            el.classList.remove('ok', 'warn', 'fail');
            if (mode) el.classList.add(mode);
        }

        function renderDomesticIPSnapshot(snapshot) {
            const v4 = snapshot.ipv4 || {};
            const v6 = snapshot.ipv6 || {};

            const ipv4IP = document.getElementById('domestic-ipv4-ip');
            const ipv4Location = document.getElementById('domestic-ipv4-location');
            const ipv6IP = document.getElementById('domestic-ipv6-ip');
            const ipv6Location = document.getElementById('domestic-ipv6-location');
            const ipv6Port = document.getElementById('domestic-ipv6-port');

            if (ipv4IP) ipv4IP.textContent = v4.ip || '未检测到';
            if (ipv4Location) ipv4Location.textContent = v4.error || v4.location || '等待查询';

            if (ipv6IP) ipv6IP.textContent = v6.ip || '未检测到';
            if (ipv6Location) ipv6Location.textContent = v6.error || v6.location || '等待查询';

            const portProbe = v6.port_probe || {};
            if (portProbe.status === 'reachable') {
                setIdentityBadge(ipv6Port, `高位端口可达 ${portProbe.latency_ms || 0} ms`, 'ok');
            } else if (portProbe.status === 'blocked') {
                setIdentityBadge(ipv6Port, '高位端口疑似受限', 'fail');
            } else if (portProbe.status === 'closed') {
                setIdentityBadge(ipv6Port, '探针关闭', 'warn');
            } else if (portProbe.status === 'unavailable') {
                setIdentityBadge(ipv6Port, '高位端口未检测', 'warn');
            } else {
                setIdentityBadge(ipv6Port, '高位端口检测失败', 'fail');
            }
        }

        async function initEgressLookups() {
            if (state.egressInitialized) return;
            state.egressInitialized = true;
            const btn = document.getElementById('egress-refresh-btn');
            if (!btn) return;
            const statusEl = document.getElementById('egress-status');

            const load = async (force) => {
                statusEl.textContent = force ? '查询中...' : '加载中...';
                btn.disabled = true;
                try {
                    const resp = await fetch('/api/v1/network/egress-lookups', {
                        method: force ? 'POST' : 'GET',
                        cache: 'no-store'
                    });
                    renderEgressLookups(await resp.json());
                } catch (e) {
                    statusEl.textContent = '加载失败: ' + e.message;
                } finally {
                    btn.disabled = false;
                }
            };

            btn.addEventListener('click', () => load(true));
            await load(false);
        }

        async function initNICRealtime() {
            if (state.nicRealtimeInitialized) return;
            state.nicRealtimeInitialized = true;
            const listEl = document.getElementById('nic-realtime-list');
            const statusEl = document.getElementById('nic-realtime-status');
            if (!listEl) return;

            const render = (data) => {
                if (!data.nics || data.nics.length === 0) {
                    listEl.innerHTML = `<div class="nic-realtime-item"><small>未配置监控网卡（MONITORED_NICS）</small></div>`;
                    statusEl.textContent = '无数据';
                    return;
                }
                listEl.innerHTML = data.nics.map(n => `
                    <div class="nic-realtime-item">
                        <div class="nic-realtime-head">
                            <span class="nic-realtime-name">${n.name}</span>
                            <span class="nic-realtime-badge ${n.present ? 'online' : 'offline'}">${n.present ? 'UP' : 'DOWN'}</span>
                        </div>
                        <div class="nic-realtime-rows">
                            <div class="nic-realtime-cell">
                                <span class="nic-realtime-label">↓ 下行</span>
                                <span class="nic-realtime-value rx">${formatBitsPerSec(n.rx_bps)}</span>
                            </div>
                            <div class="nic-realtime-cell">
                                <span class="nic-realtime-label">↑ 上行</span>
                                <span class="nic-realtime-value tx">${formatBitsPerSec(n.tx_bps)}</span>
                            </div>
                        </div>
                        <div class="nic-realtime-total">累计 ↓ ${formatBytes(n.rx_total)} / ↑ ${formatBytes(n.tx_total)}</div>
                    </div>
                `).join('');
                statusEl.textContent = `采样 ${data.timestamp || ''}`;
            };

            const tick = async () => {
                try {
                    const resp = await fetch('/api/v1/network/realtime', { cache: 'no-store' });
                    if (!resp.ok) return;
                    render(await resp.json());
                } catch (_) {}
            };
            startNICRealtimePolling(tick);
        }

        async function initAutoRefreshToggle() {
            if (state.autoRefreshInitialized) return;
            state.autoRefreshInitialized = true;
            await loadSettings();
        }

        function initSSE() {
            if (state.sse) return;
            try {
                const es = new EventSource('/api/v1/events');
                es.addEventListener('summary', (ev) => {
                    try {
                        const summary = JSON.parse(ev.data);
                        state.summary = summary;
                        renderSummary(summary);
                    } catch (_) {}
                });
                es.onerror = () => { /* browser will reconnect */ };
                state.sse = es;
            } catch (_) {}
        }

        function initTrace() {
            if (state.traceInitialized) return;
            state.traceInitialized = true;
            const btn = document.getElementById('trace-run');
            const input = document.getElementById('trace-host');
            const out = document.getElementById('trace-output');
            const summary = document.getElementById('trace-summary');
            const detailsBtn = document.getElementById('trace-details-btn');
            if (!btn) return;

            const renderTraceSummary = (items) => {
                if (!summary) return;
                summary.innerHTML = items.map(item => `
                    <div class="trace-summary-item">
                        <span class="trace-summary-label">${item.label}</span>
                        <span class="trace-summary-value">${item.value}</span>
                    </div>
                `).join('');
            };

            const renderTraceRows = (data) => {
                const hops = Array.isArray(data.hops) ? data.hops : [];
                if (hops.length === 0) {
                    out.innerHTML = '<div class="trace-empty">未返回可用跳点</div>';
                    return;
                }

                out.innerHTML = hops.map(h => {
                    const primary = h.ip || '*';
                    const secondary = h.location || (primary === '*' ? '超时' : '归属地查询中');
                    const timedOut = !h.ip && !h.host;
                    const latency = timedOut ? '超时' : (Number.isFinite(h.latency_ms) && h.latency_ms > 0 ? `${h.latency_ms} ms` : '--');
                    const latencyClass = timedOut ? 'fail' : (h.latency_ms > 200 ? 'warn' : '');
                    return `
                        <div class="trace-hop">
                            <div class="trace-hop-host">${primary}</div>
                            <div class="trace-hop-ip">${secondary}</div>
                            <div class="trace-hop-latency ${latencyClass}">${latency}</div>
                        </div>
                    `;
                }).join('');
            };

            const run = async () => {
                const host = (input.value || '').trim();
                if (!host) return;
                btn.disabled = true;
                if (detailsBtn) detailsBtn.disabled = true;
                state.traceResult = null;
                openTraceWindow();
                renderTraceSummary([
                    { label: '目标', value: host },
                    { label: '状态', value: '追踪中' },
                    { label: '工具', value: 'mtr' }
                ]);
                out.innerHTML = '<div class="trace-empty">正在采集路径信息...</div>';
                try {
                    await fetch(`/api/v1/diagnostics/trace?host=${encodeURIComponent(host)}`, {
                        method: 'POST',
                        cache: 'no-store'
                    });

                    const poll = async () => {
                        const resp = await fetch('/api/v1/diagnostics/trace/task', { cache: 'no-store' });
                        const data = await resp.json();
                        if (data.error) {
                            renderTraceSummary([
                                { label: '目标', value: data.target || host },
                                { label: '状态', value: '失败' },
                                { label: '工具', value: data.tool || 'mtr' }
                            ]);
                            out.innerHTML = `<div class="trace-empty">错误: ${data.error}</div>`;
                            if (state.tracePoller) {
                                clearInterval(state.tracePoller);
                                state.tracePoller = null;
                            }
                            return;
                        }

                        state.traceResult = data;
                        renderTraceSummary([
                            { label: '目标', value: data.target || host },
                            { label: '状态', value: data.running ? '追踪中' : `${(data.hops || []).length} 跳` },
                            { label: '工具', value: data.tool || 'mtr' }
                        ]);
                        renderTraceRows(data);
                        if (detailsBtn) detailsBtn.disabled = false;

                        if (!data.running && state.tracePoller) {
                            clearInterval(state.tracePoller);
                            state.tracePoller = null;
                        }
                    };

                    await poll();
                    if (state.tracePoller) clearInterval(state.tracePoller);
                    state.tracePoller = setInterval(poll, 1000);
                } catch (e) {
                    renderTraceSummary([
                        { label: '目标', value: host },
                        { label: '状态', value: '请求失败' },
                        { label: '工具', value: 'mtr' }
                    ]);
                    out.innerHTML = `<div class="trace-empty">请求失败: ${e.message}</div>`;
                } finally {
                    btn.disabled = false;
                }
            };

            btn.addEventListener('click', async () => {
                await run();
            });
            detailsBtn?.addEventListener('click', () => openTraceWindow());
            input?.addEventListener('keydown', async (event) => {
                if (event.key === 'Enter') {
                    event.preventDefault();
                    await run();
                }
            });
        }

        function parseTargetList(text) {
            return text.split(/[,\n]/).map(s => s.trim()).filter(Boolean).map(raw => {
                const parts = raw.split('|');
                if (parts.length === 2) return { name: parts[0].trim(), url: parts[1].trim() };
                return { name: raw, url: raw };
            });
        }
        function formatTargetList(list) {
            return (list || []).map(t => `${t.name}|${t.url}`).join(',\n');
        }

        async function initSettings() {
            if (state.settingsInitialized) return;
            state.settingsInitialized = true;
            await loadSettings();
        }

        function applySettingsToForm() {
            if (elements.settingAutoRefreshEnabled) {
                elements.settingAutoRefreshEnabled.checked = !!state.settings.auto_refresh_enabled;
            }
            if (elements.settingRefreshIntervalSec) {
                elements.settingRefreshIntervalSec.value = String(state.settings.refresh_interval_sec || 10);
            }
            if (elements.settingNICRealtimeEnabled) {
                elements.settingNICRealtimeEnabled.checked = !!state.settings.nic_realtime_enabled;
            }
            if (elements.settingNICRealtimeIntervalSec) {
                elements.settingNICRealtimeIntervalSec.value = String(state.settings.nic_realtime_interval_sec || 1);
                elements.settingNICRealtimeIntervalSec.disabled = !state.settings.nic_realtime_enabled;
            }
        }

        async function loadSettings() {
            try {
                const [settingsResp, autoResp] = await Promise.all([
                    fetch('/api/v1/settings', { cache: 'no-store' }),
                    fetch('/api/v1/auto-refresh', { cache: 'no-store' })
                ]);
                const settingsData = await settingsResp.json();
                const autoData = await autoResp.json();
                state.settings = {
                    refresh_interval_sec: settingsData.refresh_interval_sec || state.refreshInterval || 10,
                    auto_refresh_enabled: !!autoData.enabled,
                    nic_realtime_enabled: settingsData.nic_realtime_enabled !== false,
                    nic_realtime_interval_sec: settingsData.nic_realtime_interval_sec || 1
                };
                applySettingsToForm();
            } catch (error) {
                console.error(error);
            }
        }

        function stopNICRealtimePolling() {
            if (state.nicRealtimeInterval) {
                clearInterval(state.nicRealtimeInterval);
                state.nicRealtimeInterval = null;
            }
        }

        function startNICRealtimePolling(tick) {
            stopNICRealtimePolling();
            if (!state.settings.nic_realtime_enabled) {
                if (elements.nicRealtimeStatus) {
                    elements.nicRealtimeStatus.textContent = '已关闭实时刷新';
                }
                applySettingsToForm();
                return;
            }
            const intervalSec = Math.max(1, Number.parseInt(state.settings.nic_realtime_interval_sec, 10) || 1);
            tick();
            state.nicRealtimeInterval = setInterval(tick, intervalSec * 1000);
            applySettingsToForm();
        }

        async function saveSettings() {
            const payload = {
                refresh_interval_sec: parseInt(elements.settingRefreshIntervalSec?.value || '10', 10) || 10,
                auto_refresh_enabled: !!elements.settingAutoRefreshEnabled?.checked,
                nic_realtime_enabled: !!elements.settingNICRealtimeEnabled?.checked,
                nic_realtime_interval_sec: parseInt(elements.settingNICRealtimeIntervalSec?.value || '1', 10) || 1
            };

            try {
                const settingsResp = await fetch('/api/v1/settings', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                if (!settingsResp.ok) {
                    throw new Error('settings save failed');
                }
                await fetch(`/api/v1/auto-refresh?enabled=${payload.auto_refresh_enabled}`, { method: 'POST' });
                state.settings = { ...state.settings, ...payload };
                state.refreshInterval = payload.refresh_interval_sec;
                applySettingsToForm();
                state.nicRealtimeInitialized = false;
                initNICRealtime();
                showToast('设置已保存', 'success');
            } catch (error) {
                console.error(error);
                showToast('设置保存失败', 'error');
            }
        }

        function boot() {
            if (state.initialized) return;
            state.initialized = true;
            initTheme();
            initWithRetry();
        }

        setTimeout(boot, 100);
