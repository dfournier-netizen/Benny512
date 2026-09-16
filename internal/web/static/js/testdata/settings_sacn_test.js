// settings_sacn_test.js — the sACN configuration form on the Settings
// screen, run against the LITERAL ui.js/settings.js the browser loads by
// internal/web/sacn_ui_test.go. Zero dependencies.
//
// WHAT IT PROTECTS
//
//  1. STAGED, NEVER LIVE. Settings is screen-wide Apply-to-confirm: typing
//     must reach nothing but the dirty marker and the worked example. An
//     sACN start universe that took effect on keystroke would repoint a live
//     rig mid-type.
//  2. ONE SAVE, TWO DOCUMENTS. The sACN configuration has its own endpoint
//     and its own validation, so Apply sends POST /api/settings AND POST
//     /api/sacn — with EXACTLY the three keys sacnConfigJSON names, because
//     the server's decoder sets DisallowUnknownFields and an extra key is a
//     flat 400 rather than a silent no-op.
//  3. THE SERVER'S OWN WORDS. The priority-0 refusal explains a limitation
//     of this build that no house phrase can carry. It must reach the screen
//     as written.
//  4. THE WORKED EXAMPLE. Which sACN universes this show is sourced as, and
//     the multicast group each lands on, composed from the value currently
//     in the box — applied or not.
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
let sacnStored = { startUniverse: 1, priority: 100, unicastTo: '' };
let rejectSacn = '';

// The server's OWN refusal, transcribed from sacn.ValidatePriority.
const PRIORITY_0_MESSAGE = 'sACN priority 0 is legal in ANSI E1.31-2025 Section 6.2.3 but this build\'s sender cannot transmit it (a zero Config.Priority means "use the default of 100"); choose 1..200';

const Api = {
  getSettings: async () => { calls.push({ name: 'getSettings' }); return { nic: '', pollIntervalMs: 3000, captureLimit: 10000, logRdmPath: '', artnetStartUniverse: 0, timeoutProfiles: {} }; },
  getNICs: async () => { calls.push({ name: 'getNICs' }); return []; },
  getSACNConfig: async () => { calls.push({ name: 'getSACNConfig' }); return JSON.parse(JSON.stringify(sacnStored)); },
  postSettings: async (body) => { calls.push({ name: 'postSettings', body }); return body; },
  postSACNConfig: async (body) => {
    calls.push({ name: 'postSACNConfig', body: JSON.parse(JSON.stringify(body)) });
    if (rejectSacn) throw new Error(rejectSacn);
    sacnStored = JSON.parse(JSON.stringify(body));
    return JSON.parse(JSON.stringify(sacnStored));
  },
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
function writes() { return calls.filter(c => c.name === 'postSettings' || c.name === 'postSACNConfig'); }

async function main() {
  sacnStored = { startUniverse: 7, priority: 120, unicastTo: '' };
  vm.runInContext('SettingsScreen.init();', ctx, { filename: 'drive' });
  await settle();

  console.log('1. the form is painted from the server');
  check($('sacnStartUniverse').value === '7', 'start universe came from GET /api/sacn', 'got ' + $('sacnStartUniverse').value);
  check($('sacnPriority').value === '120', 'priority came from GET /api/sacn', 'got ' + $('sacnPriority').value);
  check($('sacnUnicastTo').value === '', 'blank unicast means multicast', 'got ' + JSON.stringify($('sacnUnicastTo').value));

  console.log('2. typing is staged, never live, and restates the worked example');
  calls.length = 0;
  $('sacnStartUniverse').value = '1000';
  $('sacnStartUniverse').fire('input');
  await settle();
  check(writes().length === 0, 'typing sends nothing', 'calls: ' + JSON.stringify(calls.map(c => c.name)));
  const ex = $('sacnExample').textContent;
  check(/sACN universe 1000/.test(ex), 'the example names the sACN universe show universe 1 becomes', 'example: ' + ex);
  check(/239\.255\.3\.232/.test(ex), 'the example names the Table 9-10 multicast group for it', 'example: ' + ex);
  check(/not saved yet/i.test(ex), 'and says it is not in use yet', 'example: ' + ex);
  check(/unsaved change/i.test($('settingsDirtyPill').innerHTML), 'the unsaved marker is shown in words',
    'pill: ' + $('settingsDirtyPill').innerHTML);

  console.log('3. Apply sends both documents, with exactly the keys the server names');
  calls.length = 0;
  $('btnSaveSettings').fire('click');
  await settle();
  const posted = calls.filter(c => c.name === 'postSACNConfig');
  check(calls.some(c => c.name === 'postSettings'), 'the settings document was saved');
  check(posted.length === 1, 'the sACN document was saved exactly once', 'calls: ' + JSON.stringify(calls.map(c => c.name)));
  const keys = posted.length ? Object.keys(posted[0].body).sort() : [];
  check(JSON.stringify(keys) === JSON.stringify(['priority', 'startUniverse', 'unicastTo']),
    'the body carries exactly startUniverse/priority/unicastTo', 'keys: ' + JSON.stringify(keys));
  check(posted.length === 1 && posted[0].body.startUniverse === 1000, 'it carries the typed start universe',
    'body: ' + JSON.stringify(posted.length ? posted[0].body : null));
  check(/in use right now/i.test($('sacnExample').textContent), 'after the save the example says it is live',
    'example: ' + $('sacnExample').textContent);

  console.log('4. a refused save shows the server\'s own words');
  rejectSacn = PRIORITY_0_MESSAGE;
  $('sacnPriority').value = '0';
  $('sacnPriority').fire('input');
  await settle();
  $('btnSaveSettings').fire('click');
  await settle();
  const shown = $('settingsStatus').innerHTML;
  check(shown.indexOf('sACN priority 0 is legal in ANSI E1.31-2025') !== -1,
    'the server\'s refusal reaches the screen verbatim', 'status: ' + shown);
  check(/use the default of 100/.test(shown),
    'including the part that explains WHY this build cannot transmit it', 'status: ' + shown);
  check(/unsaved change/i.test($('settingsDirtyPill').innerHTML),
    'a half-applied save does not leave the form claiming to be clean',
    'pill: ' + $('settingsDirtyPill').innerHTML);

  if (failures) { console.log('\n' + failures + ' assertion(s) failed'); process.exit(1); }
  console.log('\nall assertions passed');
}

main().catch(e => { console.error(e); process.exit(1); });
