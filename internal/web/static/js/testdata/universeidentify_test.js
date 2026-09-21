'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Exercise the literal API wrapper and Send screen, with a small DOM and an
// in-memory HTTP peer. No real network output or third-party dependencies.
const nodes = new Map(), listeners = new Map(), timers = new Map();
let nextTimer = 0;
function node(id = '') {
  const n = { id, value: '', textContent: '', disabled: false, handlers: {}, dataset: {},
    classList: { toggle() {}, add() {}, remove() {} },
    addEventListener(name, fn) { this.handlers[name] = fn; },
    appendChild() {}, setAttribute() {},
    fire(name) { return this.handlers[name]({ target: this }); },
  };
  Object.defineProperty(n, 'innerHTML', { set(html) {
    for (const m of html.matchAll(/<(?:input|select|button|span|p|div)[^>]*\bid="([^"]+)"[^>]*>/g)) {
      const child = node(m[1]);
      const value = /\bvalue="([^"]*)"/.exec(m[0]);
      child.value = value ? value[1] : '';
      child.disabled = /\bdisabled\b/.test(m[0]);
      nodes.set(m[1], child);
    }
    if (nodes.has('identifyProtocol')) nodes.get('identifyProtocol').value ||= 'artnet';
  } });
  return n;
}
nodes.set('screen-send', node('screen-send'));
let server = { armed: false, running: false }, serial = 0, calls = [], failStop = false;
let pauseArm = null, pauseStart = null;
const copy = value => JSON.parse(JSON.stringify(value));
const ctx = vm.createContext({
  console, Uint8Array,
  btoa: s => Buffer.from(s, 'binary').toString('base64'),
  document: { getElementById: id => nodes.get(id) || null, createElement: () => node(), querySelector: () => null },
  window: { addEventListener: (name, fn) => listeners.set(name, fn), dispatchEvent() {} },
  setTimeout: fn => { timers.set(++nextTimer, fn); return nextTimer; },
  clearTimeout: id => timers.delete(id),
  escapeHtml: String,
  UI: { icon: () => '', userInputAttrs: () => ({ min: 1, max: 32768 }), formatUser: n => n + 1,
    universeScheme: () => 'user universe', parseUser: v => Number(v) - 1 },
  fetch: async (url, opts) => {
    const body = opts.body ? JSON.parse(opts.body) : undefined;
    calls.push({ url, method: opts.method, body, keepalive: opts.keepalive });
    let status = 200, data;
    if (url === '/api/dmx/identify') data = copy(server);
    else if (url.endsWith('/identify/arm')) {
      if (pauseArm) await pauseArm;
      server = { ...body, armed: true, running: false };
      server.token = 'lease-' + ++serial;
      data = copy(server);
    } else if (url.endsWith('/identify/start')) {
      if (pauseStart) await pauseStart;
      if (!server.armed || server.token !== body.token) { status = 409; data = { error: 'Arm expired' }; }
      else { server.running = true; data = copy(server); }
    } else if (url.endsWith('/identify/heartbeat')) {
      if (!server.armed || server.token !== body.token) { status = 409; data = { error: 'Arm expired' }; }
      else data = copy(server);
    } else if (url.endsWith('/identify/stop')) {
      if (failStop) throw new Error('connection lost');
      server = { ...server, armed: false, running: false, token: '' }; data = copy(server);
    } else data = {};
    return { ok: status === 200, status, headers: { get: () => null }, text: async () => JSON.stringify(data) };
  },
});
for (const filename of ['api.js', 'universeidentify.js', 'send.js']) {
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', filename), 'utf8'), ctx, { filename });
}
const screen = vm.runInContext('SendScreen', ctx);
const identify = vm.runInContext('UniverseIdentify', ctx);
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const writes = () => calls.filter(c => c.method === 'POST');
const click = id => nodes.get(id).fire('click');
function setRange(from, to, protocol = 'artnet') {
  nodes.get('identifyFrom').value = String(from);
  nodes.get('identifyTo').value = String(to);
  nodes.get('identifyProtocol').value = protocol;
  nodes.get('identifyFrom').fire('input');
}

