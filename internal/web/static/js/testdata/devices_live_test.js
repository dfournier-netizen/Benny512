'use strict';
// Runs the literal browser scripts, with controllable HTTP and live events.
const fs = require('fs'), path = require('path'), vm = require('vm'), assert = require('assert');
const source = name => fs.readFileSync(path.join(__dirname, '..', name), 'utf8');
const settle = async () => { for (let i = 0; i < 15; i++) await new Promise(setImmediate); };
const deferred = () => { let resolve; const promise = new Promise(r => resolve = r); return {promise, resolve}; };
function setup() {
  const elements = {}, listeners = {}, timers = new Map(); let timerID = 0;
  function element(id) {
    let html = '';
    const el = {id, children: [], handlers: {}, style: {}, dataset: {}, value: '', scrollTop: 0,
      classList: {toggle() {}}, querySelectorAll: () => [], querySelector: () => null,
      addEventListener(type, fn) { this.handlers[type] = fn; },
      appendChild(child) { this.children.push(child); }, setAttribute() {}, focus() {},
      get innerHTML() { return html; }, set innerHTML(v) { html = v; this.children = []; }};
    return el;
  }
  const doc = {getElementById: id => elements[id] || (elements[id] = element(id)),
    createElement: () => element(''), querySelectorAll: () => [], querySelector: () => null,
    addEventListener() {}, body: element('body')};
  const state = {nodes: [], fixtures: [], nodeGets: 0, fixtureGets: 0, probes: 0, calls: []};
  const api = {
    getNodes: async () => { state.nodeGets++; return state.nodes; },
    getFixtures: async () => { state.fixtureGets++; return state.fixtures; },
    discover: async (...args) => { state.calls.push(args); return {uids: [], complete: true}; },
    compareDevices: (a, b) => a.uid.localeCompare(b.uid),
    getParam: async () => { state.probes++; return {value: {}}; },
    getDeviceParam: async () => { state.probes++; return {}; }, formatAddressRange: () => '1',
  };
  const memory = new Map([['benny512.devices.nodeFilter', '2.11.90.4|2']]);
  const ctx = vm.createContext({console, Api: api, document: doc,
    window: {addEventListener() {}, dispatchEvent() {}},
    sessionStorage: {getItem: k => memory.get(k), setItem: (k,v) => memory.set(k,v)},
    setTimeout: fn => { timers.set(++timerID, fn); return timerID; }, clearTimeout: id => timers.delete(id),
    Live: {on: (type, fn) => { (listeners[type] ||= []).push(fn); }},
    UI: {formatUniverse: n => String(n + 1), icon: () => '', spinner: () => ''},
    escapeHtml: s => String(s),
    DeviceDetail: {init() {}, noteDeviceInfo() {}, _caches: {infoCache: {}}}});
  vm.runInContext(source('devices.js'), ctx);
  const screen = vm.runInContext('DevicesScreen', ctx);
  const emit = type => (listeners[type] || []).forEach(fn => fn({type}));
  const flush = async () => { const queue = [...timers.values()]; timers.clear(); queue.forEach(fn => fn()); await settle(); };
  return {state, api, screen, elements, doc, listeners, emit, flush, memory};
}
const node = (ip, bind, address, name = '') => ({ip, bindIndex: bind, longName: 'NETRON EN4', shortName: name,
  ports: [{index: 0, output: true, outputAddress: address}]});
const fixture = uid => ({uid, nodeIp: '2.11.90.4', bindIndex: 2, portAddress: 17,
  class: 'Fixture', model: uid, manufacturer: 'Test'});
