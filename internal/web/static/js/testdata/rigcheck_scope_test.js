'use strict';

// Exercise the shipped panel, rendered controls and request bodies. The API is
// recorded locally; this test cannot send lighting output or reach a network.
const assert = require('assert/strict');
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const decode = s => s.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
const escapeHtml = s => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function container() {
  let html = '', controls = [];
  return {
    get innerHTML() { return html; },
    set innerHTML(value) {
      html = value;
      controls = [];
      const re = /<(button|input|select)\b([^>]*?)(?:>([\s\S]*?)<\/\1>|>)/g;
      for (const match of html.matchAll(re)) {
        const attrs = {};
        for (const a of match[2].matchAll(/([\w-]+)="([^"]*)"/g)) attrs[a[1]] = decode(a[2]);
        const el = {
          id: attrs.id, dataset: {}, value: attrs.value || '', checked: /\bchecked\b/.test(match[2]),
          disabled: /\bdisabled\b/.test(match[2]), handlers: {},
          getAttribute: name => attrs[name],
          addEventListener(name, fn) { this.handlers[name] = fn; },
          async fire(name) {
            assert.ok(this.handlers[name], `${this.id || JSON.stringify(attrs)} has ${name} handler`);
            await this.handlers[name]({ target: this, currentTarget: this });
          },
        };
        for (const [key, val] of Object.entries(attrs)) {
          if (key.startsWith('data-')) el.dataset[key.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = val;
        }
        if (match[1] === 'select') {
          const options = [...(match[3] || '').matchAll(/<option\b([^>]*)>/g)];
          const chosen = options.find(o => /\bselected\b/.test(o[1])) || options[0];
          if (chosen) el.value = decode((/value="([^"]*)"/.exec(chosen[1]) || ['', ''])[1]);
        }
        controls.push({ el, attrs });
      }
    },
    querySelectorAll(selector) {
      const attr = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(selector);
      return controls.filter(({ attrs }) => selector[0] === '#'
        ? attrs.id === selector.slice(1)
        : attr && attr[1] in attrs && (attr[2] === undefined || attrs[attr[1]] === attr[2])).map(c => c.el);
    },
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
  };
}

async function main() {
  const calls = [], store = new Map();
  let rejectScope = false;
  let state = {
    scopeKind: 'all', scopeFixtureType: '', totalScope: 3, outputEnabled: false,
    available: [], tests: [], baseState: {},
    fixtureTypes: [
      { key: 'Martin ERA 800 Performance', label: 'Martin ERA 800 Performance', count: 2 },
      { key: 'GLP JDC-1', label: 'GLP JDC-1', count: 1 },
    ],
  };
  const copy = value => JSON.parse(JSON.stringify(value));
  const Api = new Proxy({
    async getPattern() { calls.push({ name: 'getPattern' }); return copy(state); },
    async patternSetScope(body) {
      calls.push({ name: 'patternSetScope', body: copy(body) });
      if (rejectScope) throw new Error('fixture type no longer exists');
      state = { ...state, scopeKind: body.scopeKind, scopeFixtureType: body.fixtureType, totalScope: 2 };
      return copy(state);
    },
  }, { get(target, name) {
    if (name in target) return target[name];
    return async () => { calls.push({ name }); throw new Error(`Unexpected output/mutation: ${name}`); };
  } });
  const context = vm.createContext({
    Api, escapeHtml, console,
    sessionStorage: { getItem: k => store.get(k) || null, setItem: (k, v) => store.set(k, v) },
    window: { addEventListener() {} }, document: { body: { contains: () => true } },
    setInterval: () => 1, clearInterval() {}, confirm: () => { throw new Error('Unexpected output confirmation'); },
  });
  // The REAL ui.js, not a stub of it. rigcheck.js reads the shared protocol
  // vocabulary (UI.RC_PROTOCOLS) and the shared numbering helpers from it;
  // a hand-written UI stub here would go stale the moment either grows, and
  // would hide exactly the kind of drift these tests exist to catch.
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'ui.js'), 'utf8'), context);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'rigcheck.js'), 'utf8') + '\nthis.panel = RigCheckPanel;', context);
  const root = container();
  context.panel.init({ getEntries: () => [] });
  await context.panel.enter(root);
  const control = selector => { const el = root.querySelector(selector); assert.ok(el, `rendered control ${selector}`); return el; };
  const writes = () => calls.filter(c => c.name !== 'getPattern');

  await control('[data-rcp-scope="fixtureType"]').fire('click');
  assert.equal(writes().length, 0, 'choosing fixture-type scope must wait for Apply');
  const select = control('#rcpScopeFixtureType');
  select.value = 'Martin ERA 800 Performance';
  await select.fire('change');
  assert.equal(writes().length, 0, 'choosing a fixture type must wait for Apply');
  await control('#rcpScopeApply').fire('click');
  assert.deepEqual(writes(), [{ name: 'patternSetScope', body: { scopeKind: 'fixtureType', fixtureType: 'Martin ERA 800 Performance' } }]);
  assert.equal(control('#rcpScopeFixtureType').value, 'Martin ERA 800 Performance');

  context.panel.leave();
  await context.panel.enter(root);
  assert.equal(control('#rcpScopeFixtureType').value, 'Martin ERA 800 Performance', 'accepted scope survives re-entry');
  assert.equal(writes().length, 1, 're-entry must not rewrite accepted scope');

  rejectScope = true;
  const rejected = control('#rcpScopeFixtureType');
  rejected.value = 'GLP JDC-1';
  await rejected.fire('change');
  assert.equal(writes().length, 1, 'second selection remains a draft');
  await control('#rcpScopeApply').fire('click');
  assert.equal(writes().length, 2);
  assert.equal(control('#rcpScopeFixtureType').value, 'Martin ERA 800 Performance', 'rejection restores accepted server scope');
  assert.match(root.innerHTML, /fixture type no longer exists/, 'rejection is visible');
  assert.ok(writes().every(c => c.name === 'patternSetScope'), 'scope editing never starts output or selects tests');
  assert.equal(context.panel.outputEnabled(), false);
  console.log('Rig Check fixture-type scope: Apply, accepted scope, rejection, and no output passed');
}

main().catch(err => { console.error(err); process.exitCode = 1; });
