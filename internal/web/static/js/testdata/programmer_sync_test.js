'use strict';
// programmer_sync_test.js — two "browsers", each running the LITERAL api.js,
// ws.js and programmer.js in its own context against a real Benny512 server
// (started by internal/web/programmer_sync_test.go): real fetch, real
// WebSocket. Browser A selects and sets; browser B must catch up through the
// "programmer" broadcast alone; a write carrying a stale revision is
// refused. Prints one JSON result line.
//
// usage: node programmer_sync_test.js <jsDir> <baseURL> <entryId>
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const [jsDir, base, entryId] = process.argv.slice(2);

function browser(name) {
  const url = new URL(base);
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, URL, btoa,
    WebSocket,
    fetch: (p, opts) => fetch(new URL(p, base), opts),
    location: { protocol: url.protocol, host: url.host },
    document: { getElementById: () => null },
    window: { dispatchEvent() {}, addEventListener() {} },
    CustomEvent: class { constructor(t) { this.type = t; } },
  });
  for (const f of ['api.js', 'ws.js', 'programmer.js']) {
    vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: name + ':' + f });
  }
  return vm.runInContext('({ Api, ProgrammerSync })', ctx);
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

function dimmer(st) {
  for (const g of (st && st.groups) || []) {
    for (const a of g.attributes) if (a.attribute === 'Dimmer') return a.value;
  }
  return undefined;
}

(async () => {
  const out = {};
  const A = browser('A'), B = browser('B');
  await until('both browsers to load the programmer', () => A.ProgrammerSync.state() && B.ProgrammerSync.state());
  const startRev = B.ProgrammerSync.revision();
  await A.ProgrammerSync.act('select', { action: 'set', targets: [{ entryId, cell: '' }] });
  const res = await A.ProgrammerSync.act('set', { attribute: 'Dimmer', dmx: 65535 });
  out.aRevision = res.revision;
  await until('browser B to see A\'s dimmer through the broadcast',
    () => B.ProgrammerSync.revision() === res.revision && dimmer(B.ProgrammerSync.state()) === 65535);
  out.bRevision = B.ProgrammerSync.revision();
  out.bSelection = B.ProgrammerSync.state().selection.map(s => s.entryId);
  out.bDimmer = dimmer(B.ProgrammerSync.state());
  try {
    await B.Api.programmerAction('set', { attribute: 'Dimmer', dmx: 0 }, startRev);
    out.stale = 'accepted';
  } catch (e) {
    out.stale = e.message;
  }
  // B's own queued writes carry its current revision and succeed.
  const r2 = await B.ProgrammerSync.act('set', { attribute: 'Dimmer', dmx: 1000 });
  const r3 = await B.ProgrammerSync.act('set', { attribute: 'Dimmer', dmx: 2000 });
  out.bChain = [r2.revision, r3.revision];
  await until('browser A to see B\'s last write', () => dimmer(A.ProgrammerSync.state()) === 2000);
  out.aDimmer = dimmer(A.ProgrammerSync.state());
  // C4b: tools broadcast like every other change.
  await A.ProgrammerSync.act('highlight', { highlight: true });
  await until('browser B to see Highlight on', () => B.ProgrammerSync.state().highlight.on === true);
  await A.ProgrammerSync.act('groups/store', { name: 'From A' });
  await until('browser B to see the stored group', () => (B.ProgrammerSync.state().storedGroups || []).length === 1);
  out.bHighlight = B.ProgrammerSync.state().highlight.on;
  out.bGroup = B.ProgrammerSync.state().storedGroups[0].name;
  console.log(JSON.stringify(out));
  process.exit(0);
})().catch(e => { console.log(JSON.stringify({ error: String(e && e.stack || e) })); process.exit(1); });
