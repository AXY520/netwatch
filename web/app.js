(function () {
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
                broadband_domestic_only: true,
                nic_realtime_enabled: true,
                nic_realtime_interval_sec: 1,
                chart_time_label_interval: 0,
                traffic_sampling_enabled: true,
                traffic_sampling_interval_sec: 60,
                per_app_sampling_interval: {},
                persistent_traffic_bridges: [],
                background_monitor_enabled: false,
                background_monitor_interval_sec: 60,
                notifications_enabled: false,
                client_notification_enabled: true,
                notify_abnormal_traffic: true,
                notify_egress_change: true,
                notify_connectivity_change: true,
                notify_lan_device_change: true,
                abnormal_traffic_threshold_mbps: 100,
                bark_enabled: false,
                bark_server_url: 'https://api.day.app',
                bark_device_key: '',
                bark_group: 'Netwatch',
                dnd_enabled: false,
                dnd_start: '22:00',
                dnd_end: '08:00',
                scheduled_notify_enabled: false,
                scheduled_notify_time: '09:00'
            },
            activeWindow: null,
            runningTest: null,
            broadbandPoller: null,
            transferAbortController: null,
            sse: null,
            initialized: false,
            settingsInitialized: false,
            egressInitialized: false,
            nicRealtimeInitialized: false,
            traceInitialized: false,
            controlsBound: false,
            notificationLastID: Number(localStorage.getItem('netwatch_notification_last_id') || '0') || 0,
            deviceID: localStorage.getItem('netwatch_device_id') || '',
            lzcGatewayPromise: null,
            notificationUnsupported: localStorage.getItem('netwatch_notification_unsupported') === 'true',
            modalScrollY: 0,
            modalLockCount: 0,
            appTrafficSort: {
                key: 'total',
                direction: 'desc'
            }
        };

        const elements = {
            themeToggle: document.getElementById('theme-toggle'),
            refreshBtn: document.getElementById('refresh-btn'),
            overlay: document.getElementById('loading-overlay'),
            websiteRefreshBtn: document.getElementById('website-refresh-btn'),
            websiteStatus: document.getElementById('website-status'),
            domesticTable: document.querySelector('#domestic-table tbody'),
            globalTable: document.querySelector('#global-table tbody'),
            interfacesTable: document.querySelector('#interfaces-table tbody'),
            valGw4: document.getElementById('val-gw4'),
            valPlatformConnectivity: document.getElementById('val-platform-connectivity'),
            nicRealtimeRefreshBtn: document.getElementById('nic-realtime-refresh-btn'),
            nicRealtimeStatus: document.getElementById('nic-realtime-status'),
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
            settingBroadbandDomesticOnly: document.getElementById('setting-broadband-domestic-only'),
            settingNICRealtimeEnabled: document.getElementById('setting-nic-realtime-enabled'),
            settingNICRealtimeIntervalSec: document.getElementById('setting-nic-realtime-interval-sec'),
            settingBackgroundMonitorEnabled: document.getElementById('setting-background-monitor-enabled'),
            settingBackgroundMonitorIntervalSec: document.getElementById('setting-background-monitor-interval-sec'),
            settingNotificationsEnabled: document.getElementById('setting-notifications-enabled'),
            settingClientNotificationEnabled: document.getElementById('setting-client-notification-enabled'),
            settingNotifyAbnormalTraffic: document.getElementById('setting-notify-abnormal-traffic'),
            settingAbnormalTrafficThresholdMbps: document.getElementById('setting-abnormal-traffic-threshold-mbps'),
            settingNotifyEgressChange: document.getElementById('setting-notify-egress-change'),
            settingNotifyConnectivityChange: document.getElementById('setting-notify-connectivity-change'),
            settingNotifyLANDeviceChange: document.getElementById('setting-notify-lan-device-change'),
            settingBarkEnabled: document.getElementById('setting-bark-enabled'),
            settingBarkServerURL: document.getElementById('setting-bark-server-url'),
            settingBarkDeviceKey: document.getElementById('setting-bark-device-key'),
            settingBarkGroup: document.getElementById('setting-bark-group'),
            testBarkNotification: document.getElementById('test-bark-notification'),
            settingDNDEnabled: document.getElementById('setting-dnd-enabled'),
            settingDNDStart: document.getElementById('setting-dnd-start'),
            settingDNDEnd: document.getElementById('setting-dnd-end'),
            settingScheduledNotifyEnabled: document.getElementById('setting-scheduled-notify-enabled'),
            settingScheduledNotifyTime: document.getElementById('setting-scheduled-notify-time'),
            notificationSettingsWindow: document.getElementById('notification-settings-window'),
            openNotificationSettings: document.getElementById('open-notification-settings'),
            closeNotificationSettings: document.getElementById('close-notification-settings'),
            saveNotificationSettings: document.getElementById('save-notification-settings'),
            openNotifyTemplate: document.getElementById('open-notify-template'),
            closeNotifyTemplate: document.getElementById('close-notify-template'),
            saveNotifyTemplate: document.getElementById('save-notify-template'),
            notifyTemplateWindow: document.getElementById('notify-template-window'),
            notifyTemplateTitle: document.getElementById('notify-template-title'),
            notifyTemplateBody: document.getElementById('notify-template-body'),
            broadbandNote: document.getElementById('broadband-note'),
            transferNote: document.getElementById('transfer-note'),
            runBroadbandTest: document.getElementById('run-broadband-test'),
            runTransferTest: document.getElementById('run-transfer-test'),
            broadbandPrimaryMode: document.getElementById('broadband-primary-mode'),
            broadbandPrimaryCaption: document.getElementById('broadband-primary-caption'),
            broadbandStage: document.getElementById('broadband-stage'),
            broadbandProgress: document.getElementById('broadband-progress'),
            broadbandDownload: document.getElementById('broadband-download'),
            broadbandUpload: document.getElementById('broadband-upload'),
            broadbandLatency: document.getElementById('broadband-latency'),
            broadbandJitter: document.getElementById('broadband-jitter'),
            broadbandNodeName: document.getElementById('broadband-node-name'),
            broadbandNodeProvider: document.getElementById('broadband-node-provider'),
            broadbandNodeRegion: document.getElementById('broadband-node-region'),
            broadbandNodeSource: document.getElementById('broadband-node-source'),
            broadbandDurationNode: document.getElementById('broadband-duration-node'),
            broadbandDurationLatency: document.getElementById('broadband-duration-latency'),
            broadbandDurationDownload: document.getElementById('broadband-duration-download'),
            broadbandDurationUpload: document.getElementById('broadband-duration-upload'),
            broadbandDurationTotal: document.getElementById('broadband-duration-total'),
            broadbandFailureStage: document.getElementById('broadband-failure-stage'),
            broadbandFailureReason: document.getElementById('broadband-failure-reason'),
            transferPrimaryMode: document.getElementById('transfer-primary-mode'),
            transferPrimaryCaption: document.getElementById('transfer-primary-caption'),
            transferStage: document.getElementById('transfer-stage'),
            transferProgress: document.getElementById('transfer-progress'),
            transferDownload: document.getElementById('transfer-download'),
            transferUpload: document.getElementById('transfer-upload'),
            transferLatency: document.getElementById('transfer-latency'),
            transferJitter: document.getElementById('transfer-jitter'),
            transferRTTMin: document.getElementById('transfer-rtt-min'),
            transferRTTAvg: document.getElementById('transfer-rtt-avg'),
            transferRTTMax: document.getElementById('transfer-rtt-max'),
            transferRTTJitter: document.getElementById('transfer-rtt-jitter'),
            transferDownloadBytes: document.getElementById('transfer-download-bytes'),
            transferUploadBytes: document.getElementById('transfer-upload-bytes'),
            transferTotalBytes: document.getElementById('transfer-total-bytes'),
            transferDuration: document.getElementById('transfer-duration'),
            broadbandHistory: document.getElementById('broadband-history'),
            transferHistory: document.getElementById('transfer-history')
        };

