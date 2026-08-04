(function () {
    'use strict';
    const escapeHtml = window.NetwatchShared?.escapeHtml || ((value) => String(value ?? ''));
    const params = new URLSearchParams(location.search);
    const body = document.getElementById('connections-body');
    const status = document.getElementById('connections-status');
    const note = document.getElementById('connections-note');
    const reveal = document.getElementById('connections-reveal');
    const privacy = document.getElementById('connections-privacy');
    const refresh = document.getElementById('connections-refresh');
    const back = document.getElementById('connections-back');
    const detailParams = new URLSearchParams();
    ['bridge', 'app_id', 'project'].forEach((key) => { if (params.get(key)) detailParams.set(key, params.get(key)); });
    back.href = `/app-detail.html?${detailParams.toString()}`;

    function endpoint(address, port) { return `${escapeHtml(address || '-')}<span class="connections-port">:${Number(port) || '-'}</span>`; }
    function render(data) {
        privacy.textContent = data.revealed ? '完整显示' : '默认脱敏';
        status.textContent = `${data.generated_at || '-'} · ${(data.connections || []).length} 条${data.truncated ? '（已截断）' : ''}`;
        note.hidden = !data.note; note.textContent = data.note || '';
        if (!data.supported) { body.innerHTML = `<tr><td colspan="7" class="placeholder">当前宿主无法提供连接快照</td></tr>`; return; }
        const rows = data.connections || [];
        body.innerHTML = rows.length ? rows.map((item) => `<tr><td>${escapeHtml(item.protocol).toUpperCase()}<small>${escapeHtml(item.ip_version)}</small></td><td>${item.direction === 'outbound' ? '出站' : escapeHtml(item.direction || '-')}</td><td>${endpoint(item.local_address, item.local_port)}</td><td>${endpoint(item.remote_address, item.remote_port)}</td><td>${escapeHtml(item.state || '-')}</td><td>${escapeHtml(item.container_name || '-')}</td><td><span class="status-dot ${item.attribution_reliable ? 'good' : 'warn'}"></span>${item.attribution_reliable ? '可靠' : '共享网络栈'}</td></tr>`).join('') : '<tr><td colspan="7" class="placeholder">当前没有活动的远端连接</td></tr>';
    }
    async function load() {
        refresh.disabled = true; status.textContent = '正在读取当前连接';
        const query = Object.fromEntries(detailParams.entries()); query.limit = '200'; query.reveal = reveal.checked ? 'true' : 'false';
        try { render(await window.NetwatchAPI.get('/api/v1/network/connections/snapshot', query)); }
        catch (error) { status.textContent = '采集失败'; body.innerHTML = `<tr><td colspan="7" class="placeholder">${escapeHtml(error.message || error)}</td></tr>`; }
        finally { refresh.disabled = false; }
    }
    refresh.addEventListener('click', load); reveal.addEventListener('change', load);
})();
