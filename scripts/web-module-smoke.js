#!/usr/bin/env node

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.resolve(__dirname, '..');
const elementsByID = new Map();
let activeTab = 'ip';
const documentStub = {
    activeElement: null,
    hidden: false,
    addEventListener() {},
    getElementById: (id) => elementsByID.get(id) || null,
    querySelector: (selector) => {
        if (selector === '.network-config-tab.active') return { dataset: { tab: activeTab } };
        if (selector === '.network-config-body') return { classList: { toggle() {} } };
        return null;
    },
    querySelectorAll: () => [],
    createElement: () => ({ dataset: {}, disabled: false, value: '', textContent: '', classList: { add() {}, remove() {}, toggle() {} } }),
    createDocumentFragment: () => ({ children: [], appendChild(child) { this.children.push(child); } })
};

global.window = global;
global.document = documentStub;
global.location = { href: 'http://localhost/' };
global.__app = {
    state: { networkConfigPendingData: null },
    els: {},
    i18n: (key) => key,
    netwatchGet: async (url) => url.includes('/pending') ? { pending: false } : { enabled: true, devices: [] },
    netwatchPost: async () => ({ ok: true }),
    refreshNetworkDetailCards() {},
    refreshAppTrafficSoon() {}
};

function load(relativePath) {
    const filename = path.join(root, relativePath);
    vm.runInThisContext(fs.readFileSync(filename, 'utf8'), { filename });
}

