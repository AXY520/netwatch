(function () {
    var form = document.getElementById('dns-diag-form');
    var nameInput = document.getElementById('dns-diag-name');
    var serverInput = document.getElementById('dns-diag-server');
    var runButton = document.getElementById('dns-diag-run');
    var statusEl = document.getElementById('dns-diag-status');
    var resultsEl = document.getElementById('dns-diag-results');
    var systemEl = document.getElementById('dns-system-result');
    var specifiedEl = document.getElementById('dns-specified-result');
    var differencesEl = document.getElementById('dns-diag-differences');
    var typeButtons = Array.from(document.querySelectorAll('#dns-type-segments [data-type]'));
    var selectedType = 'A';
    var i18n = function (key) { return typeof window.__ === 'function' ? window.__(key) : key; };

    function dnsPost(path, body) {
        if (window.NetwatchAPI) return window.NetwatchAPI.post(path, body);
        return fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }).then(async function (response) {
            var data = await response.json().catch(function () { return {}; });
            if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
            return data;
        });
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

    function renderResult(target, title, result) {
        var answers = Array.isArray(result.answers) ? result.answers : [];
        var answerHTML;
        if (result.error) {
            answerHTML = '<div class="dns-result-error"><strong>' + NetwatchShared.escapeHtml(errorLabel(result.error_code)) + '</strong><span>' + NetwatchShared.escapeHtml(result.error) + '</span></div>';
        } else if (answers.length === 0) {
            answerHTML = '<div class="dns-result-empty">' + NetwatchShared.escapeHtml(i18n('dns_diag_no_answers')) + '</div>';
        } else {
            answerHTML = '<table class="data-table dns-answer-table"><thead><tr><th>' + i18n('dns_diag_value') + '</th><th>TTL</th></tr></thead><tbody>' + answers.map(function (answer) {
                return '<tr><td><span class="dns-answer-type">' + NetwatchShared.escapeHtml(answer.type) + '</span>' + NetwatchShared.escapeHtml(answer.value) + '</td><td>' + Number(answer.ttl || 0) + 's</td></tr>';
            }).join('') + '</tbody></table>';
        }
        target.innerHTML = '<div class="dns-result-head"><div><strong>' + NetwatchShared.escapeHtml(title) + '</strong><span>' + NetwatchShared.escapeHtml(result.server || '-') + '</span></div><span class="dns-status dns-status--' + (result.status === 'NOERROR' ? 'ok' : 'warning') + '">' + NetwatchShared.escapeHtml(result.status || 'ERROR') + '</span></div>' +
            '<div class="dns-result-meta"><span>' + NetwatchShared.escapeHtml((result.transport || '-').toUpperCase()) + '</span><span>' + Number(result.duration_ms || 0) + ' ms</span><span>' + NetwatchShared.escapeHtml(dnssecLabel(result.dnssec_status)) + '</span></div>' + answerHTML;
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
        try {
            var data = await dnsPost('/api/v1/diagnostics/dns', { name: name, type: selectedType, server: serverInput.value.trim() });
            renderResult(systemEl, i18n('dns_diag_system'), data.system || {});
            if (data.specified) {
                specifiedEl.hidden = false;
                renderResult(specifiedEl, i18n('dns_diag_specified'), data.specified);
            } else {
                specifiedEl.hidden = true;
            }
            renderDifferences(data.differences || [], !!data.specified);
            resultsEl.hidden = false;
            statusEl.textContent = (data.name || name) + ' / ' + (data.type || selectedType) + ' / ' + (data.sampled_at || data.generated_at || '');
        } catch (error) {
            statusEl.textContent = i18n('dns_diag_failed') + ': ' + error.message;
        } finally {
            runButton.disabled = false;
        }
    });
})();