const tests = {
  async 'clear completion restores discovery controls'() {
    const h = setup(); h.state.nodes = [node('2.11.90.4', 1, 0)];
    h.api.clearDevicesAll = async () => ({cleared: 0, todCleared: 0});
    h.screen.init(); await settle();
    h.elements.btnClearAll.handlers.click();
    await h.elements.btnClearConfirm.handlers.click();
    assert.equal(h.elements.btnDiscoverAll.disabled, false, 'clear must not leave discovery disabled');
    assert.equal(h.elements.btnDiscover.disabled, false);
  },
  async 'physical node grouping and honest bind labels preserve targets'() {
    const h = setup();
    h.state.nodes = [node('2.11.90.4', 2, 17), node('2.11.90.4', 1, 0), node('2.11.90.10', 1, 32)];
    h.screen.init(); await settle();
    assert.equal(h.elements.deviceNodeFilter.children.length, 2, 'one filter option per physical IP');
    assert.equal(h.elements.deviceNodeFilter.value, '2.11.90.4', 'migrate old bind-specific filter');
    const html = h.elements.fixtureNodeSelect.innerHTML;
    assert.match(html, /optgroup/);
    assert.match(html, /Bind 2/);
    assert.doesNotMatch(html, /Port 0/);
    assert.match(html, /2\.11\.90\.4\|2\|17/);
    assert.match(html, /universe 18/);
  },
  async 'live events and reconnect refresh registry without RDM probes'() {
    const h = setup(); h.screen.init(); await settle();
    const before = h.state.fixtureGets;
    h.state.fixtures = [fixture('fresh')];
    for (let i = 0; i < 50; i++) h.emit('rdm');
    await h.flush();
    assert.equal(h.state.fixtureGets, before + 1, 'burst coalesces into one snapshot');
    assert.match(h.elements.deviceList.innerHTML, /fresh/);
    h.state.nodes = [node('2.11.90.4', 1, 0)];
    h.emit('node'); await h.flush();
    assert.match(h.elements.fixtureNodeSelect.innerHTML, /2\.11\.90\.4/);
    h.state.fixtures = [fixture('after-reconnect')];
    h.emit('connected'); await h.flush();
    assert.match(h.elements.deviceList.innerHTML, /after-reconnect/);
    assert.equal(h.state.probes, 0);
  },
  async 'older HTTP snapshots cannot replace newer devices or nodes'() {
    const h = setup(); h.screen.init(); await settle();
    const old = deferred(); let calls = 0;
    h.api.getFixtures = () => ++calls === 1 ? old.promise : Promise.resolve([fixture('newest')]);
    const first = h.screen.refreshFixtures(); await h.screen.refreshFixtures();
    old.resolve([fixture('stale')]); await first;
    assert.match(h.elements.deviceList.innerHTML, /newest/);
    assert.doesNotMatch(h.elements.deviceList.innerHTML, />stale</);
    const oldNodes = deferred(); calls = 0;
    h.api.getNodes = () => ++calls === 1 ? oldNodes.promise : Promise.resolve([node('2.11.90.4', 2, 17)]);
    const firstNodes = h.screen.refreshNodes(); await h.screen.refreshNodes();
    oldNodes.resolve([]); await firstNodes;
    assert.match(h.elements.fixtureNodeSelect.innerHTML, /2\.11\.90\.4\|2\|17/);
  },
  async 'discover all snapshots every node, serializes, deduplicates and survives failure'() {
    const h = setup();
    h.state.nodes = [node('2.11.90.4', 1, 0), node('2.11.90.4', 2, 17),
      node('2.11.90.4', 2, 17), node('2.11.90.10', 1, 32)];
    h.screen.init(); await settle();
    const pending = deferred(); let active = 0, peak = 0;
    h.api.discover = async (...args) => {
      h.state.calls.push(args); peak = Math.max(peak, ++active);
      if (h.state.calls.length === 1) await pending.promise;
      active--;
      if (args[1] === 2) throw Error('offline');
      return {uids: ['same-uid'], complete: args[2] !== 32};
    };
    assert.equal(typeof h.elements.btnDiscoverAll.handlers.click, 'function', 'all-port button is wired');
    const run = h.elements.btnDiscoverAll.handlers.click(); await settle();
    assert.equal(h.state.calls.length, 1);
    await h.elements.btnDiscover.handlers.click();
    assert.equal(h.state.calls.length, 1, 'single-port button cannot overlap batch');
    h.state.nodes = []; await h.screen.refreshNodes();
    pending.resolve(); await run;
    assert.deepEqual(h.state.calls, [['2.11.90.4',1,0],['2.11.90.4',2,17],['2.11.90.10',1,32]]);
    assert.equal(peak, 1);
    assert.match(h.elements.discoverStatus.textContent, /1 failed/);
    assert.match(h.elements.discoverStatus.textContent, /1 incomplete/);
    assert.match(h.elements.discoverStatus.textContent, /1 unique/);
  },
  async 'stop leaves current discovery intact and skips remaining ports'() {
    const h = setup(); h.state.nodes = [node('2.11.90.4',1,0),node('2.11.90.4',2,1)];
    h.screen.init(); await settle(); const pending = deferred();
    h.api.discover = async (...args) => { h.state.calls.push(args); await pending.promise; return {uids: [],complete:true}; };
    assert.equal(typeof h.elements.btnDiscoverAll.handlers.click, 'function');
    const run = h.elements.btnDiscoverAll.handlers.click(); await settle();
    h.elements.btnStopDiscovery.handlers.click(); pending.resolve(); await run;
    assert.equal(h.state.calls.length, 1);
    assert.match(h.elements.discoverStatus.textContent, /Stopped/);
  },
  async 'literal WebSocket emits connection lifecycle on every open'() {
    const sockets = [];
    const ctx = vm.createContext({location: {protocol:'http:',host:'localhost'},
      document:{getElementById:()=>null}, setTimeout() {},
      WebSocket: class {constructor() {sockets.push(this);}}});
    vm.runInContext(source('ws.js'),ctx);
    const live = vm.runInContext('Live',ctx); let connections = 0;
    live.on('connected', () => connections++);
    sockets[0].onopen(); sockets[0].onopen();
    assert.equal(connections, 2);
  },
};
(async () => { let failed = 0;
  for (const [name, test] of Object.entries(tests)) {
    try { await test(); console.log('PASS '+name); }
    catch (e) { failed++; console.error('FAIL '+name+'\n'+e.stack); }
  }
  process.exitCode = failed ? 1 : 0;
})();
