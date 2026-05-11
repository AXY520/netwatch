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
                broadband_domestic_only: true,
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
            controlsBound: false,
            appTrafficSort: {
                key: 'total',
                direction: 'desc'
            }
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
            valPlatformConnectivity: document.getElementById('val-platform-connectivity'),
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
            settingBroadbandDomesticOnly: document.getElementById('setting-broadband-domestic-only'),
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
        }

        function createSpeedSampler() {
            return {
                startedAt: performance.now(),
                warmupEndAt: performance.now() + 2500, // 2.5秒预热
                lastAt: performance.now(),
                lastBytes: 0,
                samples: [],
                allMbps: [], // 预热后的所有样本
                lastMbps: 0,
                isWarmup: true
            };
        }

        function observeSpeedSampler(sampler, totalBytes) {
            const now = performance.now();
            if (sampler.isWarmup && now >= sampler.warmupEndAt) {
                sampler.isWarmup = false;
                // 进入正式测试阶段，重置计数以排除预热影响
                sampler.lastBytes = totalBytes;
                sampler.lastAt = now;
                return sampler.lastMbps;
            }

            const elapsedMs = now - sampler.lastAt;
            if (elapsedMs > 100) { // 100ms 采样一次
                const deltaBytes = Math.max(0, totalBytes - sampler.lastBytes);
                const instantMbps = (deltaBytes * 8) / (elapsedMs / 1000) / 1_000_000;
                
                if (!sampler.isWarmup && instantMbps > 0) {
                    sampler.samples.push(instantMbps);
                    sampler.allMbps.push(instantMbps);
                    if (sampler.samples.length > 20) {
                        sampler.samples = sampler.samples.slice(-20);
                    }
                }

                // 指数移动平均 (EMA)：LibreSpeed 风格的平滑
                // 如果是预热期，平滑权重更重，正式期则反应更快
                const weight = sampler.isWarmup ? 0.15 : 0.3;
                if (sampler.lastMbps > 0) {
                    sampler.lastMbps = instantMbps * weight + sampler.lastMbps * (1 - weight);
                } else {
                    sampler.lastMbps = instantMbps;
                }

                sampler.lastAt = now;
                sampler.lastBytes = totalBytes;
            }

            return sampler.lastMbps;
        }

        function stableSpeedFromSampler(sampler, totalBytes) {
            // 最终结果：丢弃所有样本中最高和最低的 10%，然后取剩余部分的平均值
            // 这能有效过滤掉 TCP 慢启动突发和偶发掉速
            const data = sampler.allMbps.length > 5 ? sampler.allMbps : sampler.samples;
            if (data.length === 0) {
                const totalElapsedSec = Math.max((performance.now() - sampler.startedAt) / 1000, 0.5);
                return (totalBytes * 8) / totalElapsedSec / 1_000_000;
            }

            const sorted = [...data].sort((a, b) => a - b);
            const cut = Math.floor(sorted.length * 0.1);
            const trimmed = sorted.slice(cut, sorted.length - cut);
            const final = trimmed.reduce((sum, v) => sum + v, 0) / (trimmed.length || 1);
            
            // 兜底：如果算出来的结果明显异常（比如 0），则取未裁剪的中位数
            return final > 0 ? final : sorted[Math.floor(sorted.length / 2)];
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

            if (elements.valPlatformConnectivity) {
                elements.valPlatformConnectivity.textContent = formatPlatformConnectivity(networkInfo);
            }

            const interfaces = Array.isArray(networkInfo.interfaces) ? networkInfo.interfaces : [];
            elements.interfacesTable.innerHTML = interfaces.map(iface => {
                // Wi-Fi 时直接用 SSID 作为接口主标题；有线沿用 label
                let mainLabel;
                if (iface.link_type === 'wifi' && iface.wifi_ssid) {
                    mainLabel = iface.wifi_ssid;
                } else {
                    mainLabel = iface.label || ifaceFallbackLabel(iface.link_type) || iface.name || '---';
                }
                const subtitle = iface.name && iface.name !== mainLabel
                    ? `<br><small style="color:var(--text-muted)">${iface.name}</small>`
                    : '';
                const statusCell = formatDeviceStatus(iface.device_status);
                // 过滤 link-local，主表格只展示真正可访问的地址
                const ipv4List = (iface.ipv4 || []).filter(s => s);
                const ipv6List = (iface.ipv6 || []).filter(s => !/^fe80:/i.test(s));
                return `
                    <tr>
                        <td class="col-iface">${mainLabel}${subtitle}</td>
                        <td class="col-status">${statusCell}</td>
                        <td class="col-ipv4">${ipv4List.length ? ipv4List.join('<br>') : '---'}</td>
                        <td class="col-ipv6">${ipv6List.length ? ipv6List.join('<br>') : '---'}</td>
                        <td class="col-mac"><small>${iface.hardware_addr || '---'}</small></td>
                    </tr>
                `;
            }).join('') || '<tr><td colspan="5" class="placeholder">未找到目标网卡</td></tr>';
        }

        function ifaceFallbackLabel(linkType) {
            if (linkType === 'wired') return '有线';
            if (linkType === 'wifi') return 'Wi-Fi';
            return '';
        }

        function formatDeviceStatus(status) {
            switch (status) {
                case 'connected': return '已连接';
                case 'disconnected': return '未连接';
                case 'connecting': return '连接中';
                case 'disconnecting': return '断开中';
                case 'disabled': return '已禁用';
                case 'unavailable': return '不可用';
                case 'unknown': return '未知';
                case '': case undefined: return '---';
                default: return status;
            }
        }

        function formatPlatformConnectivity(networkInfo) {
            const level = networkInfo.platform_connectivity || '';
            switch (level) {
                case 'Full': return '已联网';
                case 'Limited': return '受限';
                case 'Portal': return '需要登录认证';
                case 'None': return '无法访问外网';
                case 'Unknown': return '未知';
                case '':
                    if (networkInfo.has_internet) return '已联网';
                    return 'SDK 状态异常';
                default: return level;
            }
        }

        function renderNAT() { /* NAT card removed */ }

        function detectProxyState() {
            const ci = state.summary?.website_connectivity || {};
            const globalSites = (ci.global || []).filter(s => s && s.status);
            if (globalSites.length === 0) {
                return { mode: 'unknown', label: '状态不明' };
            }
            const okCount = globalSites.filter(s => s.status === 'ok').length;
            const total = globalSites.length;

            const lookups = state.egressData?.lookups || [];
            const dom = lookups.find(l => l.scope === 'domestic' && l.ip);
            const glb = lookups.find(l => l.scope === 'global' && l.ip);
            const inChina = (entry) => {
                if (!entry) return false;
                const c = (entry.country || '') + (entry.region || '');
                return c.includes('中国') || c.includes('China') || c.includes('CN');
            };
            const boxInChina = inChina(dom) || inChina(glb);

            if (okCount === total) {
                if (boxInChina) {
                    return { mode: 'proxy', label: '存在代理环境' };
                }
                return { mode: 'direct', label: '获取到境外出口' };
            }
            if (okCount === 0) {
                return { mode: 'direct', label: '无代理' };
            }
            return { mode: 'partial', label: '状态不明' };
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
                `宽带测速使用 Speedtest.net 节点；开启“仅国内节点”后会强制选择国内候选节点，每阶段 ${state.speedConfig.broadband_duration_sec} 秒。`;
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
            elements.broadbandNote.textContent = `宽带测速使用 Speedtest.net 节点，每阶段 ${state.speedConfig.broadband_duration_sec} 秒。`;
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
            elements.broadbandNote.textContent = task.message || broadbandStageMap[task.stage] || '等待测速开始';
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

        async function runTransferTest() {
            if (state.runningTest) return;
            state.runningTest = 'transfer';
            updateWindowControls();
            resetTransferMetrics();

            const s = new Speedtest();
            state.transferAbortController = { abort: () => s.abort() };
            
            s.setParameter("url_dl", "/api/v1/speed/local/download?sec=30");
            s.setParameter("url_ul", "/api/v1/speed/local/upload");
            s.setParameter("url_ping", "/api/v1/speed/local/ping");
            s.setParameter("url_getIp", "/api/v1/summary");
            s.setParameter("worker_path", "/speedtest_worker.js");
            s.setParameter("test_order", "P_D_U");
            s.setParameter("time_dl_max", state.speedConfig.local_transfer_duration_sec);
            s.setParameter("time_ul_max", state.speedConfig.local_transfer_duration_sec);
            s.setParameter("time_auto", false);
            s.setParameter("count_ping", 10);
            
            let lastData = {};
            s.onupdate = (data) => {
                lastData = data;
                const testStateMap = {
                    0: { stage: '准备中', progress: 0, mode: 'Idle' },
                    1: { stage: '下载测速', progress: 15 + (data.dlProgress || 0) * 40, mode: 'Download' },
                    2: { stage: '延迟采样', progress: 5 + (data.pingProgress || 0) * 10, mode: 'Ping' },
                    3: { stage: '上传测速', progress: 55 + (data.ulProgress || 0) * 45, mode: 'Upload' }
                };
                
                const current = testStateMap[data.testState];
                if (current) {
                    elements.transferStage.textContent = current.stage;
                    elements.transferProgress.textContent = `${Math.round(current.progress)}%`;
                    
                    let speed = 0, unit = 'Mbps', caption = '';
                    if (current.mode === 'Download') {
                        speed = parseFloat(data.dlStatus) || 0;
                        elements.transferDownload.textContent = speed.toFixed(1);
                        caption = `正在下载... ${speed.toFixed(2)} Mbps`;
                    } else if (current.mode === 'Upload') {
                        speed = parseFloat(data.ulStatus) || 0;
                        elements.transferUpload.textContent = speed.toFixed(1);
                        caption = `正在上传... ${speed.toFixed(2)} Mbps`;
                    } else if (current.mode === 'Ping') {
                        speed = parseFloat(data.pingStatus) || 0;
                        elements.transferLatency.textContent = formatMS(speed);
                        elements.transferJitter.textContent = formatMS(parseFloat(data.jitterStatus));
                        unit = 'ms';
                        caption = `延迟采样中... ${speed.toFixed(0)} ms`;
                    }
                    
                    setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, speed, unit, current.mode, caption);
                }
            };

            s.onend = async (aborted) => {
                if (aborted) {
                    return;
                }

                const downloadMbps = parseFloat(elements.transferDownload.textContent) || 0;
                const uploadMbps = parseFloat(elements.transferUpload.textContent) || 0;
                const pingMs = parseFloat(elements.transferLatency.textContent) || 0;
                const jitterMs = parseFloat(elements.transferJitter.textContent) || 0;

                elements.transferStage.textContent = '已完成';
                elements.transferProgress.textContent = '100%';
                elements.transferNote.textContent = '网页到本机传输测速完成';
                setPrimaryGauge(null, elements.transferPrimaryMode, elements.transferPrimarySpeed, elements.transferPrimaryUnit, elements.transferPrimaryCaption, downloadMbps, 'Mbps', 'Result', '测速完成');

                try {
                    await fetch('/api/v1/speed/local/result', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            download_mbps: downloadMbps,
                            upload_mbps: uploadMbps,
                            round_trip_latency_ms: Math.round(pingMs),
                            jitter_ms: Math.round(jitterMs)
                        })
                    });
                    await loadSpeedHistory();
                } catch (e) {
                    console.error('Failed to save transfer result:', e);
                }

                state.runningTest = null;
                state.transferAbortController = null;
                updateWindowControls();
            };

            updateTransferProgress('准备中', 0, '正在启动网页到本机传输测速');
            s.start();
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
                const isEnabled = !!elements.settingNICRealtimeEnabled.checked;
                state.settings.nic_realtime_enabled = isEnabled;
                if (elements.settingNICRealtimeIntervalSec) {
                    elements.settingNICRealtimeIntervalSec.disabled = !isEnabled;
                }
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
            initAppTraffic();
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

        async function initAppTraffic() {
            if (state.appTrafficInitialized) return;
            state.appTrafficInitialized = true;
            const table = document.getElementById('app-traffic-table');
            const tbody = document.querySelector('#app-traffic-table tbody');
            const statusEl = document.getElementById('app-traffic-status');
            const noteEl = document.getElementById('app-traffic-note');
            const btn = document.getElementById('app-traffic-refresh-btn');
            const sortButtons = Array.from(document.querySelectorAll('#app-traffic-table [data-sort-key]'));
            let latestTrafficData = null;
            if (!tbody) return;

            const getAppTrafficName = (item) => {
                return String(item.app_title || item.app_id || item.project || item.bridge || '').toLowerCase();
            };

            const compareAppTraffic = (a, b, key, direction) => {
                let result = 0;
                if (key === 'app') {
                    result = getAppTrafficName(a).localeCompare(getAppTrafficName(b), 'zh-CN');
                } else if (key === 'rx') {
                    result = (a.rx_bytes || 0) - (b.rx_bytes || 0);
                } else if (key === 'tx') {
                    result = (a.tx_bytes || 0) - (b.tx_bytes || 0);
                } else {
                    result = ((a.rx_bytes || 0) + (a.tx_bytes || 0)) - ((b.rx_bytes || 0) + (b.tx_bytes || 0));
                }
                return direction === 'asc' ? result : -result;
            };

            const updateSortHeaders = () => {
                if (!table) return;
                const { key, direction } = state.appTrafficSort;
                sortButtons.forEach((button) => {
                    const active = button.dataset.sortKey === key;
                    button.classList.toggle('active', active);
                    button.dataset.sortDirection = active ? direction : 'none';
                    button.setAttribute('aria-sort', active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
                });
            };

            const renderTraffic = (data) => {
                latestTrafficData = data;
                const list = Array.isArray(data.bridges) ? [...data.bridges] : [];
                updateSortHeaders();
                if (list.length === 0) {
                    tbody.innerHTML = '<tr><td colspan="4" class="placeholder">未发现 lzc-br-* 网桥（容器需要 host 网络模式）</td></tr>';
                } else {
                    const { key, direction } = state.appTrafficSort;
                    list.sort((a, b) => compareAppTraffic(a, b, key, direction));
                    tbody.innerHTML = list.map(b => {
                        const total = (b.rx_bytes || 0) + (b.tx_bytes || 0);
                        let appCell;
                        if (b.app_title) {
                            appCell = `<strong>${b.app_title}</strong>`;
                        } else if (b.app_id) {
                            appCell = `<strong>${shortAppName(b.app_id)}</strong><br><small style="color:var(--text-muted)">${b.app_id}</small>`;
                        } else if (b.project) {
                            appCell = `<small style="color:var(--text-muted)">${b.project}</small>`;
                        } else {
                            appCell = `<small style="color:var(--text-muted)">${b.bridge}</small>`;
                        }
                        return `
                            <tr>
                                <td class="col-app">${appCell}</td>
                                <td class="col-rx">${formatBytes(b.rx_bytes || 0)}</td>
                                <td class="col-tx">${formatBytes(b.tx_bytes || 0)}</td>
                                <td class="col-total">${formatBytes(total)}</td>
                            </tr>
                        `;
                    }).join('');
                }
                if (statusEl) statusEl.textContent = data.generated_at ? `采样于 ${data.generated_at}` : '';
                if (noteEl && data.note) noteEl.textContent = data.note;
            };

            const load = async () => {
                if (btn) btn.disabled = true;
                if (statusEl) statusEl.textContent = '采样中...';
                try {
                    const resp = await fetch('/api/v1/network/app-traffic', { cache: 'no-store' });
                    renderTraffic(await resp.json());
                } catch (e) {
                    if (statusEl) statusEl.textContent = '采样失败: ' + e.message;
                } finally {
                    if (btn) btn.disabled = false;
                }
            };

            sortButtons.forEach((button) => {
                button.addEventListener('click', () => {
                    const key = button.dataset.sortKey || 'total';
                    if (state.appTrafficSort.key === key) {
                        state.appTrafficSort.direction = state.appTrafficSort.direction === 'asc' ? 'desc' : 'asc';
                    } else {
                        state.appTrafficSort.key = key;
                        state.appTrafficSort.direction = key === 'app' ? 'asc' : 'desc';
                    }
                    if (latestTrafficData) renderTraffic(latestTrafficData);
                    else updateSortHeaders();
                });
            });

            updateSortHeaders();
            if (btn) btn.addEventListener('click', load);
            await load();
        }

        function formatBytes(n) {
            if (!n || n <= 0) return '0 B';
            const units = ['B', 'KB', 'MB', 'GB', 'TB'];
            let i = 0;
            let v = n;
            while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
            return (i === 0 ? v.toString() : v.toFixed(v < 10 ? 2 : 1)) + ' ' + units[i];
        }

        // shortAppName extracts the last dotted segment from an appid,
        // e.g. cloud.lazycat.app.netwatch → netwatch.
        function shortAppName(appid) {
            if (!appid) return '';
            const parts = appid.split('.');
            return parts[parts.length - 1] || appid;
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
            if (elements.settingBroadbandDomesticOnly) {
                elements.settingBroadbandDomesticOnly.checked = !!state.settings.broadband_domestic_only;
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
                    broadband_domestic_only: !!settingsData.broadband_domestic_only,
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
                broadband_domestic_only: !!elements.settingBroadbandDomesticOnly?.checked,
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
