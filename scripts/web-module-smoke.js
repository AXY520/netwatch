#!/usr/bin/env node

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.resolve(__dirname, '..');
const documentStub = {
    activeElement: null,
    getElementById: () => null,
    querySelector: () => null,
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
    netwatchPost: async () => ({}),
    refreshNetworkDetailCards() {},
    refreshAppTrafficSoon() {}
};

function load(relativePath) {
    const filename = path.join(root, relativePath);
    vm.runInThisContext(fs.readFileSync(filename, 'utf8'), { filename });
}

async function main() {
    load('web/shared.js');
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

    [
        'web/app-network-config.js',
        'web/app-host-bridge.js',
        'web/app-host-dns.js'
    ].forEach(load);

    [
        'loadNetworkConfigDevices',
        'onNetworkConfigDeviceChange',
        'pinnedNetworkConfigDevice',
        'setHostBridgeCreateEnabled',
        'fillHostDNSFormFromInfo',
        'setHostDNSOutput'
    ].forEach((name) => assert.equal(typeof global.__app[name], 'function', `${name} must be exported`));

    await global.__app.loadNetworkConfigDevices();
    global.__app.onNetworkConfigDeviceChange();
    global.__app.setHostBridgeCreateEnabled(false);
    global.__app.setHostDNSOutput('');
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