async function main() {
    load('web/shared.js');
    load('web/app-traffic-dashboard.js');
    let syncCount = 0;
    global.syncCustomSelect = () => { syncCount += 1; };
    const select = {
        disabled: false,
        value: '',
        options: [],
        replaceChildren(fragment) { this.options = fragment.children; }
    };
    global.NetwatchShared.setSelectOptions(select, [
        { value: 'eth0', label: 'Ethernet' },
        { value: 'wlan0', label: 'Wi-Fi' }
    ], 'wlan0', false);
    assert.equal(select.value, 'wlan0');
    assert.equal(select.options.length, 2);
    assert.equal(syncCount, 1);

    const trafficItem = (modes, limit, proxyEnabled) => ({
        app_id: 'test.app',
        network_modes: modes,
        limit,
        network_policy: {
            capabilities: { upload_limit: true, download_limit: true, internet_control: true, proxy_control: true },
            desired: { internet_allowed: true, proxy_enabled: proxyEnabled },
            internet_state: 'allowed'
        }
    });
    const hostLimitedMenu = global.__app.appTrafficMoreMenu(trafficItem(['host'], { upload_kbps: 1000 }, false), true, 'test.app');
    assert.doesNotMatch(hostLimitedMenu, /data-action="traffic-proxy"/, 'limited Host apps must hide proxy enable');
    assert.match(hostLimitedMenu, /data-action="traffic-limit"/, 'limited Host apps must keep limit editing');
    const mixedProxiedMenu = global.__app.appTrafficMoreMenu(trafficItem(['bridge', 'host'], {}, true), true, 'test.app');
    assert.doesNotMatch(mixedProxiedMenu, /data-action="traffic-limit"/, 'proxied Mixed apps must hide limit editing');
    assert.match(mixedProxiedMenu, /data-proxy-state="disable"/, 'proxied Mixed apps must keep restore direct');
    const bridgeCombinedMenu = global.__app.appTrafficMoreMenu(trafficItem(['bridge'], { download_kbps: 1000 }, false), true, 'test.app');
    assert.match(bridgeCombinedMenu, /data-action="traffic-limit"/);
    assert.match(bridgeCombinedMenu, /data-action="traffic-proxy"/, 'Bridge apps may combine proxy and limit');
    const multiInstanceItem = trafficItem(['bridge'], {}, false);
    multiInstanceItem.instance_id = 'test.app@user:axy';
    multiInstanceItem.user_id = 'axy';
    multiInstanceItem.multi_instance = true;
    const multiInstanceMenu = global.__app.appTrafficMoreMenu(multiInstanceItem, true, multiInstanceItem.instance_id);
    assert.match(multiInstanceMenu, /data-app-id="test\.app"/);
    assert.match(multiInstanceMenu, /data-instance-id="test\.app@user:axy"/, 'actions must preserve the selected user instance');
    assert.match(multiInstanceMenu, /aria-expanded="true"/, 'menu identity must be instance-scoped');

    [
        'web/app-network-config.js',
        'web/app-host-bridge.js',
        'web/app-host-dns.js'
    ].forEach(load);

    [
        'loadNetworkConfigDevices',
        'onNetworkConfigDeviceChange',
        'pinnedNetworkConfigDevice',
        'setNetworkConfigFormEnabled',
        'setNetworkConfigLocked',
        'appendNetworkMutationVerification',
        'applyPendingNetworkConfigToForm',
        'fillNetworkMACForm',
        'updateNetworkMACApplyState',
        'setHostBridgeCreateEnabled',
        'fillHostDNSFormFromInfo',
        'setHostDNSOutput'
    ].forEach((name) => assert.equal(typeof global.__app[name], 'function', `${name} must be exported`));

    const verificationText = global.__app.appendNetworkMutationVerification('apply failed', {
        status: 'failed',
        duration_ms: 12,
        steps: [{ name: 'dns_config', ok: false, error: 'bad dns' }]
    });
    assert.match(verificationText, /apply failed/);
    assert.match(verificationText, /bad dns/);

    await global.__app.loadNetworkConfigDevices();
    const deviceSelect = {
        __allDevices: [{ device: 'eth0', type: 'ethernet', connection: 'Wired connection 1' }],
        __devices: [],
        classList: { add() {}, remove() {}, toggle() {} },
        dataset: {},
        disabled: false,
        selectedIndex: 0,
        value: '',
        options: [],
        replaceChildren(fragment) { this.options = fragment.children; }
    };
    elementsByID.set('network-config-device', deviceSelect);
    global.__app.renderNetworkConfigDeviceOptions();
    assert.equal(deviceSelect.value, 'eth0');
    assert.equal(deviceSelect.disabled, false);

    function field(value = '') {
        return {
            value,
            disabled: false,
            hidden: false,
            classList: { add() {}, remove() {}, toggle() {} },
            closest() { return { hidden: false }; }
        };
    }
    elementsByID.set('network-config-method', field('auto'));
    elementsByID.set('network-config-mac', field());
    elementsByID.set('network-mac-current', field());
    elementsByID.set('network-mac-original', field());
    elementsByID.set('network-mac-apply-btn', field());
    elementsByID.set('network-mac-restore-btn', field());
    elementsByID.set('network-mac-confirm-btn', field());
    elementsByID.set('network-mac-rollback-btn', field());
    elementsByID.set('network-mac-status', field());
    elementsByID.set('network-mac-output', field());
    elementsByID.set('network-config-address', field());
    elementsByID.set('network-config-gateway', field());
    elementsByID.set('network-config-dns', field());
    elementsByID.set('network-config-preflight-btn', field());
    elementsByID.set('network-config-apply-btn', field());
    elementsByID.set('network-config-preview', field());
    elementsByID.set('network-config-confirm-btn', field());
    elementsByID.set('network-config-rollback-btn', field());
    elementsByID.set('network-config-status', field());
    deviceSelect.__allDevices = [
        { device: 'eth0', type: 'ethernet', connection: 'Wired', ipv4_method: 'auto', mac_address: '02:11:22:33:44:55', permanent_mac_address: '00:11:22:33:44:55' },
        { device: 'wlan0', type: 'wifi', connection: 'Wi-Fi', ipv4_method: 'auto', mac_address: '02:11:22:33:44:56', permanent_mac_address: '00:11:22:33:44:56' }
    ];
    global.__app.state.networkConfigPendingData = { pending: true, device: 'eth0' };
    global.__app.networkMutationCoordinator.setPending('ip', global.__app.state.networkConfigPendingData);
    global.__app.renderNetworkConfigDeviceOptions();
    deviceSelect.value = 'wlan0';
    global.__app.onNetworkConfigDeviceChange();
    assert.equal(deviceSelect.value, 'wlan0', 'pending transaction must not pin device browsing');
    assert.equal(deviceSelect.disabled, false, 'pending transaction must keep shared device selector enabled');
    assert.equal(elementsByID.get('network-config-method').disabled, true, 'pending transaction must lock mutation fields');

    activeTab = 'mac';
    global.__app.renderNetworkConfigDeviceOptions();
    assert.equal(elementsByID.get('network-mac-current').value, '02:11:22:33:44:56');
    assert.equal(elementsByID.get('network-mac-original').value, '00:11:22:33:44:56');
    assert.equal(elementsByID.get('network-config-mac').value, '02:11:22:33:44:56');

    global.__app.state.networkConfigPendingData = { pending: true, device: 'wlp4s0' };
    global.__app.networkMutationCoordinator.setPending('ip', global.__app.state.networkConfigPendingData);
    deviceSelect.__allDevices = [
        { device: 'eth0', type: 'ethernet', connection: 'Wired', ipv4_method: 'auto' },
        { device: 'wlp4s0', type: 'wifi', connection: 'Wi-Fi', ipv4_method: 'auto' }
    ];
    activeTab = 'bridge';
    global.__app.renderNetworkConfigDeviceOptions();
    assert.deepEqual(deviceSelect.__devices.map((device) => device.device), ['eth0'], 'bridge tab must not restore pending Wi-Fi devices');
    assert.equal(deviceSelect.options.some((option) => option.value === 'wlp4s0'), false, 'bridge options must not contain wlp4s0 pending');

    activeTab = 'ip';
    global.__app.networkMutationCoordinator.setPending('ip', null);
    global.__app.state.networkConfigPendingData = null;

    global.__app.state.networkConfigRollbackID = 'rollback-1';
    global.__app.state.networkConfigPendingData = { pending: true, id: 'rollback-1', device: 'eth0' };
    global.__app.networkMutationCoordinator.setPending('ip', global.__app.state.networkConfigPendingData);
    await global.__app.confirmNetworkConfig();
    assert.equal(global.__app.networkMutationCoordinator.active(), null, 'confirmed IP transaction must release bridge and DNS controls');
    assert.equal(global.__app.state.networkConfigPendingData, null);

    deviceSelect.__allDevices = [];
    global.__app.renderNetworkConfigDeviceOptions();
    assert.equal(deviceSelect.disabled, true);

    const pinnedNetworkConfigDevice = global.__app.pinnedNetworkConfigDevice;
    delete global.__app.pinnedNetworkConfigDevice;
    global.__app.onNetworkConfigDeviceChange();
    global.__app.setHostBridgeCreateEnabled(false);
    global.__app.setHostDNSOutput('');
    global.__app.pinnedNetworkConfigDevice = pinnedNetworkConfigDevice;

    const listeners = {};
    function eventElement(value = '') {
        return {
            value,
            disabled: false,
            textContent: '',
            innerHTML: '',
            options: [],
            addEventListener(name, handler) { listeners[this.id + ':' + name] = handler; },
            replaceChildren(fragment) { this.options = fragment.children; }
        };
    }
    const eventIDs = {
        'events-timeline': eventElement(),
        'events-status': eventElement(),
        'events-refresh-btn': eventElement(),
        'events-search-input': eventElement(),
        'events-severity-filter': eventElement(),
        'events-kind-filter': eventElement(),
        'events-range-filter': eventElement('all')
    };
    Object.entries(eventIDs).forEach(([id, element]) => {
        element.id = id;
        elementsByID.set(id, element);
    });
    const translations = {
        event_network_mutation_title: '网络配置操作：',
        network_mutation_kind_ip: '网卡配置',
        network_mutation_state_rolling_back: '正在回滚',
        event_kind_network_mutation_rolling_back: '网络配置正在回滚',
        event_source_network_control: '网络控制',
        events_info: '信息',
        events_empty: '没有事件',
        events_count_unit: '条事件',
        events_all_kinds: '全部类型'
    };
    global.__ = (key) => translations[key] || key;
    global.addEventListener = () => {};
    global.setInterval = () => 1;
    global.clearInterval = () => {};
    global.NetwatchAPI = {
        get: async (path) => path.includes('/app-traffic') ? { bridges: [] } : {
            kinds: ['network_mutation_rolling_back'],
            events: [{
                id: 'evt-1',
                timestamp: '2026-08-05 12:00:00',
                kind: 'network_mutation_rolling_back',
                severity: 'info',
                source: 'network_control',
                title: '网络配置操作：ip',
                summary: 'wlp4s0 / rolling_back',
                details: { mutation_kind: 'ip', target: 'wlp4s0', state: 'rolling_back' }
            }]
        }
    };
    load('web/events.js');
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setImmediate(resolve));
    assert.match(eventIDs['events-timeline'].innerHTML, /网络配置操作：网卡配置/);
    assert.match(eventIDs['events-timeline'].innerHTML, /wlp4s0 \/ 正在回滚/);
    assert.doesNotMatch(eventIDs['events-timeline'].innerHTML, />rolling_back</);

    eventIDs['events-search-input'].value = '回滚';
    listeners['events-search-input:input']();
    assert.match(eventIDs['events-timeline'].innerHTML, /wlp4s0/);
    eventIDs['events-search-input'].value = '不存在的事件';
    listeners['events-search-input:input']();
    assert.match(eventIDs['events-timeline'].innerHTML, /没有事件/);
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
