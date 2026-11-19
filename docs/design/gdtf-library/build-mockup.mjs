// Builds the self-contained G3a mockup from src/ plus the PRODUCTION CSS and
// icon sprite, so the mockup can never drift from the tokens it claims to use.
// Design asset only: zero dependencies, reads production files, writes one file.
//
//   node docs/design/gdtf-library/build-mockup.mjs mockup_2026-11-17_2309.html
//
// The output name is the dated file to (re)write; take the timestamp from
// `TZ=America/New_York date +%Y-%m-%d_%H%M` when making a new version.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = path.dirname(fileURLToPath(import.meta.url));
const stat = path.join(dir, '../../../internal/web/static');
const out = process.argv[2];
if (!out || !/^mockup_\d{4}-\d{2}-\d{2}_\d{4}\.html$/.test(out)) {
  console.error('usage: node build-mockup.mjs mockup_YYYY-MM-DD_HHMM.html');
  process.exit(2);
}
const read = p => fs.readFileSync(p, 'utf8');
// Every stylesheet index.html links, in the order it links them.
const index = read(path.join(stat, 'index.html'));
const sheets = [...index.matchAll(/<link rel="stylesheet" href="\/css\/([\w.-]+)">/g)].map(m => m[1]);
if (sheets.length < 10) throw new Error('expected the production stylesheet list, got ' + sheets.join(','));
const css = sheets.map(f => `/* ===== production: css/${f} ===== */\n` + read(path.join(stat, 'css', f))).join('\n');
const sprite = read(path.join(stat, 'icons/benny512-icons.svg')).replace(/^<\?xml[^>]*>\s*/, '');
let html = read(path.join(dir, 'src/shell.html'));
const put = (marker, text) => {
  if (!html.includes(marker)) throw new Error('missing ' + marker);
  html = html.replace(marker, () => text);
};
put('/*@PRODUCTION_CSS@*/', css);
put('/*@MOCKUP_CSS@*/', read(path.join(dir, 'src/mockup.css')));
put('<!--@SPRITE@-->', sprite);
put('/*@SCENES_JS@*/', read(path.join(dir, 'src/scenes.js')));
// The mockup's own markup must use the inlined sprite (#b5-icon-*), never the
// served file (production CSS comments mention it, so only src/ is checked).
for (const f of ['src/shell.html', 'src/scenes.js']) {
  if (/benny512-icons\.svg#/.test(read(path.join(dir, f)))) throw new Error(f + ' refers to the served sprite file');
}
fs.writeFileSync(path.join(dir, out), html);
console.log(`${out}: ${sheets.length} production stylesheets (${sheets.join(', ')}), sprite ${sprite.length} B, ${html.length} B total`);
