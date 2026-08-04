(function () {
    var form = document.getElementById('dns-diag-form');
    var nameInput = document.getElementById('dns-diag-name');
    var serverInput = document.getElementById('dns-diag-server');
    var runButton = document.getElementById('dns-diag-run');
    var statusEl = document.getElementById('dns-diag-status');
    var resultsEl = document.getElementById('dns-diag-results');
    var systemResultsEl = document.getElementById('dns-system-results');
    var specifiedEl = document.getElementById('dns-specified-result');
    var specifiedGroupEl = document.getElementById('dns-specified-group');
    var differencesEl = document.getElementById('dns-diag-differences');
    var conclusionEl = document.getElementById('dns-diag-conclusion');
    var resolverSourceEl = document.getElementById('dns-resolver-source');
    var resolverDetailsEl = document.getElementById('dns-resolver-details');
    var deviceSelectEl = document.getElementById('dns-device-select');
    var typeButtons = Array.from(document.querySelectorAll('#dns-type-segments [data-type]'));
    var selectedType = 'A';
    var resolverCandidates = [];
    var i18n = function (key) { return typeof window.__ === 'function' ? window.__(key) : key; };

    function dnsPost(path, body) {
        if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
        return fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }).then(async function (response) {
            var data = await response.json().catch(function () { return {}; });
            if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
            return data;
        });
    }

    function dnsGet(path) {
        if (window.NetwatchAPI) return window.NetwatchAPI.get(path);
        return fetch(path).then(async function (response) {
            var data = await response.json().catch(function () { return {}; });
            if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
            return data;
        });
    }

    function sourceLabel(source) {
        var key = 'dns_source_' + String(source || 'unknown');
        var translated = i18n(key);
        return translated === key ? String(source || '-') : translated;
    }

    function renderResolverInfo(info) {
        info = info || {};
        var servers = Array.isArray(info.servers) ? info.servers : [];
        resolverCandidates = Array.isArray(info.candidates) ? info.candidates : [];
        resolverSourceEl.textContent = sourceLabel(info.source);
        resolverSourceEl.classList.toggle('dns-source-badge--fallback', !!info.fallback);
        if (resolverCandidates.length) {
            var currentOptions = Array.from(deviceSelectEl.options).map(function (option) { return option.value; }).join('\n');
            var nextOptions = resolverCandidates.map(function (candidate) { return candidate.device; }).join('\n');
            if (currentOptions !== nextOptions) {
                deviceSelectEl.innerHTML = resolverCandidates.map(function (candidate) {
                    var type = String(candidate.type || '').toLowerCase();
                    var label = candidate.connection || candidate.device;
                    return '<option value="' + NetwatchShared.escapeHtml(candidate.device) + '">' + NetwatchShared.escapeHtml(label + ' · ' + candidate.device + (type ? ' · ' + type : '')) + '</option>';
                }).join('');
            }
            deviceSelectEl.value = info.device || resolverCandidates[0].device;
            deviceSelectEl.disabled = resolverCandidates.length < 2;
        } else {
            deviceSelectEl.innerHTML = '<option value="">' + NetwatchShared.escapeHtml(i18n('dns_diag_auto_device')) + '</option>';
            deviceSelectEl.disabled = true;
        }
        var facts = [];
        if (info.connection) facts.push('<span><small>' + NetwatchShared.escapeHtml(i18n('dns_diag_connection')) + '</small><strong>' + NetwatchShared.escapeHtml(info.connection) + '</strong></span>');
        var selectedCandidate = resolverCandidates.find(function (candidate) { return candidate.device === info.device; });
        if (selectedCandidate && selectedCandidate.type) facts.push('<span><small>' + NetwatchShared.escapeHtml(i18n('dns_diag_link_type')) + '</small><strong>' + NetwatchShared.escapeHtml(selectedCandidate.type) + '</strong></span>');
        facts.push('<span class="dns-resolver-addresses"><small>DNS</small><strong>' + (servers.length ? servers.map(function (server) { return '<code>' + NetwatchShared.escapeHtml(server) + '</code>'; }).join('') : '--') + '</strong></span>');
        if (info.note) facts.push('<span class="dns-resolver-note"><small>' + NetwatchShared.escapeHtml(i18n('dns_diag_notice')) + '</small><strong>' + NetwatchShared.escapeHtml(info.note) + '</strong></span>');
        resolverDetailsEl.classList.remove('placeholder');
        resolverDetailsEl.innerHTML = facts.join('');
    }

    function dnssecLabel(status) {
        var key = 'dnssec_' + String(status || 'not_present');
        var translated = i18n(key);
        return translated === key ? String(status || '-') : translated;
    }

    function errorLabel(code) {
        var key = 'dns_error_' + String(code || 'query_failed');
        var translated = i18n(key);
        return translated === key ? String(code || '-') : translated;
    }

    function resultHTML(title, result) {
        var answers = Array.isArray(result.answers) ? result.answers : [];
        var answerHTML;
        if (result.error) {
            answerHTML = '<div class="dns-result-error"><strong>' + NetwatchShared.escapeHtml(errorLabel(result.error_code)) + '</strong><span>' + NetwatchShared.escapeHtml(result.error) + '</span></div>';
        } else if (answers.length === 0) {
            answerHTML = '<div class="dns-result-empty">' + NetwatchShared.escapeHtml(i18n('dns_diag_no_answers')) + '</div>';
        } else {
            answerHTML = '<div class="dns-answer-list">' + answers.map(function (answer) {
                return '<div class="dns-answer-row"><span class="dns-answer-type">' + NetwatchShared.escapeHtml(answer.type) + '</span><code>' + NetwatchShared.escapeHtml(answer.value) + '</code><span class="dns-answer-ttl">TTL ' + Number(answer.ttl || 0) + 's</span></div>';
            }).join('') + '</div>';
        }
        return '<header class="dns-result-head"><div><span class="dns-result-kicker">' + NetwatchShared.escapeHtml(title) + '</span><strong>' + NetwatchShared.escapeHtml(result.server || '-') + '</strong></div><span class="dns-status dns-status--' + (result.status === 'NOERROR' ? 'ok' : 'warning') + '">' + NetwatchShared.escapeHtml(result.status || 'ERROR') + '</span></header>' +
            '<div class="dns-result-metrics"><span><small>' + NetwatchShared.escapeHtml(i18n('dns_diag_latency')) + '</small><strong>' + Number(result.duration_ms || 0) + ' ms</strong></span><span><small>' + NetwatchShared.escapeHtml(i18n('dns_diag_transport')) + '</small><strong>' + NetwatchShared.escapeHtml((result.transport || '-').toUpperCase()) + '</strong></span><span><small>DNSSEC</small><strong>' + NetwatchShared.escapeHtml(dnssecLabel(result.dnssec_status)) + '</strong></span></div>' + answerHTML;
    }

    function renderSystemResults(results) {
        systemResultsEl.innerHTML = results.map(function (result, index) {
            return '<article class="dns-result-panel">' + resultHTML(i18n('dns_diag_resolver') + ' ' + (index + 1), result) + '</article>';
        }).join('');
    }

    function renderConclusion(code) {
        var key = 'dns_conclusion_' + String(code || 'system_ok');
        var translated = i18n(key);
        conclusionEl.className = 'dns-diag-conclusion dns-diag-conclusion--' + NetwatchShared.escapeHtml(code || 'system_ok');
        conclusionEl.textContent = translated === key ? String(code || '') : translated;
        conclusionEl.hidden = false;
    }

    function renderDifferences(differences, compared) {
        if (!compared) {
            differencesEl.hidden = true;
            differencesEl.innerHTML = '';
            return;
        }
        differencesEl.hidden = false;
        if (!differences || differences.length === 0) {
            differencesEl.className = 'dns-diag-differences dns-diag-differences--same';
            differencesEl.textContent = i18n('dns_diag_same');
            return;
        }
        differencesEl.className = 'dns-diag-differences dns-diag-differences--different';
        differencesEl.textContent = i18n('dns_diag_different') + ': ' + differences.map(function (item) { return i18n('dns_diff_' + item); }).join(', ');
    }

    typeButtons.forEach(function (button) {
        button.addEventListener('click', function () {
            selectedType = button.dataset.type || 'A';
            typeButtons.forEach(function (item) {
                var active = item === button;
                item.classList.toggle('active', active);
                item.setAttribute('aria-pressed', active ? 'true' : 'false');
            });
        });
    });

    form.addEventListener('submit', async function (event) {
        event.preventDefault();
        var name = nameInput.value.trim();
        if (!name) return;
        runButton.disabled = true;
        statusEl.textContent = i18n('dns_diag_running');
        resultsEl.hidden = true;
        differencesEl.hidden = true;
        conclusionEl.hidden = true;
        try {
            var data = await dnsPost('/api/v1/diagnostics/dns', { name: name, type: selectedType, server: serverInput.value.trim(), device: deviceSelectEl.value });
            renderResolverInfo(data.resolver_info || {});
            var systemResults = Array.isArray(data.system_resolvers) && data.system_resolvers.length ? data.system_resolvers : [data.system || {}];
            renderSystemResults(systemResults);
            if (data.specified) {
                specifiedGroupEl.hidden = false;
                resultsEl.classList.add('dns-diag-results--compare');
                specifiedEl.innerHTML = resultHTML(i18n('dns_diag_specified'), data.specified);
            } else {
                specifiedGroupEl.hidden = true;
                resultsEl.classList.remove('dns-diag-results--compare');
            }
            renderConclusion(data.conclusion_code);
            renderDifferences(data.differences || [], !!data.specified);
            resultsEl.hidden = false;
            statusEl.textContent = (data.name || name) + ' / ' + (data.type || selectedType) + ' / ' + (data.sampled_at || data.generated_at || '');
        } catch (error) {
            statusEl.textContent = i18n('dns_diag_failed') + ': ' + error.message;
        } finally {
            runButton.disabled = false;
        }
    });

    deviceSelectEl.addEventListener('change', function () {
        var device = deviceSelectEl.value;
        resolverDetailsEl.classList.add('placeholder');
        resolverDetailsEl.textContent = i18n('dns_diag_loading_resolvers');
        dnsGet('/api/v1/diagnostics/dns?device=' + encodeURIComponent(device)).then(function (info) {
            renderResolverInfo(info);
            resultsEl.hidden = true;
            conclusionEl.hidden = true;
            differencesEl.hidden = true;
            statusEl.textContent = i18n('dns_diag_idle');
        }).catch(function (error) {
            resolverDetailsEl.textContent = error.message;
        });
    });

    dnsGet('/api/v1/diagnostics/dns').then(renderResolverInfo).catch(function (error) {
        resolverSourceEl.textContent = i18n('dns_diag_unavailable');
        resolverSourceEl.classList.add('dns-source-badge--fallback');
        resolverDetailsEl.textContent = error.message;
    });
})();
