'use strict';
// tests_sync_test.js — two "browsers", each running the LITERAL ws.js in its
// own context against a real Benny512 server (started by
// internal/web/tests_c5_test.go): real fetch, real WebSocket. Browser A
// starts a test sequence and steps it; browser B must learn of it through
// the "tests" broadcast alone and read the same revision and step. A write
// carrying a stale X-Benny-Tests revision is refused. Prints one JSON line.
//
// usage: node tests_sync_test.js <jsDir> <baseURL> <sequenceId>
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const [jsDir, base, sequenceId] = process.argv.slice(2);

function browser(name) {
  const url = new URL(base);
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, URL,
    WebSocket,
    location: { protocol: url.protocol, host: url.host },
    document: { getElementById: () => null },
  });
  vm.runInContext(fs.readFileSync(path.join(jsDir, 'ws.js'), 'utf8'), ctx, { filename: name + ':ws.js' });
  const live = vm.runInContext('Live', ctx);
  const b = { seen: 0, connected: false };
  live.on('connected', () => { b.connected = true; });
  live.on('tests', msg => { b.seen = msg.revision; });
  return b;
}

async function api(method, p, body, headers) {
  const res = await fetch(new URL(p, base), {
    method,
    headers: Object.assign({ 'Content-Type': 'application/json' }, headers || {}),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  return { status: res.status, body: text ? JSON.parse(text) : null };
}

const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(what, fn, ms = 3000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (fn()) return;
    await sleep(10);
  }
  throw new Error('timed out waiting for ' + what);
}

(async () => {
  const out = {};
  const A = browser('A'), B = browser('B');
  await until('both browsers to connect', () => A.connected && B.connected);
  const before = (await api('GET', '/api/tests')).body.revision;
  const started = await api('POST', '/api/tests/run-start', { id: sequenceId });
  if (started.status !== 200) throw new Error('run-start: ' + JSON.stringify(started));
  const next = await api('POST', '/api/tests/run-next', {}, { 'X-Benny-Tests': String(started.body.revision) });
  if (next.status !== 200) throw new Error('run-next: ' + JSON.stringify(next));
  out.aRevision = next.body.revision;
  await until('browser B to see A\'s Next through the broadcast', () => B.seen === out.aRevision);
  out.bSeen = B.seen;
  const bView = (await api('GET', '/api/tests')).body;
  out.bRevision = bView.revision;
  out.bStep = bView.run.step;
  out.bActive = bView.run.active;
  out.stale = (await api('POST', '/api/tests/run-back', {}, { 'X-Benny-Tests': String(before) })).status;
  await api('POST', '/api/tests/run-stop', {});
  console.log(JSON.stringify(out));
  process.exit(0);
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
