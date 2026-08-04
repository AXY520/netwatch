(function () {
    'use strict';
    const shared = window.NetwatchShared || {};
    const escapeHtml = shared.escapeHtml || ((value) => String(value ?? ''));
    const content = document.getElementById('detail-content');
    const refresh = document.getElementById('detail-refresh');

    function formatBytes(value) {
        let n = Number(value) || 0;
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let i = 0;
        while (n >= 1024 && i < units.length - 1) { n /= 1024; i += 1; }
        return `${n >= 100 || i === 0 ? n.toFixed(0) : n.toFixed(1)} ${units[i]}`;
    }
    function formatSpeed(value) { return `${formatBytes(value)}/s`; }
    function value(value, fallback) { return escapeHtml(value || fallback || '-'); }
    function statusClass(severity) { return severity === 'critical' || severity === 'error' ? 'bad' : severity === 'warning' ? 'warn' : 'good'; }
    function line(points, key) {
        if (!Array.isArray(points) || points.length < 2) return '';
        const rates = [];
        for (let i = 1; i < points.length; i += 1) {
            const a = points[i - 1], b = points[i];
            const seconds = (Date.parse(String(b.timestamp).replace(' ', 'T')) - Date.parse(String(a.timestamp).replace(' ', 'T'))) / 1000;
            const before = Number(a[key]) || 0, after = Number(b[key]) || 0;
            if (seconds > 0 && after >= before && !a.discontinuity && !b.discontinuity) rates.push((after - before) / seconds); else rates.push(0);
        }
        const max = Math.max(...rates, 1), width = 800, height = 150;
        return rates.map((rate, i) => `${i ? 'L' : 'M'} ${(i / Math.max(1, rates.length - 1) * width).toFixed(1)} ${(height - rate / max * (height - 12) - 6).toFixed(1)}`).join(' ');
    }
    function render(data) {
        const bridge = data.bridge || {};
        const history = data.history || [];
        const uploadPath = line(history, 'upload_bytes');
        const downloadPath = line(history, 'download_bytes');
        const limitation = data.statistics_available ? '' : `<div class="app-detail-limit"><strong>统计限制</strong><span>${value(data.limitation)}</span></div>`;
        const containers = (data.containers || []).map((item) => `<tr><td>${value(item.name)}</td><td>${value(item.image)}</td><td><span class="status-dot ${item.state === 'running' ? 'good' : 'warn'}"></span>${value(item.state)}</td></tr>`).join('');
        const ports = (data.ports || []).map((item) => `<tr><td>${value(item.protocol).toUpperCase()}</td><td>${value(item.address)}:${Number(item.port) || '-'}</td><td>${value(item.state)}</td><td>${value(item.container?.name || item.process?.name)}</td></tr>`).join('');
        const events = (data.events || []).map((item) => `<li><span class="status-dot ${statusClass(item.severity)}"></span><div><strong>${value(item.title)}</strong><p>${value(item.summary)}</p></div><time>${value(item.timestamp)}</time></li>`).join('');
        content.innerHTML = `
            <section class="app-detail-hero">
                <div><div class="app-detail-kicker">${data.mode === 'host' ? 'HOST NETWORK' : value(bridge.bridge, 'APPLICATION BRIDGE')}</div><h1>${value(data.app_title || data.app_id, '未知应用')}</h1><p>${value(data.app_id)} · ${value(data.project)}</p></div>
                <div class="app-detail-freshness"><span class="status-dot ${data.stale ? 'warn' : 'good'}"></span>${data.stale ? '数据可能已过期' : '观测正常'}<small>${value(data.sampled_at || data.generated_at)}</small></div>
            </section>
            ${limitation}
            <section class="app-detail-metrics">
                <div><span>实时上传</span><strong>${data.statistics_available ? formatSpeed(data.live?.upload_bps) : '-'}</strong></div>
                <div><span>实时下载</span><strong>${data.statistics_available ? formatSpeed(data.live?.download_bps) : '-'}</strong></div>
                <div><span>累计上传</span><strong>${data.statistics_available ? formatBytes(bridge.upload_bytes) : '-'}</strong></div>
                <div><span>累计下载</span><strong>${data.statistics_available ? formatBytes(bridge.download_bytes) : '-'}</strong></div>
            </section>
            <section class="card app-detail-chart-panel"><div class="card-title"><span>24 小时速率趋势</span><span class="placeholder">${history.length} 个采样点</span></div>${uploadPath || downloadPath ? `<svg class="app-detail-chart" viewBox="0 0 800 150" preserveAspectRatio="none" role="img" aria-label="上传下载速率趋势"><path class="upload" d="${uploadPath}"></path><path class="download" d="${downloadPath}"></path></svg><div class="app-detail-legend"><span class="upload">上传</span><span class="download">下载</span></div>` : '<div class="placeholder app-detail-empty">暂无足够历史采样</div>'}</section>
            <section class="app-detail-info-grid"><div><span>网桥</span><strong>${value(bridge.bridge, data.mode === 'host' ? 'host' : '-')}</strong></div><div><span>IPv4 子网</span><strong>${value(bridge.subnet_v4)}</strong></div><div><span>IPv6 子网</span><strong>${value(bridge.subnet_v6)}</strong></div><div><span>容器</span><strong>${(data.containers || []).length}</strong></div></section>
            <section class="app-detail-sections">
                <section class="card"><div class="card-title"><span>容器</span><span class="placeholder">${(data.containers || []).length}</span></div><div class="table-scroll"><table class="traffic-mini-table"><thead><tr><th>名称</th><th>镜像</th><th>状态</th></tr></thead><tbody>${containers || '<tr><td colspan="3">暂无容器信息</td></tr>'}</tbody></table></div></section>
                <section class="card"><div class="card-title"><span>端口占用</span><span class="placeholder">${(data.ports || []).length}</span></div><div class="table-scroll"><table class="traffic-mini-table"><thead><tr><th>协议</th><th>监听地址</th><th>状态</th><th>归属</th></tr></thead><tbody>${ports || '<tr><td colspan="4">未发现监听端口</td></tr>'}</tbody></table></div></section>
                <section class="card"><div class="card-title"><span>最近异常事件</span><span class="placeholder">${(data.events || []).length}</span></div><ul class="app-detail-events">${events || '<li class="placeholder">近期没有应用相关异常</li>'}</ul></section>
                <section class="card"><div class="card-title"><span>连接目标</span></div><div class="app-detail-empty"><strong>当前未采集</strong><p>${value(data.connection_note)}</p></div></section>
            </section>`;
    }
    async function load() {
        refresh.disabled = true;
        const params = new URLSearchParams(location.search);
        params.set('range', '24h'); params.set('limit', '500');
        try { render(await window.NetwatchAPI.get('/api/v1/network/app-detail', Object.fromEntries(params.entries()))); }
        catch (error) { content.innerHTML = `<div class="card placeholder app-detail-error">加载失败：${value(error.message)}</div>`; }
        finally { refresh.disabled = false; }
    }
    refresh.addEventListener('click', load);
    load();
})();