(async () => {
  screen.init(); await flush();
  assert.equal(nodes.get('identifyFrom').value, '');
  assert.equal(nodes.get('identifyTo').value, '');
  assert.equal(writes().length, 0, 'loading Send must never infer or transmit a patch scope');
  assert.equal(nodes.get('identifyArm').disabled, true);
  await click('identifyStart');
  assert.equal(writes().length, 0, 'Start without Arm must be inert');

  for (const bounds of [['', ''], ['', '2'], ['1', ''], ['2', '1'], ['1.5', '2'], ['1', '513'], ['0', '1', 'sacn']]) {
    setRange(...bounds);
    assert.equal(nodes.get('identifyArm').disabled, true, `invalid ${bounds}`);
    await click('identifyArm');
    assert.equal(writes().length, 0);
  }
  setRange(0, 2);
  await click('identifyArm');
  assert.deepEqual(writes()[0], { url: '/api/dmx/identify/arm', method: 'POST', body: { protocol: 'artnet', from: 0, to: 2 }, keepalive: undefined });
  assert.equal(server.running, false, 'Arm sends no Identify start');
  assert.equal(nodes.get('identifyStart').disabled, false);
  assert.match(nodes.get('identifyStatus').textContent, /Armed.*Art-Net 0–2/);
  await click('identifyStart');
  assert.equal(server.running, true);
  assert.match(nodes.get('identifyStatus').textContent, /Identifying/);
  const heartbeat = [...timers.values()][0]; timers.clear();
  await heartbeat();
  assert.equal(calls.at(-1).url, '/api/dmx/identify/heartbeat');
  assert.equal(calls.at(-1).body.token, server.token);

  setRange(10, 12); await flush();
  assert.equal(server.armed, false, 'editing live range must disarm old output');
  assert.equal(timers.size, 0);
  assert.equal(nodes.get('identifyStart').disabled, true);
  await click('identifyArm');
  nodes.get('identifyProtocol').value = 'sacn';
  nodes.get('identifyProtocol').fire('change'); await flush();
  assert.equal(server.armed, false, 'protocol edits require a fresh arm');
  await click('identifyArm');
  assert.deepEqual(writes().at(-1).body, { protocol: 'sacn', from: 10, to: 12 });
  await click('identifyStart');
  screen.onLeaveScreen(); await flush();
  assert.equal(server.running, false, 'leaving Send must disarm');

  // A delayed Arm response after range edit must never authorize Start.
  let release;
  pauseArm = new Promise(resolve => { release = resolve; });
  const pendingArm = click('identifyArm');
  setRange(20, 21); await flush();
  release(); await pendingArm; pauseArm = null; await flush();
  assert.equal(server.armed, false);
  assert.equal(nodes.get('identifyStart').disabled, true);

  // Stop can overtake Start; the stale token must not restart output.
  await click('identifyArm');
  pauseStart = new Promise(resolve => { release = resolve; });
  const pendingStart = click('identifyStart');
  await click('identifyStop');
  release(); await pendingStart; pauseStart = null;
  assert.equal(server.running, false);
  assert.equal(nodes.get('identifyStart').disabled, true);

  await click('identifyArm'); await click('identifyStart');
  failStop = true;
  await click('identifyStop');
  assert.match(nodes.get('identifyStatus').textContent, /unknown/i);
  assert.match(nodes.get('identifyMessage').textContent, /Stop not confirmed/);
  assert.equal(nodes.get('identifyStart').disabled, true);
  assert.equal(timers.size, 0, 'failed Stop must not continue heartbeats');
  failStop = false; await identify.disarm();

  await click('identifyArm'); await click('identifyStart');
  listeners.get('pagehide')(); await flush();
  assert.equal(calls.at(-1).keepalive, true);
  assert.equal(server.running, false);
  assert.equal(timers.size, 0);

  // Reading a different session never adopts its lease or keeps it alive.
  server = { armed: true, running: true, protocol: 'artnet', from: 7, to: 7, token: 'foreign' };
  await identify.refresh();
  assert.match(nodes.get('identifyStatus').textContent, /another session/);
  assert.equal(nodes.get('identifyStart').disabled, true);
  assert.equal(timers.size, 0);
  await click('identifyStop');
  assert.equal(server.running, false);
  console.log('Universe Identify UI: validation, explicit arm, raw ranges, lifecycle and request races passed.');
})().catch(err => { console.error(err); process.exitCode = 1; });