const i18n = (key) => (typeof window.__ === 'function' ? window.__(key) : key);

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

        const statusMap = { ok: i18n('ok'), down: i18n('down'), degraded: i18n('degraded'), unknown: i18n('unknown') };
        const broadbandStageMap = { starting: i18n('starting'), latency: i18n('latency'), download: i18n('download'), upload: i18n('upload'), finalizing: i18n('finalizing'), complete: i18n('complete'), canceled: i18n('canceled'), error: i18n('error') };
        const broadbandFailureStageMap = {
            node_selection: i18n('select_node'),
            latency: i18n('latency'),
            download: i18n('download'),
            upload: i18n('upload'),
            timeout: i18n('timeout')
        };

        function getIconUrl(name) {
            const lowerName = String(name || '').toLowerCase();
            const localIcons = ['baidu', 'bilibili', 'github', 'youtube'];
            return localIcons.includes(lowerName) ? `/icons/${lowerName}.ico` : '/icons/default.ico';
        }

        const countryNameZh = {
            'united states': '美国',
            'usa': '美国',
            'us': '美国',
            'china': '中国',
            'cn': '中国',
            'hong kong': '中国香港',
            'taiwan': '中国台湾',
            'japan': '日本',
            'singapore': '新加坡',
            'south korea': '韩国',
            'korea, republic of': '韩国',
            'germany': '德国',
            'united kingdom': '英国',
            'france': '法国',
            'netherlands': '荷兰',
            'canada': '加拿大',
            'australia': '澳大利亚'
        };

        const regionNameZh = {
            'california': '加利福尼亚州',
            'new york': '纽约州',
            'washington': '华盛顿州',
            'oregon': '俄勒冈州',
            'texas': '得克萨斯州',
            'illinois': '伊利诺伊州',
            'virginia': '弗吉尼亚州'
        };

        const cityNameZh = {
            'los angeles': '洛杉矶',
            'new york city': '纽约市',
            'new york': '纽约',
            'san jose': '圣何塞',
            'san francisco': '旧金山',
            'seattle': '西雅图',
            'chicago': '芝加哥',
            'ashburn': '阿什本',
            'tokyo': '东京',
            'osaka': '大阪',
            'seoul': '首尔',
            'singapore': '新加坡',
            'frankfurt': '法兰克福',
            'london': '伦敦',
            'paris': '巴黎',
            'amsterdam': '阿姆斯特丹',
            'toronto': '多伦多',
            'sydney': '悉尼'
        };

        function translateGeoName(value, kind) {
            const raw = String(value || '').trim();
            if (!raw) return '';
            const key = raw.toLowerCase();
            if (kind === 'country') return countryNameZh[key] || raw;
            if (kind === 'region') return regionNameZh[key] || raw;
            if (kind === 'city') return cityNameZh[key] || raw;
            return countryNameZh[key] || regionNameZh[key] || cityNameZh[key] || raw;
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

        function formatDurationMS(value) {
            const ms = Number(value);
            if (!Number.isFinite(ms) || ms <= 0) return '--';
            if (ms < 1000) return `${Math.round(ms)} ms`;
            return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`;
        }

        function formatMB(value) {
            const mb = Number(value);
            if (!Number.isFinite(mb) || mb <= 0) return '--';
            return `${mb.toFixed(mb >= 100 ? 0 : 1)} MB`;
        }

        function bytesToMB(value) {
            const bytes = Number(value);
            if (!Number.isFinite(bytes) || bytes <= 0) return 0;
            return bytes / 1024 / 1024;
        }

        function setText(el, value = '--') {
            if (el) el.textContent = value || '--';
        }

        function finiteNumber(value, fallback = 0) {
            const number = parseFloat(value);
            return Number.isFinite(number) ? number : fallback;
        }

        function summarizeRTT(samples = []) {
            const values = samples.map(Number).filter(v => Number.isFinite(v) && v > 0);
            if (!values.length) {
                return { min: 0, avg: 0, max: 0 };
            }
            const sum = values.reduce((acc, value) => acc + value, 0);
            return {
                min: Math.round(Math.min(...values)),
                avg: Math.round(sum / values.length),
                max: Math.round(Math.max(...values))
            };
        }

        function finishTransferRun() {
            if (state.runningTest === 'transfer') {
                state.runningTest = null;
            }
            state.transferAbortController = null;
            updateWindowControls();
        }

        function setPrimaryStatus(modeEl, captionEl, mode, caption) {
            if (modeEl) {
                modeEl.textContent = mode;
                modeEl.classList.remove('active', 'done', 'error');
                if (mode === 'Download' || mode === 'Upload' || mode === 'Ping') modeEl.classList.add('active');
                else if (mode === 'Result') modeEl.classList.add('done');
                else if (mode === 'Stopped') modeEl.classList.add('error');
            }
            if (captionEl) captionEl.textContent = caption || '';

        }

        function setSpeedPanelMode(scope, mode) {
            const dlPanel = document.getElementById(`${scope}-panel-download`);
            const upPanel = document.getElementById(`${scope}-panel-upload`);
            if (!dlPanel || !upPanel) return;
            dlPanel.classList.toggle('active', mode === 'Download');
            upPanel.classList.toggle('active', mode === 'Upload');
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
                renderPlaceholderTable(tbody, i18n('no_results'));
                return;
            }

            tbody.innerHTML = items.map(item => {
                return `
                <tr>
                    <td>
                        <div class="target-info">
                            <img class="site-icon" src="${getIconUrl(item.name)}" onerror="this.src='/icons/default.ico'">
                            <span>${escapeHtml(item.name)}</span>
                        </div>
                    </td>
                    <td data-label="${i18n('status_col')}"><span class="nat-badge ${getStatusClass(item.status)}">${statusMap[item.status] || i18n('unknown')}</span></td>
                    <td data-label="${i18n('latency_col')}" class="latency ${item.latency_ms > 200 ? 'high' : (item.latency_ms === 0 ? 'down' : '')}">
                        ${item.latency_ms > 0 ? `${item.latency_ms} ms` : i18n('connection_failed')}
                    </td>
                </tr>`;
            }).join('');
        }

        function renderNetworkInfo(networkInfo = {}) {
            elements.valGw4.textContent = networkInfo.default_ipv4?.gateway || i18n('unknown');

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
                        <td class="col-ipv4">${ipv4List.length ? ipv4List.map(escapeHtml).join('<br>') : '---'}</td>
                        <td class="col-ipv6">${ipv6List.length ? ipv6List.map(escapeHtml).join('<br>') : '---'}</td>
                        <td class="col-mac"><small>${escapeHtml(iface.hardware_addr) || '---'}</small></td>
                    </tr>
                `;
            }).join('') || `<tr><td colspan="5" class="placeholder">${i18n('no_target_nic')}</td></tr>`;
        }

        function ifaceFallbackLabel(linkType) {
            if (linkType === 'wired') return i18n('wired');
            if (linkType === 'wifi') return 'Wi-Fi';
            return '';
        }

        function formatDeviceStatus(status) {
            switch (status) {
                case 'connected': return i18n('connected');
                case 'disconnected': return i18n('disconnected');
                case 'connecting': return i18n('connecting');
                case 'disconnecting': return i18n('disconnecting');
                case 'disabled': return i18n('disabled');
                case 'unavailable': return i18n('unavailable');
                case 'unknown': return i18n('unknown');
                case '': case undefined: return '---';
                default: return status;
            }
        }

        function formatPlatformConnectivity(networkInfo) {
            const level = networkInfo.platform_connectivity || '';
            switch (level) {
                case 'Full': return i18n('internet_full');
                case 'Limited': return i18n('internet_limited');
                case 'Portal': return i18n('internet_portal');
                case 'None': return i18n('internet_none');
                case 'Unknown': return i18n('unknown');
                case '':
                    if (networkInfo.has_internet) return i18n('internet_full');
                    return i18n('sdk_status_error');
                default: return level;
            }
        }

        function detectProxyState() {
            const ci = state.summary?.website_connectivity || {};
            const globalSites = (ci.global || []).filter(s => s && s.status);
            if (globalSites.length === 0) {
                return { mode: 'unknown', label: i18n('unknown_status') };
            }
            const okCount = globalSites.filter(s => s.status === 'ok').length;
            const total = globalSites.length;

            const glb = (state.egressData?.lookups || []).find(l => l.scope === 'global' && l.ip);
            const domesticV4 = state.egressData?.domestic_ip?.ipv4;
            const inChina = (entry) => {
                if (!entry) return false;
                const c = (entry.country || '') + (entry.region || '') + (entry.location || '');
                return c.includes('中国') || c.includes('China') || c.includes('CN');
            };
            const boxInChina = inChina(domesticV4) || inChina(glb);

            if (okCount === total) {
                if (boxInChina) {
                    return { mode: 'proxy', label: i18n('proxy_detected') };
                }
                return { mode: 'direct', label: i18n('global_egress_detected') };
            }
            if (okCount === 0) {
                return { mode: 'direct', label: i18n('no_proxy') };
            }
            return { mode: 'partial', label: i18n('unknown_status') };
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
            updateConnectivityTable(elements.domesticTable, summary.website_connectivity?.domestic || []);
            updateConnectivityTable(elements.globalTable, summary.website_connectivity?.global || []);
            renderNetworkInfo(summary.network_info || {});
            refreshProxyDisplay();
        }

        async function loadSummary(showOverlay = false, refresh = false) {
            if (showOverlay) elements.overlay.style.display = 'flex';
            try {
                // refresh=true 时调用 probe/run 触发实际探测，否则只读缓存
                const url = refresh ? '/api/v1/probe/run' : '/api/v1/summary';
                const method = refresh ? 'POST' : 'GET';
                const response = await fetch(url, { method, cache: 'no-store' });
                if (!response.ok) {
                    throw new Error('HTTP ' + response.status);
                }
                const data = await response.json();
                renderSummary(data);
            } catch (error) {
                console.error(error);
                showToast(i18n('load_failed') + ': ' + error.message, 'error');
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
                showToast(i18n('refresh_failed'), 'error');
            } finally {
                state.fastRefreshing = false;
                if (showOverlay) elements.overlay.style.display = 'none';
                elements.refreshBtn.disabled = false;
            }
        }

        async function runWebsiteRefresh() {
            elements.websiteRefreshBtn.disabled = true;
            elements.websiteStatus.textContent = `${i18n('checking')}...`;
            try {
                const response = await fetch('/api/v1/connectivity/websites/run', { method: 'POST' });
                if (!response.ok) throw new Error(`HTTP ${response.status}`);
                const websiteData = await response.json();
                updateConnectivityTable(elements.domesticTable, websiteData.domestic || []);
                updateConnectivityTable(elements.globalTable, websiteData.global || []);
                elements.websiteStatus.textContent = '';
                if (state.summary) {
                    state.summary.website_connectivity = websiteData;
                }
            } catch (error) {
                console.error(error);
                elements.websiteStatus.textContent = i18n('check_failed');
                showToast(i18n('speedtest_failed'), 'error');
            } finally {
                elements.websiteRefreshBtn.disabled = false;
            }
        }

        async function loadSpeedConfig() {
            try {
                const response = await fetch('/api/v1/speed/config', { cache: 'no-store' });
                if (!response.ok) throw new Error(`HTTP ${response.status}`);
                const data = await response.json();
                state.speedConfig = {
                    broadband_duration_sec: data.broadband_duration_sec || 10,
                    local_transfer_duration_sec: data.local_transfer_duration_sec || 10,
                    local_transfer_payload_mb: data.local_transfer_payload_mb || 32
                };
            } catch (error) {
                console.error(error);
            }

            elements.broadbandNote.textContent = `${i18n('broadband_note_prefix')}${state.speedConfig.broadband_duration_sec}${i18n('seconds_unit')}`;
            elements.transferNote.textContent = `${i18n('transfer_note_prefix')}${state.speedConfig.local_transfer_duration_sec}${i18n('seconds_unit')}`;
        }

        async function loadSpeedHistory() {
            try {
                const [broadband, localTransfer] = await Promise.all([
                    fetch('/api/v1/speed/broadband/history', { cache: 'no-store' }).then(r => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); }),
                    fetch('/api/v1/speed/local/history', { cache: 'no-store' }).then(r => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); })
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
                        <small>${escapeHtml(item.timestamp) || '--'}${item.provider ? ' · ' + escapeHtml(item.provider) : ''}${item.node_source ? ' · ' + escapeHtml(item.node_source) : ''}</small>
                    </div>
                    <div style="text-align: right">
                        <small>${i18n('latency_col')} ${item.latency_ms || 0} ms</small><br>
                        <small>${i18n('total_duration')} ${formatDurationMS(item.stage_durations?.total_ms)}</small>
                    </div>
                </div>
            `).join('') || `<div class="history-item"><small>${i18n('no_history')}</small></div>`;
        }

        function renderTransferHistory(items) {
            elements.transferHistory.innerHTML = items.map(item => `
                <div class="history-item">
                    <div class="history-item-info">
                        <span class="history-item-value">${item.download_mbps?.toFixed?.(2) || '0.00'} / ${item.upload_mbps?.toFixed?.(2) || '0.00'} <small>Mbps</small></span>
                        <small>${escapeHtml(item.timestamp) || '--'} · ${i18n('total')} ${formatMB(item.payload_mb || ((item.download_mb || 0) + (item.upload_mb || 0)))}</small>
                    </div>
                    <div style="text-align: right">
                        <small>RTT ${item.rtt_min_ms || 0}/${item.rtt_avg_ms || item.round_trip_latency_ms || 0}/${item.rtt_max_ms || 0} ms</small><br>
                        <small>${i18n('duration')} ${formatDurationMS(item.duration_ms)}</small>
                    </div>
                </div>
            `).join('') || `<div class="history-item"><small>${i18n('no_history')}</small></div>`;
        }

        function resetBroadbandDetails() {
            setText(elements.broadbandNodeName);
            setText(elements.broadbandNodeProvider);
            setText(elements.broadbandNodeRegion);
            setText(elements.broadbandNodeSource);
            setText(elements.broadbandDurationNode);
            setText(elements.broadbandDurationLatency);
            setText(elements.broadbandDurationDownload);
            setText(elements.broadbandDurationUpload);
            setText(elements.broadbandDurationTotal);
            setText(elements.broadbandFailureStage);
            setText(elements.broadbandFailureReason);
        }

        function renderBroadbandDetails(result = {}) {
            const durations = result.stage_durations || {};
            setText(elements.broadbandNodeName, result.server_name || result.server_region || '--');
            setText(elements.broadbandNodeProvider, result.provider || '--');
            setText(elements.broadbandNodeRegion, result.server_country || result.server_region || '--');
            setText(elements.broadbandNodeSource, result.node_source ? `${result.node_source}${result.domestic_node ? ` · ${i18n('domestic_node')}` : ''}` : '--');
            setText(elements.broadbandDurationNode, formatDurationMS(durations.node_selection_ms));
            setText(elements.broadbandDurationLatency, formatDurationMS(durations.latency_test_ms));
            setText(elements.broadbandDurationDownload, formatDurationMS(durations.download_test_ms));
            setText(elements.broadbandDurationUpload, formatDurationMS(durations.upload_test_ms));
            setText(elements.broadbandDurationTotal, formatDurationMS(durations.total_ms));
            setText(elements.broadbandFailureStage, result.failure_stage ? (broadbandFailureStageMap[result.failure_stage] || result.failure_stage) : '--');
            setText(elements.broadbandFailureReason, result.failure_reason || result.error || '--');
        }

        function resetTransferDetails() {
            setText(elements.transferRTTMin);
            setText(elements.transferRTTAvg);
            setText(elements.transferRTTMax);
            setText(elements.transferRTTJitter);
            setText(elements.transferDownloadBytes);
            setText(elements.transferUploadBytes);
            setText(elements.transferTotalBytes);
            setText(elements.transferDuration);
        }

        function renderTransferDetails(stats = {}) {
            const downloadMB = Number(stats.download_mb) || 0;
            const uploadMB = Number(stats.upload_mb) || 0;
            setText(elements.transferRTTMin, formatMS(stats.rtt_min_ms));
            setText(elements.transferRTTAvg, formatMS(stats.rtt_avg_ms));
            setText(elements.transferRTTMax, formatMS(stats.rtt_max_ms));
            setText(elements.transferRTTJitter, formatMS(stats.jitter_ms));
            setText(elements.transferDownloadBytes, formatMB(downloadMB));
            setText(elements.transferUploadBytes, formatMB(uploadMB));
            setText(elements.transferTotalBytes, formatMB(stats.payload_mb || (downloadMB + uploadMB)));
            setText(elements.transferDuration, formatDurationMS(stats.duration_ms));
        }

        function resetBroadbandMetrics() {
            elements.broadbandStage.textContent = i18n('idle');
            elements.broadbandProgress.textContent = '0%';
            elements.broadbandNote.textContent = `${i18n('broadband_note_prefix')}${state.speedConfig.broadband_duration_sec}${i18n('seconds_unit')}`;
            setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Idle', i18n('standby'));
            setSpeedPanelMode('broadband', 'Idle');
            elements.broadbandDownload.textContent = '--';
            elements.broadbandUpload.textContent = '--';
            elements.broadbandLatency.textContent = '--';
            elements.broadbandJitter.textContent = '--';
            resetBroadbandDetails();
        }

        function resetTransferMetrics() {
            elements.transferStage.textContent = i18n('idle');
            elements.transferProgress.textContent = '0%';
            elements.transferNote.textContent = `${i18n('transfer_note_prefix')}${state.speedConfig.local_transfer_duration_sec}${i18n('seconds_unit')}`;
            setPrimaryStatus(elements.transferPrimaryMode, elements.transferPrimaryCaption, 'Idle', i18n('standby'));
            setSpeedPanelMode('transfer', 'Idle');
            elements.transferDownload.textContent = '--';
            elements.transferUpload.textContent = '--';
            elements.transferLatency.textContent = '--';
            elements.transferJitter.textContent = '--';
            resetTransferDetails();
        }

        function renderBroadbandTask(task = {}) {
            elements.broadbandStage.textContent = broadbandStageMap[task.stage] || i18n('idle');
            elements.broadbandProgress.textContent = `${Math.max(0, Math.min(100, Math.round(task.progress_percent || 0)))}%`;
            elements.broadbandNote.textContent = task.message || broadbandStageMap[task.stage] || i18n('standby');
            elements.broadbandLatency.textContent = formatMS(task.result?.latency_ms);
            elements.broadbandJitter.textContent = formatMS(task.result?.jitter_ms);
            elements.broadbandDownload.textContent = formatMbps(task.result?.download_mbps);
            elements.broadbandUpload.textContent = formatMbps(task.result?.upload_mbps);
            renderBroadbandDetails(task.result || {});

            if (task.stage === 'latency') {
                setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Ping', task.message || i18n('latency_sampling'));
                setSpeedPanelMode('broadband', 'Ping');
                return;
            }
            if (task.stage === 'download') {
                setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Download', task.message || i18n('downloading'));
                setSpeedPanelMode('broadband', 'Download');
                return;
            }
            if (task.stage === 'upload') {
                setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Upload', task.message || i18n('uploading'));
                setSpeedPanelMode('broadband', 'Upload');
                return;
            }
            setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Result', task.message || i18n('speedtest_complete'));
            setSpeedPanelMode('broadband', 'Result');
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
            elements.broadbandNote.textContent = i18n('started_broadband');

            try {
                const response = await fetch('/api/v1/speed/broadband/start', { method: 'POST' });
                const task = await response.json();
                renderBroadbandTask(task);
                startBroadbandPolling();
            } catch (error) {
                console.error(error);
                state.runningTest = null;
                updateWindowControls();
                elements.broadbandNote.textContent = i18n('broadband_start_failed');
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
                    elements.broadbandStage.textContent = i18n('canceled');
                    elements.broadbandNote.textContent = i18n('speedtest_stopped');
                    elements.broadbandProgress.textContent = '0%';
                    setPrimaryStatus(elements.broadbandPrimaryMode, elements.broadbandPrimaryCaption, 'Stopped', i18n('manual_stop'));
                    setSpeedPanelMode('broadband', 'Stopped');
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
            const durationSec = Math.max(1, Math.min(60, Number(state.speedConfig.local_transfer_duration_sec) || 10));
            
            s.setParameter("url_dl", `/api/v1/speed/local/download?sec=${durationSec}`);
            s.setParameter("url_ul", "/api/v1/speed/local/upload");
            s.setParameter("url_ping", "/api/v1/speed/local/ping");
            s.setParameter("url_getIp", "/api/v1/summary");
            s.setParameter("worker_path", "/speedtest_worker.js");
            s.setParameter("test_order", "P_D_U");
            s.setParameter("time_dl_max", durationSec);
            s.setParameter("time_ul_max", durationSec);
            s.setParameter("time_auto", false);
            s.setParameter("count_ping", 10);
            
            let lastData = {};
            let transferStartedAt = Date.now();
            s.onupdate = (data) => {
                if (state.runningTest !== 'transfer') return;
                lastData = data;
                const rtt = summarizeRTT(data.pingSamples || []);
                const transferStats = {
                    rtt_min_ms: rtt.min,
                    rtt_avg_ms: rtt.avg || finiteNumber(data.pingStatus),
                    rtt_max_ms: rtt.max,
                    jitter_ms: finiteNumber(data.jitterStatus),
                    download_mb: bytesToMB(data.dlBytes),
                    upload_mb: bytesToMB(data.ulBytes),
                    duration_ms: (Number(data.dlDuration) || 0) + (Number(data.ulDuration) || 0)
                };
                transferStats.payload_mb = transferStats.download_mb + transferStats.upload_mb;
                renderTransferDetails(transferStats);
                const testStateMap = {
                    0: { stage: i18n('preparing'), progress: 0, mode: 'Idle' },
                    1: { stage: i18n('dl_speedtest'), progress: 15 + (data.dlProgress || 0) * 40, mode: 'Download' },
                    2: { stage: i18n('latency'), progress: 5 + (data.pingProgress || 0) * 10, mode: 'Ping' },
                    3: { stage: i18n('ul_speedtest'), progress: 55 + (data.ulProgress || 0) * 45, mode: 'Upload' }
                };
                
                const current = testStateMap[data.testState];
                if (current) {
                    elements.transferStage.textContent = current.stage;
                    elements.transferProgress.textContent = `${Math.round(current.progress)}%`;
                    
                    let speed = 0, unit = 'Mbps', caption = '';
                    if (current.mode === 'Download') {
                        speed = finiteNumber(data.dlStatus);
                        elements.transferDownload.textContent = speed.toFixed(1);
                        caption = `${i18n('downloading')}... ${speed.toFixed(2)} Mbps`;
                    } else if (current.mode === 'Upload') {
                        speed = finiteNumber(data.ulStatus);
                        elements.transferUpload.textContent = speed.toFixed(1);
                        caption = `${i18n('uploading')}... ${speed.toFixed(2)} Mbps`;
                    } else if (current.mode === 'Ping') {
                        speed = finiteNumber(data.pingStatus);
                        elements.transferLatency.textContent = formatMS(speed);
                        elements.transferJitter.textContent = formatMS(finiteNumber(data.jitterStatus));
                        unit = 'ms';
                        caption = `${i18n('latency_sampling')}... ${speed.toFixed(0)} ms`;
                    }
                    
                    setPrimaryStatus(elements.transferPrimaryMode, elements.transferPrimaryCaption, current.mode, caption);
                    setSpeedPanelMode('transfer', current.mode);
                }
            };

            s.onend = async (aborted) => {
                if (aborted) {
                    finishTransferRun();
                    return;
                }

                const downloadMbps = finiteNumber(lastData.dlStatus, finiteNumber(elements.transferDownload.textContent));
                const uploadMbps = finiteNumber(lastData.ulStatus, finiteNumber(elements.transferUpload.textContent));
                const pingMs = finiteNumber(lastData.pingStatus, finiteNumber(elements.transferLatency.textContent));
                const jitterMs = finiteNumber(lastData.jitterStatus, finiteNumber(elements.transferJitter.textContent));
                const rtt = summarizeRTT(lastData.pingSamples || []);
                const transferStats = {
                    download_mb: bytesToMB(lastData.dlBytes),
                    upload_mb: bytesToMB(lastData.ulBytes),
                    duration_ms: ((Number(lastData.dlDuration) || 0) + (Number(lastData.ulDuration) || 0)) || (Date.now() - transferStartedAt),
                    rtt_min_ms: rtt.min || Math.round(pingMs),
                    rtt_avg_ms: rtt.avg || Math.round(pingMs),
                    rtt_max_ms: rtt.max || Math.round(pingMs),
                    jitter_ms: Math.round(jitterMs)
                };
                transferStats.payload_mb = transferStats.download_mb + transferStats.upload_mb;
                renderTransferDetails(transferStats);

                elements.transferStage.textContent = i18n('complete');
                elements.transferProgress.textContent = '100%';
                elements.transferNote.textContent = i18n('transfer_done');
                setPrimaryStatus(elements.transferPrimaryMode, elements.transferPrimaryCaption, 'Result', i18n('speedtest_complete'));
                setSpeedPanelMode('transfer', 'Result');

                try {
                    await fetch('/api/v1/speed/local/result', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
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
                        })
                    });
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
                elements.transferStage.textContent = i18n('start_failed');
                elements.transferNote.textContent = i18n('transfer_start_failed');
                setPrimaryStatus(elements.transferPrimaryMode, elements.transferPrimaryCaption, 'Error', i18n('speedtest_start_failed'));
                setSpeedPanelMode('transfer', 'Error');
                finishTransferRun();
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
                elements.transferStage.textContent = i18n('canceled');
                elements.transferNote.textContent = i18n('transfer_stopped');
                elements.transferProgress.textContent = '0%';
                setPrimaryStatus(elements.transferPrimaryMode, elements.transferPrimaryCaption, 'Stopped', i18n('manual_stop'));
                setSpeedPanelMode('transfer', 'Stopped');
            }
        }

        async function openWindow(name) {
            if (state.runningTest && state.runningTest !== name) return;

            elements.settingsWindow.classList.remove('active');
            elements.broadbandWindow.classList.remove('active');
            elements.transferWindow.classList.remove('active');
            elements.notificationSettingsWindow?.classList.remove('active');
            elements.notifyTemplateWindow?.classList.remove('active');
            document.getElementById('traffic-settings-window')?.classList.remove('active');

            if (name === 'settings') {
                elements.settingsWindow.classList.add('active');
            } else if (name === 'broadband') {
                elements.broadbandWindow.classList.add('active');
            } else if (name === 'transfer') {
                elements.transferWindow.classList.add('active');
            } else if (name === 'notification-settings') {
                elements.notificationSettingsWindow?.classList.add('active');
                loadLazycatDevices();
            }

            elements.backdrop.classList.add('active');
            lockModalScroll();
            state.activeWindow = name;
            updateWindowControls();
            if (name === 'broadband' || name === 'transfer') {
                await loadSpeedHistory();
            }
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
            elements.notificationSettingsWindow?.classList.remove('active');
            elements.notifyTemplateWindow?.classList.remove('active');
            document.getElementById('traffic-settings-window')?.classList.remove('active');
            elements.backdrop.classList.remove('active');
            unlockModalScroll();
            state.activeWindow = null;
            updateWindowControls();
        }

        function openTraceWindow() {
            elements.traceWindow?.classList.add('active');
            elements.traceBackdrop?.classList.add('active');
            lockModalScroll();
        }

        function closeTraceWindow() {
            elements.traceWindow?.classList.remove('active');
            elements.traceBackdrop?.classList.remove('active');
            if (state.tracePoller) {
                clearInterval(state.tracePoller);
                state.tracePoller = null;
            }
            unlockModalScroll();
        }

        function lockModalScroll() {
            state.modalLockCount += 1;
            if (state.modalLockCount > 1) return;
            state.modalScrollY = window.scrollY || document.documentElement.scrollTop || 0;
            const hasStableGutter = window.CSS?.supports?.('scrollbar-gutter: stable');
            const scrollbarWidth = hasStableGutter ? 0 : Math.max(0, window.innerWidth - document.documentElement.clientWidth);
            document.documentElement.style.setProperty('--scrollbar-compensation', `${scrollbarWidth}px`);
            document.body.style.top = `-${state.modalScrollY}px`;
            document.body.classList.add('modal-scroll-locked');
        }

        function unlockModalScroll() {
            if (state.modalLockCount > 0) {
                state.modalLockCount -= 1;
            }
            if (state.modalLockCount > 0) return;
            const y = state.modalScrollY || 0;
            document.body.classList.remove('modal-scroll-locked');
            document.body.style.top = '';
            document.documentElement.style.removeProperty('--scrollbar-compensation');
            window.scrollTo(0, y);
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
            elements.closeNotificationSettings?.addEventListener('click', closeCurrentWindow);
            elements.openNotificationSettings?.addEventListener('click', () => openWindow('notification-settings'));
            elements.saveNotificationSettings?.addEventListener('click', saveSettings);
            elements.openNotifyTemplate?.addEventListener('click', () => {
                const w = elements.notifyTemplateWindow;
                if (!w) return;
                w.classList.add('active');
                elements.notifyTemplateTitle.value = state.settings.notify_template_title || '';
                elements.notifyTemplateBody.value = state.settings.notify_template_body || '';
            });
            elements.closeNotifyTemplate?.addEventListener('click', () => {
                elements.notifyTemplateWindow?.classList.remove('active');
            });
            elements.saveNotifyTemplate?.addEventListener('click', async () => {
                state.settings.notify_template_title = elements.notifyTemplateTitle.value.trim();
                state.settings.notify_template_body = elements.notifyTemplateBody.value.trim();
                await saveSettings();
                elements.notifyTemplateWindow?.classList.remove('active');
            });
            const deviceTrigger = document.getElementById('notification-device-trigger');
            const devicePanel = document.getElementById('notification-device-panel');
            if (deviceTrigger && devicePanel) {
                deviceTrigger.addEventListener('click', (e) => {
                    e.stopPropagation();
                    devicePanel.classList.toggle('open');
                });
                document.addEventListener('click', (e) => {
                    if (!devicePanel.contains(e.target) && !deviceTrigger.contains(e.target)) {
                        devicePanel.classList.remove('open');
                    }
                });
            }
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
            elements.settingBackgroundMonitorEnabled?.addEventListener('change', () => {
                state.settings.background_monitor_enabled = !!elements.settingBackgroundMonitorEnabled.checked;
                applySettingsToForm();
            });
            elements.settingNotificationsEnabled?.addEventListener('change', () => {
                state.settings.notifications_enabled = !!elements.settingNotificationsEnabled.checked;
                applySettingsToForm();
            });
            elements.settingClientNotificationEnabled?.addEventListener('change', () => {
                state.settings.client_notification_enabled = !!elements.settingClientNotificationEnabled.checked;
                applySettingsToForm();
            });
            elements.settingNotifyAbnormalTraffic?.addEventListener('change', () => {
                state.settings.notify_abnormal_traffic = !!elements.settingNotifyAbnormalTraffic.checked;
                applySettingsToForm();
            });
            elements.settingNotifyEgressChange?.addEventListener('change', () => {
                state.settings.notify_egress_change = !!elements.settingNotifyEgressChange.checked;
            });
            elements.settingNotifyConnectivityChange?.addEventListener('change', () => {
                state.settings.notify_connectivity_change = !!elements.settingNotifyConnectivityChange.checked;
            });
            elements.settingBarkEnabled?.addEventListener('change', () => {
                state.settings.bark_enabled = !!elements.settingBarkEnabled.checked;
                if (!state.settings.bark_enabled) {
                    state.settings.client_notification_enabled = true;
                }
                applySettingsToForm();
            });
            elements.testBarkNotification?.addEventListener('click', testBarkNotification);
            elements.settingDNDEnabled?.addEventListener('change', () => {
                state.settings.dnd_enabled = !!elements.settingDNDEnabled.checked;
            });
            elements.settingDNDStart?.addEventListener('change', () => {
                state.settings.dnd_start = elements.settingDNDStart.value || '22:00';
            });
            elements.settingDNDEnd?.addEventListener('change', () => {
                state.settings.dnd_end = elements.settingDNDEnd.value || '08:00';
            });
        }

        function initWithRetry(maxRetries = 3) {
            bindControls();
            resetBroadbandMetrics();
            resetTransferMetrics();

            // Phase 1: load cached data immediately (fast, no overlay)
            Promise.all([
                loadSpeedConfig(),
                loadSummary(false, false),
                loadSpeedHistory(),
                loadSettings()
            ]).then(() => {
                updateWindowControls();
                initNICRealtime();
                // Re-render traffic table now that settings are loaded
                if (state.appTrafficInitialized) {
                    const btn = document.getElementById('app-traffic-refresh-btn');
                    if (btn) btn.click();
                }
            });

            // Phase 2: trigger actual probes in background (slow, no blocking)
            loadSummary(false, true).then(() => {
                if (!state.summary || !state.summary.ready) {
                    if (maxRetries > 0) {
                        setTimeout(() => initWithRetry(maxRetries - 1), 2000);
                    }
                }
            });

            initSSE();
            initTrace();
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
            if (!n || n <= 0) return '0 B';
            const units = ['B', 'KB', 'MB', 'GB', 'TB'];
            let i = 0;
            let v = n;
            while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
            return (i === 0 ? v.toString() : v.toFixed(v < 10 ? 2 : 1)) + ' ' + units[i];
        }

        function renderEgressLookups(data) {
            state.egressData = data;
            const domesticEl = document.getElementById('egress-domestic-list');
            const globalEl = document.getElementById('egress-global-list');
            const statusEl = document.getElementById('egress-status');
            if (!globalEl) return;

            const itemHTML = (lu, options = {}) => {
                if (lu.error) {
                    return `
                        <div class="egress-item error">
                            <span class="egress-duration">${Number.isFinite(lu.duration_ms) && lu.duration_ms > 0 ? `${lu.duration_ms} ms` : ''}</span>
                            <span class="egress-meta">${i18n('error')}: ${escapeHtml(lu.error)}</span>
                        </div>`;
                }
                const geoParts = [
                    ['country', lu.country],
                    ['region', lu.region],
                    ['city', lu.city]
                ].filter(([, value]) => value)
                    .map(([kind, value]) => options.translateGeo ? translateGeoName(value, kind) : value);
                const geo = geoParts.join(' · ');
                const meta = [geo, lu.asn, lu.isp].filter(Boolean).join('  ');
                return `
                    <div class="egress-item">
                        <span class="egress-ip">${escapeHtml(lu.ip) || '--'}</span>
                        <span class="egress-duration">${Number.isFinite(lu.duration_ms) && lu.duration_ms > 0 ? `${lu.duration_ms} ms` : ''}</span>
                        <span class="egress-meta">${escapeHtml(meta) || '—'}</span>
                    </div>`;
            };

            const lookup = (data.lookups || []).find(x => x.scope === 'global' && (x.ip || x.error));
            const domestic = data.domestic_ip?.ipv4 || {};
            if (domesticEl) {
                domesticEl.innerHTML = domestic.ip
                    ? itemHTML({
                        provider: domestic.source || 'domestic',
                        ip: domestic.ip,
                        duration_ms: 0,
                        country: '',
                        region: domestic.location,
                        city: '',
                        isp: domestic.isp,
                        error: domestic.error
                    })
                    : `<div class="egress-item"><small>${i18n('no_data')}</small></div>`;
            }
            globalEl.innerHTML = lookup ? itemHTML(lookup, { translateGeo: true }) : `<div class="egress-item"><small>${i18n('no_data')}</small></div>`;
            statusEl.textContent = data.generated_at ? `${i18n('queried_at')} ${data.generated_at}` : i18n('waiting');
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

            if (ipv4IP) ipv4IP.textContent = v4.ip || i18n('not_detected');
            if (ipv4Location) ipv4Location.textContent = v4.error || v4.location || i18n('waiting');

            if (ipv6IP) ipv6IP.textContent = v6.ip || i18n('not_detected');
            if (ipv6Location) ipv6Location.textContent = v6.error || v6.location || i18n('waiting');

            const portProbe = v6.port_probe || {};
            if (portProbe.status === 'reachable') {
                setIdentityBadge(ipv6Port, `${i18n('high_port_reachable')} ${portProbe.latency_ms || 0} ms`, 'ok');
            } else if (portProbe.status === 'blocked') {
                setIdentityBadge(ipv6Port, i18n('high_port_blocked'), 'fail');
            } else if (portProbe.status === 'closed') {
                setIdentityBadge(ipv6Port, i18n('probe_closed'), 'warn');
            } else if (portProbe.status === 'unavailable') {
                setIdentityBadge(ipv6Port, i18n('high_port_not_checked'), 'warn');
            } else {
                setIdentityBadge(ipv6Port, i18n('high_port_check_failed'), 'fail');
            }
        }

        async function initEgressLookups() {
            if (state.egressInitialized) return;
            state.egressInitialized = true;
            const btn = document.getElementById('egress-refresh-btn');
            if (!btn) return;
            const statusEl = document.getElementById('egress-status');

            const load = async (force) => {
                statusEl.textContent = force ? `${i18n('querying')}...` : `${i18n('loading')}...`;
                btn.disabled = true;
                try {
                    const resp = await fetch('/api/v1/network/egress-lookups', {
                        method: force ? 'POST' : 'GET',
                        cache: 'no-store'
                    });
                    renderEgressLookups(await resp.json());
                } catch (e) {
                    statusEl.textContent = i18n('load_failed') + ': ' + e.message;
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

            const sortAppTrafficRows = (a, b, key, direction) => {
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

            const isNetwatchBridge = (b) => {
                const id = (b.app_id || '').toLowerCase();
                const proj = (b.project || '').toLowerCase();
                return id === 'cloud.lazycat.app.netwatch' || id === 'netwatch' || proj.includes('netwatch');
            };

            const renderTraffic = (data) => {
                latestTrafficData = data;
                const list = Array.isArray(data.bridges) ? data.bridges.filter(b => !isNetwatchBridge(b)) : [];
                updateSortHeaders();
                if (list.length === 0) {
                    tbody.innerHTML = `<tr><td colspan="4" class="placeholder">${i18n('no_app_data')}</td></tr>`;
                } else {
                    const { key, direction } = state.appTrafficSort;
                    list.sort((a, b) => sortAppTrafficRows(a, b, key, direction));
                    tbody.innerHTML = list.map(b => {
                        const total = (b.rx_bytes || 0) + (b.tx_bytes || 0);
                        const title = escapeHtml(b.app_title || shortAppName(b.app_id) || b.bridge);
                        const iconHtml = b.icon ? `<img class="app-icon" src="${escapeHtml(b.icon)}" alt="" loading="lazy" onerror="this.style.display='none'">` : '';
                        const statusLine = b.status_text ? `<div class="app-status-text">${escapeHtml(b.status_text)}</div>` : '';
                        let nameHtml;
                        if (b.app_title) {
                            nameHtml = `<strong>${escapeHtml(b.app_title)}</strong>${statusLine}`;
                        } else if (b.app_id) {
                            nameHtml = `<strong>${escapeHtml(shortAppName(b.app_id))}</strong>${statusLine}<div class="app-status-text">${escapeHtml(b.app_id)}</div>`;
                        } else if (b.project) {
                            nameHtml = `<small style="color:var(--text-muted)">${escapeHtml(b.project)}</small>`;
                        } else {
                            nameHtml = `<small style="color:var(--text-muted)">${escapeHtml(b.bridge)}</small>`;
                        }
                        return `
                            <tr>
                                <td class="col-app"><div class="app-cell">${iconHtml}<div class="app-cell-info">${nameHtml}</div></div></td>
                                <td class="col-rx">${formatBytes(b.rx_bytes || 0)}</td>
                                <td class="col-tx">${formatBytes(b.tx_bytes || 0)}</td>
                                <td class="col-total">${formatBytes(total)}</td>
                            </tr>
                        `;
                    }).join('');
                }
                if (statusEl) statusEl.textContent = data.generated_at ? `${i18n('sampled_at')} ${data.generated_at}` : '';
                if (noteEl && data.note) noteEl.textContent = data.note;
            };

            const load = async () => {
                if (btn) btn.disabled = true;
                if (statusEl) statusEl.textContent = `${i18n('sampling')}...`;
                try {
                    const resp = await fetch('/api/v1/network/app-traffic', { cache: 'no-store' });
                    renderTraffic(await resp.json());
                } catch (e) {
                    if (statusEl) statusEl.textContent = `${i18n('sampling_failed')}: ${e.message}`;
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

            // --- Traffic settings floating window ---
            const settingsBtn = document.getElementById('traffic-settings-btn');
            const trafficSettingsWindow = document.getElementById('traffic-settings-window');
            const closeTrafficSettingsBtn = document.getElementById('close-traffic-settings-window');

            function populateTrafficSettings() {
                const el = (id) => document.getElementById(id);
                if (el('ts-sampling-enabled')) el('ts-sampling-enabled').checked = !!state.settings.traffic_sampling_enabled;
                if (el('ts-global-interval')) {
                    el('ts-global-interval').value = String(state.settings.traffic_sampling_interval_sec || 60);
                    window.syncCustomSelect?.(el('ts-global-interval'));
                }
                const perAppList = el('ts-per-app-list');
                if (perAppList && latestTrafficData?.bridges) {
                    const perApp = state.settings.per_app_sampling_interval || {};
                    perAppList.innerHTML = latestTrafficData.bridges.filter(b => !isNetwatchBridge(b)).map(b => {
                        const title = b.app_title || shortAppName(b.app_id) || b.bridge;
                        const currentVal = perApp[b.bridge] || '';
                        const options = [
                            { v: '', l: i18n('follow_global') },
                            { v: '10', l: `10 ${i18n('seconds_short')}` },
                            { v: '30', l: `30 ${i18n('seconds_short')}` },
                            { v: '60', l: `60 ${i18n('seconds_short')}` },
                            { v: '120', l: `120 ${i18n('seconds_short')}` },
                            { v: '300', l: `300 ${i18n('seconds_short')}` },
                        ].map(o => `<option value="${o.v}" ${String(currentVal) === o.v ? 'selected' : ''}>${o.l}</option>`).join('');
                        return `<div class="ts-per-app-item" data-bridge="${b.bridge}">
                            <span class="ts-app-name" title="${b.bridge}">${title}</span>
                            <select class="ts-per-app-select">${options}</select>
                        </div>`;
                    }).join('');
                    window.enhanceSelects?.(perAppList);
                }
            }

            function openTrafficSettings() {
                if (!trafficSettingsWindow) return;
                populateTrafficSettings();
                trafficSettingsWindow.classList.add('active');
                document.getElementById('window-backdrop').classList.add('active');
                lockModalScroll();
            }

            async function saveTrafficSettings() {
                const perApp = {};
                const perAppList = document.getElementById('ts-per-app-list');
                if (perAppList) {
                    perAppList.querySelectorAll('.ts-per-app-item').forEach(item => {
                        const bridge = item.dataset.bridge;
                        const select = item.querySelector('.ts-per-app-select');
                        if (select && select.value) {
                            perApp[bridge] = parseInt(select.value, 10);
                        }
                    });
                }
                const el = (id) => document.getElementById(id);
                const payload = {
                    refresh_interval_sec: state.settings.refresh_interval_sec,
                    broadband_domestic_only: state.settings.broadband_domestic_only,
                    nic_realtime_enabled: state.settings.nic_realtime_enabled,
                    nic_realtime_interval_sec: state.settings.nic_realtime_interval_sec,
                    chart_time_label_interval: state.settings.chart_time_label_interval,
                    traffic_sampling_enabled: !!el('ts-sampling-enabled')?.checked,
                    traffic_sampling_interval_sec: parseInt(el('ts-global-interval')?.value || '60', 10) || 60,
                    per_app_sampling_interval: perApp,
                    persistent_traffic_bridges: state.settings.persistent_traffic_bridges || [],
                    background_monitor_enabled: state.settings.background_monitor_enabled,
                    background_monitor_interval_sec: state.settings.background_monitor_interval_sec,
                    notifications_enabled: state.settings.notifications_enabled,
                    client_notification_enabled: state.settings.client_notification_enabled !== false,
                    notify_abnormal_traffic: state.settings.notify_abnormal_traffic,
                    notify_egress_change: state.settings.notify_egress_change,
                    notify_connectivity_change: state.settings.notify_connectivity_change,
                    notify_lan_device_change: state.settings.notify_lan_device_change,
                    lan_device_offline_after_sec: state.settings.lan_device_offline_after_sec || 180,
                    lan_device_online_after_sec: state.settings.lan_device_online_after_sec || 0,
                    lan_device_offline_notify_delay_sec: state.settings.lan_device_offline_notify_delay_sec ?? 120,
                    lan_device_online_notify_delay_sec: state.settings.lan_device_online_notify_delay_sec ?? 120,
                    abnormal_traffic_threshold_mbps: state.settings.abnormal_traffic_threshold_mbps,
                    bark_enabled: state.settings.bark_enabled,
                    bark_server_url: state.settings.bark_server_url,
                    bark_device_key: state.settings.bark_device_key,
                    bark_group: state.settings.bark_group
                };
                try {
                    const resp = await fetch('/api/v1/settings', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(payload)
                    });
                    if (!resp.ok) throw new Error('save failed');
                    const saved = await resp.json();
                    state.settings = { ...state.settings, ...saved };
                    trafficSettingsWindow.classList.remove('active');
                    document.getElementById('window-backdrop').classList.remove('active');
                    unlockModalScroll();
                    updateTrafficAnalysisLink();
                    // Re-render traffic with saved sampling settings.
                    if (latestTrafficData) renderTraffic(latestTrafficData);
                    showToast(i18n('traffic_settings_saved'), 'success');
                } catch (e) {
                    showToast(i18n('save_settings_fail') + ': ' + e.message, 'error');
                }
            }

            if (settingsBtn) settingsBtn.addEventListener('click', openTrafficSettings);
            if (closeTrafficSettingsBtn) closeTrafficSettingsBtn.addEventListener('click', () => {
                trafficSettingsWindow?.classList.remove('active');
                document.getElementById('window-backdrop').classList.remove('active');
                unlockModalScroll();
            });
            if (document.getElementById('ts-save-btn')) document.getElementById('ts-save-btn').addEventListener('click', saveTrafficSettings);
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

            window.renderNICRealtime = (data) => {
                if (!data.nics || data.nics.length === 0) {
                    listEl.innerHTML = `<div class="nic-realtime-item"><small>${i18n('no_monitored_nics')}</small></div>`;
                    statusEl.textContent = i18n('no_data');
                    return;
                }
                listEl.innerHTML = data.nics.map(n => {
                    const isUp = n.oper_state ? n.oper_state === 'up' : n.present;
                    return `
                    <div class="nic-realtime-item">
                        <div class="nic-realtime-head">
                            <span class="nic-realtime-name">${escapeHtml(n.name)}</span>
                            <span class="nic-realtime-badge ${isUp ? 'online' : 'offline'}">${isUp ? 'UP' : 'DOWN'}</span>
                        </div>
                        <div class="nic-realtime-rows">
                            <div class="nic-realtime-cell">
                                <span class="nic-realtime-label">↓ ${i18n('rx')}</span>
                                <span class="nic-realtime-value rx">${formatBitsPerSec(n.rx_bps)}</span>
                            </div>
                            <div class="nic-realtime-cell">
                                <span class="nic-realtime-label">↑ ${i18n('tx')}</span>
                                <span class="nic-realtime-value tx">${formatBitsPerSec(n.tx_bps)}</span>
                            </div>
                        </div>
                        <div class="nic-realtime-total">${i18n('cumulative')} ↓ ${formatBytes(n.rx_total)} / ↑ ${formatBytes(n.tx_total)}</div>
                    </div>`;
                }).join('');
                statusEl.textContent = `${i18n('sampled_at')} ${data.timestamp || ''}`;
            };

            const tick = async (manual = false) => {
                try {
                    if (manual && elements.nicRealtimeRefreshBtn) elements.nicRealtimeRefreshBtn.disabled = true;
                    const resp = await fetch('/api/v1/network/realtime', { cache: 'no-store' });
                    if (!resp.ok) return;
                    window.renderNICRealtime(await resp.json());
                } catch (_) {
                    if (statusEl) statusEl.textContent = i18n('sampling_failed');
                } finally {
                    if (manual && elements.nicRealtimeRefreshBtn) elements.nicRealtimeRefreshBtn.disabled = false;
                }
            };
            updateNICRealtimeRefreshButton();
            elements.nicRealtimeRefreshBtn?.addEventListener('click', () => tick(true));
            tick();
            applySettingsToForm();
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
                es.addEventListener('notification', (ev) => {
                    try {
                        handleNotificationEvent(JSON.parse(ev.data));
                    } catch (_) {}
                });
                es.addEventListener('nic_realtime', (ev) => {
                    try {
                        if (state.settings.nic_realtime_enabled && typeof window.renderNICRealtime === 'function') {
                            window.renderNICRealtime(JSON.parse(ev.data));
                        }
                    } catch (_) {}
                });
                es.onerror = () => { /* browser auto-reconnects */ };
                state.sse = es;
            } catch (_) {}
        }

        async function getLazycatGateway() {
            if (state.lzcGatewayPromise) return state.lzcGatewayPromise;
            state.lzcGatewayPromise = (async () => {
                const Gateway = window.lzcAPIGateway || window.LazycatSDK?.lzcAPIGateway;
                if (!Gateway) {
                    throw new Error('Lazycat SDK 未加载，请确认当前客户端支持系统通知');
                }
                return new Gateway(window.location.origin, false);
            })();
            state.lzcGatewayPromise.catch(() => { state.lzcGatewayPromise = null; });
            return state.lzcGatewayPromise;
        }

        async function notifySelectedDevices(event) {
            if (state.notificationUnsupported) return;
            const selectedIDs = state.settings.notification_device_ids;
            const devices = state.lazycatDevices || [];
            const gateway = await getLazycatGateway();
            const payload = {
                title: event.title || 'Netwatch',
                body: event.body || '',
                deeplinkUrl: event.deeplink_url || 'lzc://app/cloud.lazycat.app.netwatch'
            };

            // If no devices selected, send to current device only
            if (!selectedIDs || selectedIDs.length === 0) {
                const device = await gateway.currentDevice;
                await device.notification.Notify(payload);
                return;
            }

            // Send to each selected device
            for (const dev of devices) {
                if (!selectedIDs.includes(dev.id)) continue;
                try {
                    const proxy = await gateway.getDeviceProxy(dev.id);
                    await proxy.notification.Notify(payload);
                } catch (err) {
                    console.debug(`notify device ${dev.id} failed`, err);
                }
            }
        }

        function markNotificationSeen(id) {
            if (!Number.isFinite(id) || id <= state.notificationLastID) return;
            state.notificationLastID = id;
            localStorage.setItem('netwatch_notification_last_id', String(id));
        }

        function handleNotificationEvent(event) {
            const id = Number(event?.id || 0);
            if (!id || id <= state.notificationLastID) return;
            if (!state.settings.notifications_enabled || state.settings.client_notification_enabled === false) {
                markNotificationSeen(id);
                return;
            }
            markNotificationSeen(id);
            notifySelectedDevices(event).catch((err) => {
                console.debug('lazycat notification unavailable', err);
                if (isNotificationUnsupportedError(err)) {
                    disableClientNotifications();
                }
            });
        }

        // --- Device management via Lazycat SDK ---
        async function loadLazycatDevices() {
            try {
                const gateway = await getLazycatGateway();
                const session = await gateway.session;
                const result = await gateway.devices.ListEndDevices({ uid: session.uid });
                const devices = (result.devices || []).map(d => ({
                    id: d.uniqueDeivceId || '',
                    name: d.remarkName || d.name || d.model || '',
                    model: d.model || '',
                    isOnline: !!d.isOnline,
                    isMobile: !!d.isMobile,
                    isCurrent: d.uniqueDeivceId === session.deviceId
                }));
                state.lazycatDevices = devices;
                renderNotificationDeviceList();
                return devices;
            } catch (err) {
                console.debug('load lazycat devices failed', err);
                state.lazycatDevices = [];
                renderNotificationDeviceList();
                return [];
            }
        }

        function renderNotificationDeviceList() {
            const container = document.getElementById('notification-device-list');
            const summaryEl = document.getElementById('notification-device-summary');
            if (!container) return;
            const devices = state.lazycatDevices || [];
            const selectedIDs = new Set(state.settings.notification_device_ids || []);

            if (devices.length === 0) {
                container.innerHTML = `<div class="placeholder">${i18n('no_devices_registered')}</div>`;
                if (summaryEl) summaryEl.textContent = i18n('no_devices_registered');
                return;
            }

            container.innerHTML = devices.map(dev => {
                const isSelected = selectedIDs.size === 0 || selectedIDs.has(dev.id);
                const label = escapeHtml(dev.name || dev.model || dev.id);
                return `
                    <label class="notification-device-item ${isSelected ? 'selected' : ''}">
                        <input type="checkbox" data-device-id="${escapeHtml(dev.id)}" ${isSelected ? 'checked' : ''}>
                        <div class="notification-device-status ${dev.isOnline ? 'online' : ''}"></div>
                        <div class="notification-device-name">${label}${dev.isCurrent ? ' (当前)' : ''}</div>
                    </label>
                `;
            }).join('');

            container.querySelectorAll('input[type="checkbox"]').forEach(cb => {
                cb.addEventListener('change', () => {
                    const checked = Array.from(container.querySelectorAll('input[type="checkbox"]:checked')).map(el => el.dataset.deviceId);
                    state.settings.notification_device_ids = checked;
                    updateDeviceSummary();
                });
            });

            updateDeviceSummary();

            function updateDeviceSummary() {
                if (!summaryEl) return;
                const total = devices.length;
                const checkedCount = container.querySelectorAll('input[type="checkbox"]:checked').length;
                if (checkedCount === total || checkedCount === 0) {
                    summaryEl.textContent = `全部设备 (${total})`;
                } else {
                    summaryEl.textContent = `${checkedCount} / ${total} 台`;
                }
            }
        }

        function isNotificationUnsupportedError(err) {
            const message = String(err?.message || err || '').toLowerCase();
            return message.includes('notificationservice') && (
                message.includes('not registered') ||
                message.includes('unimplemented') ||
                message.includes('unknown service')
            );
        }

        function disableClientNotifications() {
            state.notificationUnsupported = true;
            localStorage.setItem('netwatch_notification_unsupported', 'true');
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
                    out.innerHTML = `<div class="trace-empty">${i18n('trace_no_hops')}</div>`;
                    return;
                }

                out.innerHTML = hops.map(h => {
                    const primary = h.ip || '*';
                    const secondary = h.location || (primary === '*' ? i18n('timeout') : i18n('geo_lookup_pending'));
                    const timedOut = !h.ip && !h.host;
                    const latency = timedOut ? i18n('timeout') : (Number.isFinite(h.latency_ms) && h.latency_ms > 0 ? `${h.latency_ms} ms` : '--');
                    const latencyClass = timedOut ? 'fail' : (h.latency_ms > 200 ? 'warn' : '');
                    return `
                        <div class="trace-hop">
                            <div class="trace-hop-host">${escapeHtml(primary)}</div>
                            <div class="trace-hop-ip">${escapeHtml(secondary)}</div>
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
                    { label: i18n('target'), value: host },
                    { label: i18n('status_col'), value: i18n('tracing') },
                    { label: i18n('tool'), value: 'mtr' }
                ]);
                out.innerHTML = `<div class="trace-empty">${i18n('collecting_trace')}</div>`;
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
                                { label: i18n('target'), value: data.target || host },
                                { label: i18n('status_col'), value: i18n('failed') },
                                { label: i18n('tool'), value: data.tool || 'mtr' }
                            ]);
                            out.innerHTML = `<div class="trace-empty">${i18n('error')}: ${data.error}</div>`;
                            if (state.tracePoller) {
                                clearInterval(state.tracePoller);
                                state.tracePoller = null;
                            }
                            return;
                        }

                        state.traceResult = data;
                        renderTraceSummary([
                            { label: i18n('target'), value: data.target || host },
                            { label: i18n('status_col'), value: data.running ? i18n('tracing') : `${(data.hops || []).length} ${i18n('hops')}` },
                            { label: i18n('tool'), value: data.tool || 'mtr' }
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
                        { label: i18n('target'), value: host },
                        { label: i18n('status_col'), value: i18n('request_failed') },
                        { label: i18n('tool'), value: 'mtr' }
                    ]);
                    out.innerHTML = `<div class="trace-empty">${i18n('request_failed')}: ${e.message}</div>`;
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

        function applySettingsToForm() {
            if (elements.settingBroadbandDomesticOnly) {
                elements.settingBroadbandDomesticOnly.checked = !!state.settings.broadband_domestic_only;
            }
            if (elements.settingNICRealtimeEnabled) {
                elements.settingNICRealtimeEnabled.checked = !!state.settings.nic_realtime_enabled;
            }
            if (elements.settingNICRealtimeIntervalSec) {
                elements.settingNICRealtimeIntervalSec.value = String(state.settings.nic_realtime_interval_sec || 1);
                elements.settingNICRealtimeIntervalSec.disabled = !state.settings.nic_realtime_enabled;
                window.syncCustomSelect?.(elements.settingNICRealtimeIntervalSec);
            }
            if (elements.settingBackgroundMonitorEnabled) {
                elements.settingBackgroundMonitorEnabled.checked = !!state.settings.background_monitor_enabled;
            }
            if (elements.settingBackgroundMonitorIntervalSec) {
                elements.settingBackgroundMonitorIntervalSec.value = String(state.settings.background_monitor_interval_sec || 60);
                elements.settingBackgroundMonitorIntervalSec.disabled = !state.settings.background_monitor_enabled;
                window.syncCustomSelect?.(elements.settingBackgroundMonitorIntervalSec);
            }
            const notificationsDisabled = !state.settings.background_monitor_enabled || !state.settings.notifications_enabled;
            if (elements.settingNotificationsEnabled) {
                elements.settingNotificationsEnabled.checked = !!state.settings.notifications_enabled;
                elements.settingNotificationsEnabled.disabled = !state.settings.background_monitor_enabled;
            }
            if (elements.settingNotifyAbnormalTraffic) {
                elements.settingNotifyAbnormalTraffic.checked = state.settings.notify_abnormal_traffic !== false;
                elements.settingNotifyAbnormalTraffic.disabled = notificationsDisabled;
            }
            if (elements.settingAbnormalTrafficThresholdMbps) {
                elements.settingAbnormalTrafficThresholdMbps.value = String(state.settings.abnormal_traffic_threshold_mbps || 100);
                elements.settingAbnormalTrafficThresholdMbps.disabled = notificationsDisabled || state.settings.notify_abnormal_traffic === false;
                window.syncCustomSelect?.(elements.settingAbnormalTrafficThresholdMbps);
            }
            if (elements.settingNotifyEgressChange) {
                elements.settingNotifyEgressChange.checked = state.settings.notify_egress_change !== false;
                elements.settingNotifyEgressChange.disabled = notificationsDisabled;
            }
            if (elements.settingNotifyConnectivityChange) {
                elements.settingNotifyConnectivityChange.checked = state.settings.notify_connectivity_change !== false;
                elements.settingNotifyConnectivityChange.disabled = notificationsDisabled;
            }
            if (elements.settingNotifyLANDeviceChange) {
                elements.settingNotifyLANDeviceChange.checked = state.settings.notify_lan_device_change !== false;
                elements.settingNotifyLANDeviceChange.disabled = notificationsDisabled;
            }
            if (elements.settingClientNotificationEnabled) {
                elements.settingClientNotificationEnabled.checked = state.settings.client_notification_enabled !== false;
                elements.settingClientNotificationEnabled.disabled = notificationsDisabled || !state.settings.bark_enabled;
            }
            if (elements.settingBarkEnabled) {
                elements.settingBarkEnabled.checked = !!state.settings.bark_enabled;
                elements.settingBarkEnabled.disabled = notificationsDisabled;
            }
            const barkInputsDisabled = notificationsDisabled || !state.settings.bark_enabled;
            if (elements.settingBarkServerURL) {
                elements.settingBarkServerURL.value = state.settings.bark_server_url || 'https://api.day.app';
                elements.settingBarkServerURL.disabled = barkInputsDisabled;
            }
            if (elements.settingBarkDeviceKey) {
                elements.settingBarkDeviceKey.value = state.settings.bark_device_key || '';
                elements.settingBarkDeviceKey.disabled = barkInputsDisabled;
            }
            if (elements.settingBarkGroup) {
                elements.settingBarkGroup.value = state.settings.bark_group || 'Netwatch';
                elements.settingBarkGroup.disabled = barkInputsDisabled;
            }
            if (elements.testBarkNotification) {
                elements.testBarkNotification.disabled = barkInputsDisabled;
            }
            if (elements.settingDNDEnabled) {
                elements.settingDNDEnabled.checked = !!state.settings.dnd_enabled;
                elements.settingDNDEnabled.disabled = notificationsDisabled;
            }
            if (elements.settingDNDStart) {
                elements.settingDNDStart.value = state.settings.dnd_start || '22:00';
                elements.settingDNDStart.disabled = notificationsDisabled || !state.settings.dnd_enabled;
            }
            if (elements.settingDNDEnd) {
                elements.settingDNDEnd.value = state.settings.dnd_end || '08:00';
                elements.settingDNDEnd.disabled = notificationsDisabled || !state.settings.dnd_enabled;
            }
            if (elements.settingScheduledNotifyEnabled) {
                elements.settingScheduledNotifyEnabled.checked = !!state.settings.scheduled_notify_enabled;
                elements.settingScheduledNotifyEnabled.disabled = notificationsDisabled;
            }
            if (elements.settingScheduledNotifyTime) {
                elements.settingScheduledNotifyTime.value = state.settings.scheduled_notify_time || '09:00';
                elements.settingScheduledNotifyTime.disabled = notificationsDisabled || !state.settings.scheduled_notify_enabled;
            }
            updateNICRealtimeRefreshButton();
        }

        async function testBarkNotification() {
            try {
                await saveSettings();
                const resp = await fetch('/api/v1/notifications/bark/test', { method: 'POST' });
                if (!resp.ok) {
                    const data = await resp.json().catch(() => ({}));
                    throw new Error(data.error || `HTTP ${resp.status}`);
                }
                showToast('Bark 测试推送已发送', 'success');
            } catch (err) {
                showToast(`Bark 测试失败: ${err.message}`, 'error');
            }
        }

        async function loadSettings() {
            try {
                const settingsResp = await fetch('/api/v1/settings', { cache: 'no-store' });
                if (!settingsResp.ok) throw new Error(`HTTP ${settingsResp.status}`);
                const settingsData = await settingsResp.json();
                state.settings = {
                    refresh_interval_sec: settingsData.refresh_interval_sec || state.refreshInterval || 10,
                    broadband_domestic_only: !!settingsData.broadband_domestic_only,
                    nic_realtime_enabled: settingsData.nic_realtime_enabled !== false,
                    nic_realtime_interval_sec: settingsData.nic_realtime_interval_sec || 1,
                    chart_time_label_interval: settingsData.chart_time_label_interval || 0,
                    traffic_sampling_enabled: settingsData.traffic_sampling_enabled !== false,
                    traffic_sampling_interval_sec: settingsData.traffic_sampling_interval_sec || 60,
                    per_app_sampling_interval: settingsData.per_app_sampling_interval || {},
                    persistent_traffic_bridges: settingsData.persistent_traffic_bridges || [],
                    background_monitor_enabled: !!settingsData.background_monitor_enabled,
                    background_monitor_interval_sec: settingsData.background_monitor_interval_sec || 60,
                    notifications_enabled: !!settingsData.notifications_enabled,
                    client_notification_enabled: settingsData.client_notification_enabled !== false,
                    notify_abnormal_traffic: settingsData.notify_abnormal_traffic !== false,
                    notify_egress_change: settingsData.notify_egress_change !== false,
                    notify_connectivity_change: settingsData.notify_connectivity_change !== false,
                    notify_lan_device_change: settingsData.notify_lan_device_change !== false,
                    lan_device_offline_after_sec: settingsData.lan_device_offline_after_sec ?? 180,
                    lan_device_online_after_sec: settingsData.lan_device_online_after_sec ?? 0,
                    lan_device_offline_notify_delay_sec: settingsData.lan_device_offline_notify_delay_sec ?? 120,
                    lan_device_online_notify_delay_sec: settingsData.lan_device_online_notify_delay_sec ?? 120,
                    abnormal_traffic_threshold_mbps: settingsData.abnormal_traffic_threshold_mbps || 100,
                    bark_enabled: !!settingsData.bark_enabled,
                    bark_server_url: settingsData.bark_server_url || 'https://api.day.app',
                    bark_device_key: settingsData.bark_device_key || '',
                    bark_group: settingsData.bark_group || 'Netwatch',
                    dnd_enabled: !!settingsData.dnd_enabled,
                    dnd_start: settingsData.dnd_start || '22:00',
                    dnd_end: settingsData.dnd_end || '08:00',
                    scheduled_notify_enabled: !!settingsData.scheduled_notify_enabled,
                    scheduled_notify_time: settingsData.scheduled_notify_time || '09:00',
                    notification_device_ids: settingsData.notification_device_ids || [],
                    notify_template_title: settingsData.notify_template_title || '',
                    notify_template_body: settingsData.notify_template_body || ''
                };
                applySettingsToForm();
                updateTrafficAnalysisLink();
                loadLazycatDevices();
            } catch (error) {
                console.error(error);
            }
        }

        function updateNICRealtimeRefreshButton() {
            if (!elements.nicRealtimeRefreshBtn) return;
            elements.nicRealtimeRefreshBtn.style.display = state.settings.nic_realtime_enabled ? 'none' : '';
        }

        function updateTrafficAnalysisLink() {
            const link = document.getElementById('traffic-analysis-link');
            if (!link) return;
            link.hidden = !state.settings.traffic_sampling_enabled;
        }

        async function saveSettings() {
            const payload = {
                refresh_interval_sec: state.settings.refresh_interval_sec,
                broadband_domestic_only: !!elements.settingBroadbandDomesticOnly?.checked,
                nic_realtime_enabled: !!elements.settingNICRealtimeEnabled?.checked,
                nic_realtime_interval_sec: parseInt(elements.settingNICRealtimeIntervalSec?.value || '1', 10) || 1,
                chart_time_label_interval: state.settings.chart_time_label_interval,
                traffic_sampling_enabled: state.settings.traffic_sampling_enabled,
                traffic_sampling_interval_sec: state.settings.traffic_sampling_interval_sec,
                per_app_sampling_interval: state.settings.per_app_sampling_interval,
                persistent_traffic_bridges: state.settings.persistent_traffic_bridges,
                background_monitor_enabled: !!elements.settingBackgroundMonitorEnabled?.checked,
                background_monitor_interval_sec: parseInt(elements.settingBackgroundMonitorIntervalSec?.value || '60', 10) || 60,
                notifications_enabled: !!elements.settingNotificationsEnabled?.checked,
                client_notification_enabled: !!elements.settingClientNotificationEnabled?.checked || !elements.settingBarkEnabled?.checked,
                notify_abnormal_traffic: !!elements.settingNotifyAbnormalTraffic?.checked,
                notify_egress_change: !!elements.settingNotifyEgressChange?.checked,
                notify_connectivity_change: !!elements.settingNotifyConnectivityChange?.checked,
                notify_lan_device_change: state.settings.notify_lan_device_change !== false,
                lan_device_offline_after_sec: state.settings.lan_device_offline_after_sec || 180,
                lan_device_online_after_sec: state.settings.lan_device_online_after_sec || 0,
                lan_device_offline_notify_delay_sec: state.settings.lan_device_offline_notify_delay_sec ?? 120,
                lan_device_online_notify_delay_sec: state.settings.lan_device_online_notify_delay_sec ?? 120,
                abnormal_traffic_threshold_mbps: parseInt(elements.settingAbnormalTrafficThresholdMbps?.value || '100', 10) || 100,
                bark_enabled: !!elements.settingBarkEnabled?.checked,
                bark_server_url: elements.settingBarkServerURL?.value?.trim() || 'https://api.day.app',
                bark_device_key: elements.settingBarkDeviceKey?.value?.trim() || '',
                bark_group: elements.settingBarkGroup?.value?.trim() || 'Netwatch',
                dnd_enabled: !!elements.settingDNDEnabled?.checked,
                dnd_start: elements.settingDNDStart?.value || '22:00',
                dnd_end: elements.settingDNDEnd?.value || '08:00',
                scheduled_notify_enabled: !!elements.settingScheduledNotifyEnabled?.checked,
                scheduled_notify_time: elements.settingScheduledNotifyTime?.value || '09:00',
                notification_device_ids: state.settings.notification_device_ids || [],
                notify_template_title: state.settings.notify_template_title || '',
                notify_template_body: state.settings.notify_template_body || ''
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
                state.settings = { ...state.settings, ...payload };
                state.refreshInterval = payload.refresh_interval_sec;
                applySettingsToForm();
                state.nicRealtimeInitialized = false;
                initNICRealtime();
                showToast(i18n('save_settings_success'), 'success');
            } catch (error) {
                console.error(error);
                showToast(i18n('save_settings_fail'), 'error');
            }
        }

        function boot() {
            if (state.initialized) return;
            state.initialized = true;
            initTheme();
            initWithRetry();

            document.addEventListener('visibilitychange', () => {
                if (!document.hidden) {
                    loadSummary(false, true);
                }
            });
        }

        setTimeout(boot, 100);
})();
