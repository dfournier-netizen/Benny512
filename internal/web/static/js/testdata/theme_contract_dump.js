// theme_contract_dump.js — runs the LITERAL static/js/theme.js contract maths
// over fixtures and prints the results as JSON, for
// internal/web/console_c9_test.go to compare with the Go copy (theme.go)
// and with docs/design/console-lite/contrast.json. Zero dependencies.
//   node theme_contract_dump.js <jsDir> <input.json>
'use strict';
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const [jsDir, inputPath] = process.argv.slice(2);
const input = JSON.parse(fs.readFileSync(inputPath, 'utf8'));
const root = { getAttribute: () => 'dark', setAttribute() {}, style: { setProperty() {}, removeProperty() {} } };
const ctx = vm.createContext({ document: { documentElement: root }, window: { localStorage: { getItem: () => null, setItem() {} } }, console, JSON, Math });
vm.runInContext(fs.readFileSync(path.join(jsDir, 'theme.js'), 'utf8'), ctx, { filename: 'theme.js' });
const T = vm.runInContext('B5Theme', ctx);
if (!T.contrast || !T.deltaE || !T.validate) { console.error('theme.js has no contract maths (contrast/deltaE/validate)'); process.exit(2); }
const out = {
  ratios: input.pairs.map(([a, b]) => T.contrast(a, b)),
  deltas: input.tones.map(([a, b]) => T.deltaE(a, b)),
  failures: input.drafts.map(d => T.validate(d.base, d.colors, d.preview, input.roles, input.labels)),
};
process.stdout.write(JSON.stringify(out));
