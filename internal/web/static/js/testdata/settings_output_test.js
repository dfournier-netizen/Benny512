// settings_output_test.js — the Output section of the Settings screen (C3:
// lease-loss action, per-protocol network adapters, per-universe protocol),
// run against the LITERAL ui.js/settings.js the browser loads by
// internal/web/output_c3_ui_test.go. Zero dependencies.
//
// WHAT IT PROTECTS (owner decisions, Dom 2026-10-06/07)
//  1. The lease-loss action is a Settings choice, Blackout or Hold last look,
//     and the page states that Blackout is the default.
//  2. Art-Net and sACN each have their OWN adapter setting.
//  3. Each universe outputs Art-Net, sACN or both; the choice is staged like
//     every other setting (Apply-to-confirm) and reaches POST /api/settings.
//
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

function makeEl(id) {
  const el = {
    id: id || '', _innerHTML: '', textContent: '', value: '', checked: false,
    disabled: false, className: '', style: {}, dataset: {}, options: [],
    children: [], handlers: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
    removeEventListener() {},
    appendChild(c) { this.children.push(c); return c; },
    removeChild() {}, remove() {}, setAttribute() {}, getAttribute() { return null; },
    focus() {}, blur() {}, click() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    contains() { return true; },
    fire(type) {
      const ev = { target: this, currentTarget: this, preventDefault() {} };
      (this.handlers[type] || []).forEach(fn => fn(ev));
    },
  };
  Object.defineProperty(el, 'innerHTML', { get: () => el._innerHTML, set: v => { el._innerHTML = String(v); } });
  return el;
}

const byId = new Map();
const doc = {
  getElementById(id) {
    if (!byId.has(id)) byId.set(id, makeEl(id));
    return byId.get(id);
  },
  querySelector() { return null; },
  querySelectorAll() { return []; },
  createElement(tag) { const e = makeEl(''); e.tagName = tag; return e; },
  addEventListener() {}, removeEventListener() {},
};
doc.body = makeEl('body');


const calls = [];
let settingsStored = { nic: 'eth0', pollIntervalMs: 3000, captureLimit: 10000, logRdmPath: '', artnetStartUniverse: 0, timeoutProfiles: {},
  sacnNic: '', leaseLossAction: 'blackout', universeProtocols: [{ universe: 3, protocol: 'sacn' }] };
const outputStatus = { state: 'disarmed', simulated: false, error: '', leaseLossAction: 'blackout', browsers: 1, lastDisarm: '',
  universes: [{ universe: 0, protocol: 'artnet', sacnUniverse: 1, sacnError: '' }, { universe: 3, protocol: 'sacn', sacnUniverse: 4, sacnError: '' }] };

const Api = {
  getSettings: async () => { calls.push({ name: 'getSettings' }); return JSON.parse(JSON.stringify(settingsStored)); },
  getNICs: async () => { calls.push({ name: 'getNICs' }); return [{ name: 'eth0', ipv4: ['10.0.0.2'], up: true }, { name: 'eth1', ipv4: ['2.0.0.9'], up: true }]; },
  getSACNConfig: async () => ({ startUniverse: 1, priority: 100, unicastTo: '' }),
  getOutput: async () => { calls.push({ name: 'getOutput' }); return JSON.parse(JSON.stringify(outputStatus)); },
  postSettings: async (body) => { calls.push({ name: 'postSettings', body: JSON.parse(JSON.stringify(body)) }); settingsStored = JSON.parse(JSON.stringify(body)); return { status: 'ok' }; },
  postSACNConfig: async (body) => { calls.push({ name: 'postSACNConfig', body }); return body; },
  fullReset: async () => ({ deleted: [], errors: [] }),
};

const win = { handlers: {}, addEventListener(t, f) { (this.handlers[t] = this.handlers[t] || []).push(f); }, dispatchEvent() { return true; } };
class CustomEventStub { constructor(t, i) { this.type = t; this.detail = (i && i.detail) || null; } }

const ctx = vm.createContext({
  window: win, document: doc, console, Api, CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Live: { enterShutdown() {} },
  escapeHtml: (s) => String(s == null ? '' : s),
});
ctx.globalThis = ctx;
for (const f of ['ui.js', 'settings.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}

const tick = () => new Promise(r => setTimeout(r, 0));
async function settle(n) { for (let i = 0; i < (n || 20); i++) await tick(); }
const $ = id => doc.getElementById(id);

async function main() {
  vm.runInContext('SettingsScreen.init();', ctx, { filename: 'drive' });
  await settle();
  const page = $('screen-settings').innerHTML;

  console.log('1. the Output section exists and names its default');
  check(/Hold last look/.test(page) && /Blackout/.test(page), 'the page offers Blackout and Hold last look', '');
  check(/default[^.]{0,40}Blackout|Blackout[^.]{0,40}default/i.test(page), 'the page states that Blackout is the default');
  check($('leaseLossAction').value === 'blackout', 'the lease-loss choice is painted from the server', 'got ' + $('leaseLossAction').value);
  check(/Art-Net network adapter/i.test(page) && /sACN network adapter/i.test(page), 'Art-Net and sACN each have their own adapter setting');
  check($('sacnNicSelect').value === '', 'the sACN adapter is painted from the server (blank = same as Art-Net)', 'got ' + JSON.stringify($('sacnNicSelect').value));
  check($('outProto-0').value === 'artnet' && $('outProto-3').value === 'sacn', 'each universe row shows its protocol', 'u0=' + $('outProto-0').value + ' u3=' + $('outProto-3').value);

  console.log('2. choices are staged, never live');
  calls.length = 0;
  $('leaseLossAction').value = 'hold'; $('leaseLossAction').fire('change');
  $('sacnNicSelect').value = 'eth1'; $('sacnNicSelect').fire('change');
  $('outProto-0').value = 'both'; $('outProto-0').fire('change');
  await settle();
  check(!calls.some(c => c.name === 'postSettings'), 'changing them sends nothing', JSON.stringify(calls.map(c => c.name)));
  check(/3 unsaved changes/i.test($('settingsDirtyPill').innerHTML), 'all three count as unsaved changes, in words', $('settingsDirtyPill').innerHTML);

  console.log('3. Apply carries them to POST /api/settings');
  $('btnSaveSettings').fire('click');
  await settle();
  const post = calls.find(c => c.name === 'postSettings');
  check(!!post, 'Apply posted the settings document');
  const b = post ? post.body : {};
  check(b.leaseLossAction === 'hold', 'leaseLossAction travels', JSON.stringify(b));
  check(b.sacnNic === 'eth1', 'sacnNic travels', JSON.stringify(b));
  check(b.nic === 'eth0', 'the Art-Net adapter is untouched', JSON.stringify(b));
  const rows = (b.universeProtocols || []).map(r => r.universe + ':' + r.protocol).sort();
  check(JSON.stringify(rows) === JSON.stringify(['0:both', '3:sacn']), 'universeProtocols carries every non-default row', JSON.stringify(b.universeProtocols));

  if (failures) { console.log('\n' + failures + ' assertion(s) failed'); process.exit(1); }
  console.log('\nall assertions passed');
}

main().catch(e => { console.error(e); process.exit(1); });
