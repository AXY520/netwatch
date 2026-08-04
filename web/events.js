(function () {
    var timeline = document.getElementById('events-timeline');
    var statusEl = document.getElementById('events-status');
    var refreshBtn = document.getElementById('events-refresh-btn');
    var severityFilter = document.getElementById('events-severity-filter');
    var kindFilter = document.getElementById('events-kind-filter');
    var rangeFilter = document.getElementById('events-range-filter');
    var i18n = function (key) { return typeof window.__ === 'function' ? window.__(key) : key; };

    function eventGet(path) {
        if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
        return fetch(path, { cache: 'no-store' }).then(function (response) {
            if (!response.ok) throw new Error('HTTP ' + response.status);
            return response.json();
        });
    }

    function rangeStart(value) {
        var duration = { '24h': 86400000, '7d': 604800000, '30d': 2592000000 }[value];
        if (!duration) return '';
        return new Date(Date.now() - duration).toISOString();
    }

    function eventKindLabel(kind) {
        if (kind === 'app_bridge_appeared') kind = 'app_enabled';
        if (kind === 'app_bridge_disappeared') kind = 'app_disabled';
        var key = 'event_kind_' + String(kind || 'unknown');
        var translated = i18n(key);
        return translated === key ? String(kind || '-') : translated;
    }

    function sourceLabel(source) {
        var key = 'event_source_' + String(source || 'unknown');
        var translated = i18n(key);
        return translated === key ? String(source || '-') : translated;
    }

    function displayEvent(event) {
        var out = Object.assign({}, event || {});
        if (out.kind === 'app_bridge_appeared') out.kind = 'app_enabled';
        if (out.kind === 'app_bridge_disappeared') out.kind = 'app_disabled';
        if (out.kind === 'app_enabled' || out.kind === 'app_disabled') {
            out.title = eventKindLabel(out.kind);
            if (/^lzc-br-/i.test(String(out.summary || '').trim())) {
                out.summary = i18n('unknown_app');
            }
        }
        return out;
    }

    function formatEventTime(value) {
        if (!value) return '-';
        var date = new Date(String(value).replace(' ', 'T'));
        if (Number.isNaN(date.getTime())) return value;
        return date.toLocaleString();
    }

    function renderKinds(kinds) {
        var current = kindFilter.value;
        var options = [{ value: '', label: i18n('events_all_kinds') }].concat((kinds || []).map(function (kind) {
            return { value: kind, label: eventKindLabel(kind) };
        }));
        NetwatchShared.setSelectOptions(kindFilter, options, current, false);
    }

    function renderEvents(events) {
        if (!events || events.length === 0) {
            timeline.innerHTML = '<div class="events-empty"><strong>' + NetwatchShared.escapeHtml(i18n('events_empty')) + '</strong></div>';
            return;
        }
        timeline.innerHTML = events.map(function (rawEvent) {
            var event = displayEvent(rawEvent);
            var severity = event.severity === 'warning' ? 'warning' : 'info';
            var count = Number(event.count) || 1;
            var countHtml = count > 1 ? '<span class="event-count">x' + count + '</span>' : '';
            return '<article class="event-row event-row--' + severity + '">' +
                '<div class="event-rail"><span class="event-marker" aria-hidden="true"></span></div>' +
                '<div class="event-time">' + NetwatchShared.escapeHtml(formatEventTime(event.timestamp)) + '</div>' +
                '<div class="event-content">' +
                    '<div class="event-heading"><strong>' + NetwatchShared.escapeHtml(event.title || eventKindLabel(event.kind)) + '</strong>' + countHtml + '</div>' +
                    '<div class="event-summary">' + NetwatchShared.escapeHtml(event.summary || '-') + '</div>' +
                    '<div class="event-meta"><span>' + NetwatchShared.escapeHtml(eventKindLabel(event.kind)) + '</span><span>' + NetwatchShared.escapeHtml(sourceLabel(event.source)) + '</span><span>' + NetwatchShared.escapeHtml(severity === 'warning' ? i18n('events_warning') : i18n('events_info')) + '</span></div>' +
                '</div>' +
            '</article>';
        }).join('');
    }

    async function loadEvents() {
        refreshBtn.disabled = true;
        statusEl.textContent = i18n('loading');
        var params = new URLSearchParams({ limit: '300' });
        if (severityFilter.value) params.set('severity', severityFilter.value);
        if (kindFilter.value) params.set('kind', kindFilter.value);
        var since = rangeStart(rangeFilter.value);
        if (since) params.set('since', since);
        try {
            var data = await eventGet('/api/v1/events/history?' + params.toString());
            renderKinds(data.kinds || []);
            renderEvents(data.events || []);
            statusEl.textContent = (data.events || []).length + ' ' + i18n('events_count_unit');
        } catch (error) {
            timeline.innerHTML = '<div class="events-empty events-error"><strong>' + NetwatchShared.escapeHtml(i18n('load_failed')) + '</strong><span>' + NetwatchShared.escapeHtml(error.message) + '</span></div>';
            statusEl.textContent = i18n('load_failed');
        } finally {
            refreshBtn.disabled = false;
        }
    }

    refreshBtn.addEventListener('click', loadEvents);
    severityFilter.addEventListener('change', loadEvents);
    kindFilter.addEventListener('change', loadEvents);
    rangeFilter.addEventListener('change', loadEvents);
    loadEvents();
})();
