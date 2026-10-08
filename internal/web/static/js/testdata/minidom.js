// minidom.js — a small HTML DOM for this project's Node-run browser tests
// (console_test.js). Never loaded by the app. Zero dependencies.
//
// Covers what console.js and app.js use: elements with attributes, classList,
// dataset, style, text nodes, innerHTML (parsed by a small HTML parser, so
// the real index.html can be loaded), events with bubbling and on<type>
// handlers, querySelector/All/closest/matches for simple selectors (tag, #id,
// .class, [attr], [attr="v"], descendant combinator, comma lists), <dialog>
// showModal/close, form control value/disabled/hidden/checked.
'use strict';

const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr']);
const ENT = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ', hellip: '…', rarr: '→', larr: '←', mdash: '—', ndash: '–', middot: '·', times: '×', deg: '°' };
function decode(s) {
  return s.replace(/&(#x[0-9a-fA-F]+|#\d+|\w+);/g, (m, e) => {
    if (e[0] === '#') return String.fromCodePoint(e[1] === 'x' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10));
    return e in ENT ? ENT[e] : m;
  });
}

class Event {
  constructor(type, init) {
    init = init || {};
    Object.assign(this, init);
    this.type = type;
    this.bubbles = init.bubbles !== undefined ? init.bubbles : true;
    this.defaultPrevented = false;
    this._stop = false;
    this.target = null;
    this.currentTarget = null;
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this._stop = true; }
  stopImmediatePropagation() { this._stop = true; }
}

class EventTarget {
  constructor() { this._l = {}; }
  addEventListener(t, fn) { (this._l[t] = this._l[t] || []).push(fn); }
  removeEventListener(t, fn) { this._l[t] = (this._l[t] || []).filter(f => f !== fn); }
  _fire(ev) {
    ev.currentTarget = this;
    for (const fn of (this._l[ev.type] || []).slice()) fn.call(this, ev);
    const prop = this['on' + ev.type];
    if (typeof prop === 'function') prop.call(this, ev);
  }
  dispatchEvent(ev) {
    if (!ev.target) ev.target = this;
    let n = this;
    while (n) {
      n._fire(ev);
      if (ev._stop || !ev.bubbles) break;
      n = n.parentNode || (n.nodeType === 9 ? n.defaultView : null);
    }
    return !ev.defaultPrevented;
  }
}

