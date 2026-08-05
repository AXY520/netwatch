(function () {
    var timeline = document.getElementById('events-timeline');
    var statusEl = document.getElementById('events-status');
    var refreshBtn = document.getElementById('events-refresh-btn');
    var severityFilter = document.getElementById('events-severity-filter');
    var kindFilter = document.getElementById('events-kind-filter');
    var rangeFilter = document.getElementById('events-range-filter');
    var i18n = function (key) { return typeof window.__ === 'function' ? window.__(key) : key; };
    var loadSequence = 0;
    var eventPoller = null;
    var lastEventSignature = '';
    var latestEvents = [];
    var cachedIcons = {};
    var iconsLoaded = false;

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

    function appIconMap(trafficData) {
        var icons = {};
        (trafficData && Array.isArray(trafficData.bridges) ? trafficData.bridges : []).forEach(function (app) {
            if (!app || !app.icon) return;
            [app.bridge, app.app_id, app.project].filter(Boolean).forEach(function (key) {
                icons[String(key)] = app.icon;
            });
        });
        return icons;
    }

    function eventAppIcon(event, icons) {
        if (event.kind !== 'app_enabled' && event.kind !== 'app_disabled' && event.kind !== 'app_traffic_high') return '';
        var details = event.details || {};
        var direct = details.icon || '';
        if (direct) return direct;
        var keys = [details.bridge, details.app_id, details.project];
        for (var index = 0; index < keys.length; index++) {
            if (keys[index] && icons[String(keys[index])]) return icons[String(keys[index])];
        }
        return '';
    }

    function renderEvents(events, icons) {
        icons = icons || {};
        if (!events || events.length === 0) {
            timeline.innerHTML = '<div class="events-empty"><strong>' + NetwatchShared.escapeHtml(i18n('events_empty')) + '</strong></div>';
            return;
        }
        timeline.innerHTML = events.map(function (rawEvent) {
            var event = displayEvent(rawEvent);
            var severity = event.severity === 'warning' ? 'warning' : 'info';
            var count = Number(event.count) || 1;
            var countHtml = count > 1 ? '<span class="event-count">x' + count + '</span>' : '';
            var icon = eventAppIcon(event, icons);
            var iconHtml = icon ? '<img class="event-app-icon" src="' + NetwatchShared.escapeHtml(icon) + '" alt="" loading="lazy" onerror="this.remove()">' : '';
            return '<article class="event-row event-row--' + severity + '">' +
                '<div class="event-rail"><span class="event-marker" aria-hidden="true"></span></div>' +
                '<div class="event-time">' + NetwatchShared.escapeHtml(formatEventTime(event.timestamp)) + '</div>' +
                '<div class="event-content">' +
                    iconHtml + '<div class="event-copy">' +
                        '<div class="event-heading"><strong>' + NetwatchShared.escapeHtml(event.title || eventKindLabel(event.kind)) + '</strong>' + countHtml + '</div>' +
                        '<div class="event-summary">' + NetwatchShared.escapeHtml(event.summary || '-') + '</div>' +
                        '<div class="event-meta"><span>' + NetwatchShared.escapeHtml(eventKindLabel(event.kind)) + '</span><span>' + NetwatchShared.escapeHtml(sourceLabel(event.source)) + '</span><span>' + NetwatchShared.escapeHtml(severity === 'warning' ? i18n('events_warning') : i18n('events_info')) + '</span></div>' +
                    '</div>' +
                '</div>' +
            '</article>';
        }).join('');
    }

    function eventSignature(events) {
        return (events || []).map(function (event) {
            return [event.id, event.timestamp, event.count, event.kind].join(':');
        }).join('|');
    }

    async function loadEvents(silent, forceRender) {
        silent = !!silent;
        forceRender = !!forceRender;
        var sequence = ++loadSequence;
        if (!silent) {
            refreshBtn.disabled = true;
            statusEl.textContent = i18n('loading');
        }
        var params = new URLSearchParams({ limit: '300' });
        if (severityFilter.value) params.set('severity', severityFilter.value);
        if (kindFilter.value) params.set('kind', kindFilter.value);
        var since = rangeStart(rangeFilter.value);
        if (since) params.set('since', since);
        var trafficPromise = iconsLoaded ? Promise.resolve(null) : eventGet('/api/v1/network/app-traffic').catch(function () { return null; });
        try {
            var data = await eventGet('/api/v1/events/history?' + params.toString());
            if (sequence !== loadSequence) return;
            renderKinds(data.kinds || []);
            latestEvents = data.events || [];
            var signature = eventSignature(latestEvents);
            if (forceRender || signature !== lastEventSignature) {
                lastEventSignature = signature;
                renderEvents(latestEvents, cachedIcons);
            }
            statusEl.textContent = latestEvents.length + ' ' + i18n('events_count_unit');
            trafficPromise.then(function (trafficData) {
                if (sequence !== loadSequence || !trafficData) return;
                cachedIcons = appIconMap(trafficData);
                iconsLoaded = true;
                renderEvents(latestEvents, cachedIcons);
            });
        } catch (error) {
            if (!silent) {
                timeline.innerHTML = '<div class="events-empty events-error"><strong>' + NetwatchShared.escapeHtml(i18n('load_failed')) + '</strong><span>' + NetwatchShared.escapeHtml(error.message) + '</span></div>';
                statusEl.textContent = i18n('load_failed');
            }
        } finally {
            if (!silent) refreshBtn.disabled = false;
        }
    }

    refreshBtn.addEventListener('click', function () { loadEvents(false, true); });
    severityFilter.addEventListener('change', function () { lastEventSignature = ''; loadEvents(false, true); });
    kindFilter.addEventListener('change', function () { lastEventSignature = ''; loadEvents(false, true); });
    rangeFilter.addEventListener('change', function () { lastEventSignature = ''; loadEvents(false, true); });
    document.addEventListener('visibilitychange', function () {
        if (!document.hidden) loadEvents(true, false);
    });
    eventPoller = setInterval(function () {
        if (!document.hidden) loadEvents(true, false);
    }, 5000);
    window.addEventListener('pagehide', function () {
        if (eventPoller) clearInterval(eventPoller);
    }, { once: true });
    loadEvents(false, true);
})();
