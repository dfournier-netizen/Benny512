// theme.js — which colour theme this browser shows (C9), and the browser's
// copy of the theme contract. Loaded as a BLOCKING script in <head>, after
// the token stylesheets, so it sets data-theme on <html> before the first
// paint: no flash of the wrong theme.
//
// SCOPE: per browser. A phone outdoors and a laptop at front of house want
// different themes, so the choice lives in this browser's localStorage and
// nowhere else. Storage that is missing, unreadable (private window,
// blocked site data) or holds a value this build does not know means Dark —
// the theme the instrument is designed for in a dark venue. Custom themes
// themselves are station settings (GET /api/themes); this browser keeps a
// copy of the one it uses so it can apply it before the first paint, and
// sync() replaces or drops that copy once the server's list arrives.
//
// Themes are role values only (theme-contract.md): data-theme="dark" or
// "light" selects a block of css/console-tokens.css, and a custom theme
// sets that block's opaque colour roles (and the bounded live-preview
// intensity) as custom properties on <html>. Nothing here touches a size,
// border, glyph or word.
//
// THE CONTRACT below (contrast, OKLab, validate) is a port of
// docs/design/console-lite/build-assets.mjs. The server has its own copy
// (internal/web/theme.go) and checks every save; console_c9_test.go runs
// both over contrast.json and a set of failing drafts and requires the same
// numbers and the same words.
var B5Theme = (function () {
  'use strict';
  var KEY = 'b5-theme';
  var CUSTOM_KEY = 'b5-theme-custom';
  var BUILT_IN = { dark: true, light: true };
  var HEX = /^#[0-9a-f]{6}$/;
  var root = document.documentElement;
  var applied = 'dark';
  var chosenCustom = null; // the custom theme object in use, or null
  var props = [];          // custom properties this script set on <html>

  // --- the contract --------------------------------------------------------
  var SURFACES = ['bg-canvas', 'bg-surface', 'bg-surface-raised', 'bg-surface-overlay', 'bg-inset', 'bg-hover', 'bg-selected',
    'grid-bg', 'xypad-bg', 'ok-fill', 'warn-fill', 'danger-fill', 'accent-fill', 'armed-fill', 'bg-pressed', 'info-fill'];
  var TEXT45 = ['armed-text', 'arm-pill-text', 'simulated-text', 'grid-layer-label', 'value-set', 'value-default', 'value-test'];
  var DECORATIVE = ['border-subtle', 'border-default', 'grid-line', 'grid-line-major'];
  var LIMIT_020 = ['bg-canvas', 'bg-surface'];
  var LIMIT_035 = ['bg-surface-raised', 'bg-surface-overlay', 'bg-inset', 'bg-hover', 'bg-selected', 'bg-pressed',
    'ok-fill', 'warn-fill', 'danger-fill', 'accent-fill', 'armed-fill', 'info-fill'];
  var LARGE = ['bg-canvas', 'bg-surface', 'grid-bg', 'xypad-bg'];
  var TONES = ['armed-border', 'danger-border', 'warn-border', 'ok-border', 'accent-border', 'info-border'];
  var MIN_TONE = 0.04;
  var PREVIEW_DARK_MAX = 0.15;

  function ruleFor(key) {
    var trackAnd = ['track'].concat(SURFACES);
    if (key === 'disarm-slab-bg') return { against: ['disarm-slab-text'], min: 4.5, background: true };
    if (key === 'disarm-slab-text' || key === 'text-on-danger') return { against: ['disarm-slab-bg'], min: 4.5 };
    if (key === 'text-on-accent') return { against: ['thumb'], min: 4.5 };
    if (key === 'text-disabled') return { against: SURFACES, min: 0 };
    if (key === 'text-primary') return { against: SURFACES, min: 7 };
    if (key.indexOf('text-') === 0 || TEXT45.indexOf(key) >= 0) return { against: SURFACES, min: 4.5 };
    if (DECORATIVE.indexOf(key) >= 0) return { against: ['grid-bg', 'bg-surface'], min: 0 };
    if (key === 'thumb-border' || key === 'track-border' || key === 'thumb') return { against: trackAnd, min: 3 };
    if (key.indexOf('border') >= 0 || key.indexOf('fixture-') === 0 || key.indexOf('crosshair') >= 0 || key.indexOf('mixed-band') >= 0) return { against: SURFACES, min: 3 };
    return { against: ['text-primary', 'text-secondary', 'text-muted'], min: 4.5, background: true };
  }
  function lin(c) { return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); }
  function channel(hex, i) { return lin(parseInt(hex.slice(1 + 2 * i, 3 + 2 * i), 16) / 255); }
  function luminance(hex) { return 0.2126 * channel(hex, 0) + 0.7152 * channel(hex, 1) + 0.0722 * channel(hex, 2); }
  function contrast(a, b) {
    var x = luminance(a), y = luminance(b);
    if (x < y) { var t = x; x = y; y = t; }
    return (x + 0.05) / (y + 0.05);
  }
  function oklab(hex) {
    var r = channel(hex, 0), g = channel(hex, 1), b = channel(hex, 2);
    var l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
    var m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
    var s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
    return [0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
      1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
      0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s];
  }
  function deltaE(a, b) {
    var x = oklab(a), y = oklab(b);
    return Math.sqrt((x[0] - y[0]) * (x[0] - y[0]) + (x[1] - y[1]) * (x[1] - y[1]) + (x[2] - y[2]) * (x[2] - y[2]));
  }
  // A failing value is shown floored (a 4.47 never reads "4.5:1; needs
  // 4.5:1"), a too-bright one rounded up — the same rule as theme.go.
  function floorShow(v, min, from) {
    for (var d = from; d <= 6; d++) {
      var p = Math.pow(10, d), s = Math.floor(v * p) / p;
      if (s < min || d === 6) return s.toFixed(d);
    }
    return '';
  }
  function ceilShow(v, max) {
    for (var d = 3; d <= 8; d++) {
      var p = Math.pow(10, d), s = Math.ceil(v * p) / p;
      if (s > max || d === 8) return s.toFixed(d);
    }
    return '';
  }
  function num(v) { return String(v); }
  function labelOf(labels, role) { return (labels && labels[role]) || role; }
  function objectOf(labels, role) {
    var l = labelOf(labels, role);
    return l.length > 1 && /[A-Z]/.test(l[0]) && /[a-z]/.test(l[1]) ? l[0].toLowerCase() + l.slice(1) : l;
  }
  function fail(kind, role, against, message) { return { kind: kind, role: role || '', against: against || '', message: message }; }

  // validate(base, colours, preview, roles, labels): every reason the theme
  // cannot be saved, in plain words; [] when it passes. Unrounded ratios.
  function validate(base, colours, preview, roles, labels) {
    var out = [];
    colours = colours || {};
    if (base !== 'dark' && base !== 'light') return [fail('format', '', '', 'Base must be Dark or Daylight.')];
    var known = {};
    roles.forEach(function (r) {
      known[r] = true;
      if (!Object.prototype.hasOwnProperty.call(colours, r)) out.push(fail('format', r, '', labelOf(labels, r) + ' is missing.'));
      else if (!HEX.test(colours[r])) out.push(fail('format', r, '', labelOf(labels, r) + ' must be an opaque colour written #rrggbb.'));
    });
    Object.keys(colours).filter(function (k) { return !known[k]; }).sort().forEach(function (k) {
      out.push(fail('format', k, '', JSON.stringify(k) + ' is not a theme colour.'));
    });
    if (out.length) return out;

    var pairs = [], index = {};
    roles.forEach(function (r) {
      var rule = ruleFor(r);
      if (rule.min <= 0) return;
      rule.against.forEach(function (a) {
        if (!Object.prototype.hasOwnProperty.call(colours, a)) return;
        var fg = rule.background ? a : r, bg = rule.background ? r : a, k = fg + '|' + bg;
        if (Object.prototype.hasOwnProperty.call(index, k)) {
          if (rule.min > pairs[index[k]].min) pairs[index[k]].min = rule.min;
          return;
        }
        index[k] = pairs.length;
        pairs.push({ fg: fg, bg: bg, min: rule.min });
      });
    });
    pairs.forEach(function (p) {
      var c = contrast(colours[p.fg], colours[p.bg]);
      if (c < p.min) out.push(fail('contrast', p.fg, p.bg, labelOf(labels, p.fg) + ' on ' + objectOf(labels, p.bg) + ' is ' + floorShow(c, p.min, 1) + ':1; needs ' + num(p.min) + ':1.'));
    });

    var yo = luminance(colours['bg-surface-overlay']);
    if (base === 'dark') {
      var limit = function (list, max) {
        list.forEach(function (r) {
          var y = luminance(colours[r]);
          if (y > max) out.push(fail('luminance', r, '', labelOf(labels, r) + ' is too bright for a dark theme: luminance ' + ceilShow(y, max) + '; at most ' + num(max) + '.'));
        });
      };
      limit(LIMIT_020, 0.020);
      limit(LIMIT_035, 0.035);
      LARGE.forEach(function (r) {
        var y = luminance(colours[r]);
        if (y > yo) out.push(fail('luminance', r, 'bg-surface-overlay', labelOf(labels, r) + ' is brighter than overlay (luminance ' + ceilShow(y, yo) + ' against ' + num(Math.floor(yo * 1e4) / 1e4) + '); a large area may not be brighter than overlay in a dark theme.'));
      });
    }

    for (var i = 0; i < TONES.length; i++) {
      for (var j = i + 1; j < TONES.length; j++) {
        var a = TONES[i], b = TONES[j];
        if (colours[a] === colours[b]) {
          out.push(fail('tone', a, b, labelOf(labels, a) + ' and ' + objectOf(labels, b) + ' are the same colour; tones must differ.'));
          continue;
        }
        var d = deltaE(colours[a], colours[b]);
        if (d < MIN_TONE) out.push(fail('tone', a, b, labelOf(labels, a) + ' and ' + objectOf(labels, b) + ' are too alike: OKLab ΔE ' + floorShow(d, MIN_TONE, 3) + '; needs ' + num(MIN_TONE) + '.'));
      }
    }

    var max = base === 'dark' ? PREVIEW_DARK_MAX : 1;
    if (!(typeof preview === 'number' && preview > 0 && preview <= max)) {
      out.push(fail('preview', 'preview', '', 'Live-preview intensity must be more than 0 and at most ' + num(max) + '.'));
    } else if (base === 'dark') {
      var yp = lin(preview);
      if (yp > yo) out.push(fail('preview', 'preview', 'bg-surface-overlay', 'Live-preview intensity ' + num(preview) + ' lights the colour preview brighter than overlay (luminance ' + ceilShow(yp, yo) + ' against ' + num(Math.floor(yo * 1e4) / 1e4) + ').'));
    }
    return out;
  }

  // --- applying -------------------------------------------------------------
  function read(key) {
    try { return window.localStorage.getItem(key); } catch (e) { return null; }
  }
  function write(key, value) {
    try {
      if (value === null) window.localStorage.removeItem(key); else window.localStorage.setItem(key, value);
      return true;
    } catch (e) { return false; }
  }
  function announce() {
    try { window.dispatchEvent(new CustomEvent('b5-theme-changed', { detail: { theme: applied } })); } catch (e) { /* no events yet (head) */ }
  }
  function clearProps() {
    props.forEach(function (p) { root.style.removeProperty(p); });
    props = [];
  }
  // usable: enough of a custom theme to put on screen — a known base and
  // only opaque #rrggbb values. (Contract checks are the server's list.)
  function usable(t) {
    if (!t || typeof t !== 'object' || !BUILT_IN[t.base] || !t.colors || typeof t.colors !== 'object') return false;
    var keys = Object.keys(t.colors);
    return keys.length > 0 && keys.every(function (k) { return /^[a-z0-9-]+$/.test(k) && HEX.test(t.colors[k]); });
  }
  function paint(base, colours, preview) {
    clearProps();
    root.setAttribute('data-theme', base);
    Object.keys(colours || {}).forEach(function (k) {
      var p = '--b5-color-' + k;
      root.style.setProperty(p, colours[k]);
      props.push(p);
    });
    if (typeof preview === 'number' && preview > 0) {
      root.style.setProperty('--b5-preview-max-brightness', String(preview));
      props.push('--b5-preview-max-brightness');
    }
  }
  function applyBuiltIn(name) {
    paint(name, null, null);
    applied = name;
    chosenCustom = null;
  }
  function applyCustom(t) {
    paint(t.base, t.colors, t.preview);
    applied = 'custom:' + t.id;
    chosenCustom = t;
  }

  // choose(name[, theme]): apply now and remember it in this browser. name
  // is "dark", "light" or "custom:<id>" with that theme object. Returns
  // false when the browser refused to store it — the theme still applies
  // to this page, but the next load starts on Dark.
  function choose(name, theme) {
    var ok;
    if (typeof name === 'string' && name.indexOf('custom:') === 0 && usable(theme) && 'custom:' + theme.id === name) {
      applyCustom(theme);
      ok = write(KEY, name) && write(CUSTOM_KEY, JSON.stringify(theme));
    } else {
      if (!BUILT_IN[name]) name = 'dark';
      applyBuiltIn(name);
      ok = write(KEY, name);
      write(CUSTOM_KEY, null);
    }
    announce();
    return ok;
  }
  function current() { return applied; }
  function currentCustom() { return chosenCustom; }

  // preview(draft): show a draft on this page only — not stored, not
  // chosen. endPreview() puts back what this browser has chosen.
  function preview(draft) { paint(draft.base, draft.colors, draft.preview); }
  function endPreview() {
    if (chosenCustom) applyCustom(chosenCustom); else applyBuiltIn(applied);
  }

  // sync(themes): the server's list (GET /api/themes). A custom theme this
  // browser uses that was deleted, or that the server now says needs
  // changes, is dropped for Dark; an edited one is re-applied. Returns what
  // happened: null, "deleted" or "invalid".
  function sync(themes) {
    if (!chosenCustom) return null;
    var id = chosenCustom.id, t = null;
    (themes || []).forEach(function (x) { if (x && x.id === id) t = x; });
    if (!t) { choose('dark'); return 'deleted'; }
    if (t.failures && t.failures.length) { choose('dark'); return 'invalid'; }
    var plain = { id: t.id, name: t.name, base: t.base, colors: t.colors, preview: t.preview };
    if (JSON.stringify(plain) !== JSON.stringify(chosenCustom)) choose('custom:' + id, plain);
    return null;
  }

  var stored = read(KEY);
  var cached = null;
  if (stored && stored.indexOf('custom:') === 0) {
    try { cached = JSON.parse(read(CUSTOM_KEY)); } catch (e) { cached = null; }
  }
  if (cached && usable(cached) && 'custom:' + cached.id === stored) applyCustom(cached);
  else applyBuiltIn(BUILT_IN[stored] ? stored : 'dark');

  return {
    choose: choose, current: current, currentCustom: currentCustom, preview: preview, endPreview: endPreview, sync: sync,
    validate: validate, contrast: contrast, deltaE: deltaE, luminance: luminance, KEY: KEY, CUSTOM_KEY: CUSTOM_KEY,
  };
})();