class Node extends EventTarget {
  constructor(doc) { super(); this.ownerDocument = doc; this.parentNode = null; this.childNodes = []; }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get firstElementChild() { return this.children[0] || null; }
  appendChild(c) {
    if (c.nodeType === 11) { for (const k of c.childNodes.slice()) this.appendChild(k); return c; }
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    this.childNodes.push(c);
    return c;
  }
  insertBefore(c, ref) {
    if (!ref) return this.appendChild(c);
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    this.childNodes.splice(this.childNodes.indexOf(ref), 0, c);
    return c;
  }
  removeChild(c) {
    const i = this.childNodes.indexOf(c);
    if (i >= 0) this.childNodes.splice(i, 1);
    c.parentNode = null;
    return c;
  }
  append(...ns) { ns.forEach(n => this.appendChild(typeof n === 'string' ? this.ownerDocument.createTextNode(n) : n)); }
  replaceChildren(...ns) { this.childNodes.slice().forEach(c => this.removeChild(c)); this.append(...ns); }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  contains(n) { while (n) { if (n === this) return true; n = n.parentNode; } return false; }
  get textContent() { return this.childNodes.map(c => c.textContent).join(''); }
  set textContent(v) {
    this.childNodes.slice().forEach(c => this.removeChild(c));
    if (v !== '' && v !== null && v !== undefined) this.appendChild(this.ownerDocument.createTextNode(String(v)));
  }
  querySelectorAll(sel) {
    const groups = parseSelector(sel);
    const out = [];
    const walk = n => { for (const c of n.children) { if (groups.some(g => matchChain(c, g))) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

class Text extends Node {
  constructor(doc, data) { super(doc); this.nodeType = 3; this.data = data; }
  get textContent() { return this.data; }
  set textContent(v) { this.data = String(v); }
}

class Fragment extends Node { constructor(doc) { super(doc); this.nodeType = 11; } }

function camelToKebab(k) { return k.replace(/[A-Z]/g, c => '-' + c.toLowerCase()); }

class Element extends Node {
  constructor(doc, tag) {
    super(doc);
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.localName = tag.toLowerCase();
    this.attributes = new Map();
    this._value = undefined;
    this.checked = false;
    const self = this;
    const styleStore = {};
    this.style = new Proxy(styleStore, {
      get(t, k) {
        if (k === 'setProperty') return (p, v) => { t[p] = String(v); };
        if (k === 'removeProperty') return p => { delete t[p]; };
        if (k === 'getPropertyValue') return p => t[p] || '';
        return t[camelToKebab(String(k))] || '';
      },
      set(t, k, v) { t[camelToKebab(String(k))] = String(v); return true; },
    });
    this.dataset = new Proxy({}, {
      get(t, k) { return self.getAttribute('data-' + camelToKebab(String(k))) === null ? undefined : self.getAttribute('data-' + camelToKebab(String(k))); },
      set(t, k, v) { self.setAttribute('data-' + camelToKebab(String(k)), v); return true; },
    });
    this.classList = {
      _list: () => (self.getAttribute('class') || '').split(/\s+/).filter(Boolean),
      contains(c) { return this._list().includes(c); },
      add(...cs) { const l = this._list(); cs.forEach(c => { if (!l.includes(c)) l.push(c); }); self.setAttribute('class', l.join(' ')); },
      remove(...cs) { self.setAttribute('class', this._list().filter(c => !cs.includes(c)).join(' ')); },
      toggle(c, force) {
        const on = force === undefined ? !this.contains(c) : !!force;
        if (on) this.add(c); else this.remove(c);
        return on;
      },
    };
  }
  setAttribute(k, v) { this.attributes.set(k.toLowerCase(), String(v)); }
  getAttribute(k) { k = k.toLowerCase(); return this.attributes.has(k) ? this.attributes.get(k) : null; }
  hasAttribute(k) { return this.attributes.has(k.toLowerCase()); }
  removeAttribute(k) { this.attributes.delete(k.toLowerCase()); }
  get id() { return this.getAttribute('id') || ''; }
  set id(v) { this.setAttribute('id', v); }
  get className() { return this.getAttribute('class') || ''; }
  set className(v) { this.setAttribute('class', v); }
  get hidden() { return this.hasAttribute('hidden'); }
  set hidden(v) { if (v) this.setAttribute('hidden', ''); else this.removeAttribute('hidden'); }
  get disabled() { return this.hasAttribute('disabled'); }
  set disabled(v) { if (v) this.setAttribute('disabled', ''); else this.removeAttribute('disabled'); }
  get open() { return this.hasAttribute('open'); }
  set open(v) { if (v) this.setAttribute('open', ''); else this.removeAttribute('open'); }
  get value() {
    if (this.localName === 'select') {
      if (this._value !== undefined) return this._value;
      const opts = this.querySelectorAll('option');
      const sel = opts.find(o => o.hasAttribute('selected')) || opts[0];
      return sel ? sel.value : '';
    }
    if (this.localName === 'option') return this.getAttribute('value') !== null ? this.getAttribute('value') : this.textContent;
    return this._value !== undefined ? this._value : (this.getAttribute('value') || '');
  }
  set value(v) { this._value = String(v); }
  get innerHTML() { return this._html || ''; }
  set innerHTML(v) {
    this._html = String(v);
    this.childNodes.slice().forEach(c => this.removeChild(c));
    parseHTML(this.ownerDocument, String(v), this);
  }
  matches(sel) { return parseSelector(sel).some(g => matchChain(this, g)); }
  closest(sel) { let n = this; while (n && n.nodeType === 1) { if (n.matches(sel)) return n; n = n.parentNode; } return null; }
  click() { if (this.disabled) return; this.dispatchEvent(new Event('click', { bubbles: true })); }
  focus() { this.ownerDocument.activeElement = this; }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument.activeElement = null; }
  scrollIntoView() {}
  getBoundingClientRect() { return { x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 }; }
  setPointerCapture() {}
  releasePointerCapture() {}
  hasPointerCapture() { return false; }
  showModal() { this.open = true; }
  show() { this.open = true; }
  close() { if (!this.open) return; this.open = false; this.dispatchEvent(new Event('close', { bubbles: false })); }
}

// --- selectors --------------------------------------------------------------
function parseCompound(s) {
  const c = { tag: null, id: null, classes: [], attrs: [] };
  const re = /^([a-zA-Z][\w-]*|\*)|#([\w-]+)|\.([\w-]+)|\[([\w-]+)(?:([~^$*|]?=)"([^"]*)"|([~^$*|]?=)'([^']*)'|([~^$*|]?=)([^\]]*))?\]|:not\(([^)]*)\)/g;
  let m, pos = 0;
  while ((m = re.exec(s)) && m.index === pos) {
    pos = re.lastIndex;
    if (m[1]) c.tag = m[1] === '*' ? null : m[1].toLowerCase();
    else if (m[2]) c.id = m[2];
    else if (m[3]) c.classes.push(m[3]);
    else if (m[4]) c.attrs.push({ name: m[4].toLowerCase(), op: m[5] || m[7] || m[9] || null, val: m[6] !== undefined ? m[6] : m[8] !== undefined ? m[8] : m[10] });
    else if (m[11] !== undefined) (c.not = c.not || []).push(parseCompound(m[11].trim()));
  }
  if (pos !== s.length) throw new Error('minidom: unsupported selector ' + JSON.stringify(s));
  return c;
}
// split on sep (',' or whitespace) outside [...], (...) and quotes.
function splitTop(s, isSep) {
  const out = [];
  let cur = '', depth = 0, q = null;
  for (const ch of s) {
    if (q) { if (ch === q) q = null; cur += ch; continue; }
    if (ch === '"' || ch === "'") { q = ch; cur += ch; continue; }
    if (ch === '[' || ch === '(') depth++;
    if (ch === ']' || ch === ')') depth--;
    if (depth === 0 && isSep(ch)) { if (cur) out.push(cur); cur = ''; continue; }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}
function parseSelector(sel) {
  return splitTop(sel, c => c === ',').map(g => splitTop(g.trim(), c => /\s/.test(c)).map(parseCompound));
}
function matchCompound(el, c) {
  if (c.tag && el.localName !== c.tag) return false;
  if (c.id && el.id !== c.id) return false;
  for (const k of c.classes) if (!el.classList.contains(k)) return false;
  for (const a of c.attrs) {
    const v = el.getAttribute(a.name);
    if (v === null) return false;
    if (a.op === '=' && v !== a.val) return false;
  }
  if (c.not && c.not.some(n => matchCompound(el, n))) return false;
  return true;
}
function matchChain(el, chain) {
  if (!matchCompound(el, chain[chain.length - 1])) return false;
  let i = chain.length - 2, n = el.parentNode;
  while (i >= 0 && n && n.nodeType === 1) {
    if (matchCompound(n, chain[i])) i--;
    n = n.parentNode;
  }
  return i < 0;
}

// --- HTML parser ---------------------------------------------------------------
function parseHTML(doc, html, into) {
  const stack = [into];
  const top = () => stack[stack.length - 1];
  const re = /<!--[\s\S]*?-->|<!DOCTYPE[^>]*>|<\/([a-zA-Z][\w-]*)\s*>|<([a-zA-Z][\w-]*)((?:\s+[^\s=>\/]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*(\/?)>/gi;
  let m, last = 0;
  while ((m = re.exec(html))) {
    if (m.index > last) {
      const t = html.slice(last, m.index);
      if (t) top().appendChild(doc.createTextNode(decode(t)));
    }
    last = re.lastIndex;
    if (m[0].startsWith('<!')) continue;
    if (m[1]) {
      const tag = m[1].toLowerCase();
      for (let i = stack.length - 1; i > 0; i--) {
        if (stack[i].localName === tag) { stack.length = i; break; }
      }
      continue;
    }
    const tag = m[2].toLowerCase();
    const el = doc.createElement(tag);
    const attrRe = /([^\s=>\/]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g;
    let a;
    while ((a = attrRe.exec(m[3] || ''))) el.setAttribute(a[1], decode(a[2] !== undefined ? a[2] : a[3] !== undefined ? a[3] : a[4] !== undefined ? a[4] : ''));
    top().appendChild(el);
    if (tag === 'script' || tag === 'style') {
      const end = html.toLowerCase().indexOf('</' + tag, last);
      const body = html.slice(last, end < 0 ? html.length : end);
      if (body) el.appendChild(doc.createTextNode(body));
      re.lastIndex = last = end < 0 ? html.length : html.indexOf('>', end) + 1;
      continue;
    }
    if (!VOID.has(tag) && !m[4]) stack.push(el);
  }
  if (last < html.length) top().appendChild(doc.createTextNode(decode(html.slice(last))));
}

class Document extends Node {
  constructor() {
    super(null);
    this.ownerDocument = this;
    this.nodeType = 9;
    this.activeElement = null;
    this.documentElement = this.createElement('html');
    this.appendChild(this.documentElement);
    this.head = this.createElement('head');
    this.body = this.createElement('body');
    this.documentElement.appendChild(this.head);
    this.documentElement.appendChild(this.body);
  }
  createElement(tag) { return new Element(this, tag); }
  createElementNS(ns, tag) { return new Element(this, tag); }
  createTextNode(t) { return new Text(this, t); }
  createDocumentFragment() { return new Fragment(this); }
  getElementById(id) { return this.querySelector('#' + id); }
  // loadHTML replaces <head>/<body> with a parsed page (index.html).
  loadHTML(html) {
    const tmp = this.createElement('div');
    parseHTML(this, html, tmp);
    const htmlEl = tmp.querySelector('html') || tmp;
    const head = htmlEl.querySelector('head'), body = htmlEl.querySelector('body');
    if (head) { this.head.replaceChildren(...head.childNodes.slice()); }
    if (body) this.body.replaceChildren(...body.childNodes.slice());
    if (htmlEl.getAttribute && htmlEl.getAttribute('data-theme')) this.documentElement.setAttribute('data-theme', htmlEl.getAttribute('data-theme'));
  }
}

// makeWindow builds the globals a browser script expects.
function makeWindow() {
  const document = new Document();
  const win = new EventTarget();
  document.defaultView = win;
  const store = new Map();
  Object.assign(win, {
    document,
    localStorage: { getItem: k => (store.has(k) ? store.get(k) : null), setItem: (k, v) => store.set(k, String(v)), removeItem: k => store.delete(k) },
    getComputedStyle: () => ({ getPropertyValue: () => '' }),
    innerWidth: 1366, innerHeight: 900,
    scrollTo() {},
  });
  return { window: win, document, Event, CustomEvent: class CustomEvent extends Event { constructor(t, i) { super(t, i); this.detail = i && i.detail; } } };
}

module.exports = { makeWindow, Event };
