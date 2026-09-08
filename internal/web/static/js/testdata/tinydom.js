// tinydom.js — minimal XML DOM shim used ONLY by this project's Node-run
// regression test for gdtfparse.js (internal/web/gdtfparse_footprint_test.go
// shells out to gdtf_footprint_test.js, which requires this file). Never
// loaded by the browser app — index.html doesn't reference it, and it ships
// no browser-facing behavior; it exists purely so gdtfparse.js's `new
// DOMParser().parseFromString(...)` call has something to run against under
// plain Node (no jsdom/xmldom package — keeps this test dependency-free too,
// consistent with the project's zero-external-dependency rule).
//
// Implements exactly the subset of the DOM Element/Document interface
// gdtfparse.js actually calls: tagName, localName, children (array-like,
// indexable, .length), getAttribute, and (document-level only)
// getElementsByTagName. Not a general-purpose XML parser — no namespaces,
// CDATA, or processing-instruction handling beyond stripping the XML
// declaration and comments, which is all this project's GDTF fixtures need.
'use strict';

function decodeEntities(s) {
  return s
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(parseInt(d, 10)))
    .replace(/&#x([0-9a-fA-F]+);/g, (_, h) => String.fromCodePoint(parseInt(h, 16)))
    .replace(/&amp;/g, '&');
}

class Element {
  constructor(tagName) {
    this.tagName = tagName;
    this.localName = tagName;
    this.attrs = new Map();
    this.children = [];
    this.textContent = '';
  }
  getAttribute(name) {
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }
}

function parseXmlToTinyDom(xmlString) {
  const s = xmlString.replace(/<\?xml[\s\S]*?\?>/, '').replace(/<!--[\s\S]*?-->/g, '');
  const tagRe = /<(\/?)([A-Za-z_][\w.:-]*)((?:\s+[\w.:-]+\s*=\s*(?:"[^"]*"|'[^']*'))*)\s*(\/?)>/g;
  const attrRe = /([\w.:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
  const root = new Element('#document');
  const stack = [root];
  let m;
  let cursor = 0;
  while ((m = tagRe.exec(s))) {
	// Keep direct text on its containing element. Earlier fixture-parser
	// tests used attributes only; MVR's <Position>UUID</Position> and
	// <Address>1</Address> need this small, literal text-node subset.
	const text = decodeEntities(s.slice(cursor, m.index)).trim();
	if (text) stack[stack.length - 1].textContent += text;
	cursor = tagRe.lastIndex;
    const closing = m[1] === '/';
    const tag = m[2];
    const attrsStr = m[3];
    const selfClose = m[4] === '/';
    if (closing) {
      stack.pop();
      continue;
    }
    const el = new Element(tag);
    let am;
    attrRe.lastIndex = 0;
    while ((am = attrRe.exec(attrsStr))) {
      const val = am[2] !== undefined ? am[2] : am[3];
      el.attrs.set(am[1], decodeEntities(val));
    }
    stack[stack.length - 1].children.push(el);
    if (!selfClose) stack.push(el);
  }
	const tail = decodeEntities(s.slice(cursor)).trim();
	if (tail) stack[stack.length - 1].textContent += tail;
  return root;
}

class TinyDocument {
  constructor(root) {
    this.root = root;
  }
  getElementsByTagName(tag) {
    const out = [];
    const stack = [this.root];
    while (stack.length) {
      const cur = stack.pop();
      for (const c of cur.children) {
        if (c.tagName === tag) out.push(c);
        stack.push(c);
      }
    }
    return out;
  }
}

class DOMParser {
  parseFromString(xmlString) {
    return new TinyDocument(parseXmlToTinyDom(xmlString));
  }
}

module.exports = { DOMParser };
