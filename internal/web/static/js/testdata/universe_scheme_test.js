'use strict';
// Runs the LITERAL ui.js the browser loads, against the numbering rules the
// owner set out:
//
//   Nodes, Analyzer      raw Art-Net Port-Address, one flat number
//   Patch, Rig Check,
//   Function check,
//   Rig Walk, Send       the show's own universe, starting at 1
//   Devices              both
//
// with an "Art-Net starting universe" saying which Art-Net universe the
// show's universe 1 lives on.
//
// The defect this replaces was one global `universeBase` (0|1) applied to
// every screen at once, through a single `formatUniverse` whose name did not
// say WHICH numbering it produced. That is why the Nodes tab read one less
// than the EN4's faceplate and no call site looked wrong: every screen was
// using the one function, and the function could only be right for some of
// them.

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const jsDir = path.resolve(__dirname, '..');

let failures = 0;
function check(cond, msg, detail) {
  if (!cond) { failures++; console.error('FAIL: ' + msg + (detail ? '\n      ' + detail : '')); }
}

const ctx = { console, window: { dispatchEvent() {} }, CustomEvent: class { constructor() {} }, Number, String, Math, document: {} };
ctx.globalThis = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(path.join(jsDir, 'ui.js'), 'utf8'), ctx);
// `const UI = ...` at file scope is not a context property — reach it by
// evaluating its name (the harness gotcha this project hit once already).
const UI = vm.runInContext('UI', ctx);

// --- 1. the old ambiguous API is GONE --------------------------------------
// Not cosmetic. While `formatUniverse` still exists, a screen can call it and
// silently get whichever numbering the setting happened to imply — which is
// the bug. Removing the name forces every call site to say what it means.
for (const gone of ['formatUniverse', 'parseUniverse', 'universeInputAttrs', 'universeBaseLabel', 'setUniverseBase', 'getUniverseBase']) {
  check(UI[gone] === undefined,
    `UI.${gone} still exists — a screen can call it without stating which numbering it wants, ` +
    'which is precisely how Nodes ended up disagreeing with the gateway faceplate');
}

// --- 2. start 0: the show's universe 1 is Art-Net universe 0 ---------------
UI.setArtnetStart(0);
check(UI.getArtnetStart() === 0, 'start did not take');
check(UI.formatUser(0) === '1', 'Art-Net 0 is show universe 1 at start 0', 'got ' + UI.formatUser(0));
check(UI.formatUser(16) === '17', 'Art-Net 16 is show universe 17 at start 0', 'got ' + UI.formatUser(16));
check(UI.parseUser('1') === 0, 'typing show universe 1 means Art-Net 0', 'got ' + UI.parseUser('1'));
check(UI.parseUser('17') === 16, 'typing show universe 17 means Art-Net 16', 'got ' + UI.parseUser('17'));

// The protocol pair is identity, and must NOT move with the start.
check(UI.formatArtnet(0) === '0', 'Art-Net 0 shows as 0');
check(UI.formatArtnet(17) === '17', 'Art-Net 17 shows as 17 — typed and read as itself');
check(UI.parseArtnet('17') === 17, 'typing 17 on a protocol screen means Port-Address 17');

// --- 3. start 1: the other common rig --------------------------------------
UI.setArtnetStart(1);
check(UI.formatUser(1) === '1', 'Art-Net 1 is show universe 1 at start 1', 'got ' + UI.formatUser(1));
check(UI.formatUser(17) === '17', 'Art-Net 17 is show universe 17 at start 1', 'got ' + UI.formatUser(17));
check(UI.parseUser('1') === 1, 'typing show universe 1 means Art-Net 1 at start 1');
// Still identity on the protocol side. This is the assertion that would have
// caught the original Nodes-vs-faceplate disagreement.
check(UI.formatArtnet(17) === '17',
  'Art-Net 17 still shows as 17 at start 1 — the show offset must never reach a protocol screen');

// --- 4. a block-offset show, which a 0/1 toggle could not express ----------
UI.setArtnetStart(100);
check(UI.formatUser(100) === '1', 'Art-Net 100 is show universe 1 at start 100', 'got ' + UI.formatUser(100));
check(UI.formatUser(139) === '40', 'Art-Net 139 is show universe 40', 'got ' + UI.formatUser(139));
check(UI.parseUser('40') === 139, 'typing show universe 40 means Art-Net 139', 'got ' + UI.parseUser('40'));

// --- 5. below the start there is NO show universe --------------------------
// The honesty rule. With a start of 100, a node port on Art-Net 5 is real and
// outside this show's block. "Universe -94" reads as an arithmetic bug and
// invites someone to "fix" it; an em dash plus the Art-Net number is the
// truth. This is the same class as every other defect on this project where
// a plausible number stood in for "no answer".
check(UI.artnetToUser(5) === null,
  'Art-Net 5 has no show universe at start 100 — artnetToUser must return null, not -94',
  'got ' + UI.artnetToUser(5));
check(UI.formatUser(5) === UI.OUTSIDE_SHOW,
  'a universe below the start renders as the outside-range marker, never a negative number',
  'got ' + JSON.stringify(UI.formatUser(5)));
check(!/-/.test(UI.formatUser(5)), 'the outside-range marker must not contain a minus sign');
check(UI.formatUser(99) === UI.OUTSIDE_SHOW, 'the universe immediately below the start is outside too');
check(UI.formatUser(100) === '1', 'and the start itself is show universe 1');

// The Art-Net number is never lost, even outside the range: Devices must
// still be able to name the port.
check(/Art-Net 5/.test(UI.formatBoth(5)),
  'formatBoth drops the Art-Net universe for an outside-range port, leaving nothing to identify it by',
  'got ' + UI.formatBoth(5));
check(/outside/i.test(UI.formatBoth(5)),
  'formatBoth does not say an outside-range port is outside the show range',
  'got ' + UI.formatBoth(5));

// --- 6. Devices shows both, unambiguously ----------------------------------
UI.setArtnetStart(0);
const both = UI.formatBoth(16);
check(/17/.test(both) && /16/.test(both),
  'Devices must show the show universe AND the Art-Net universe — it is where a ' +
  'physical port and a patched fixture meet',
  'got ' + both);
check(/Art-Net/.test(both),
  'the Art-Net number must be LABELLED — an unlabelled pair of numbers is two guesses',
  'got ' + both);

// --- 7. input bounds match the numbering -----------------------------------
UI.setArtnetStart(0);
check(UI.userInputAttrs().min === 1, 'there is no show universe 0; the user field starts at 1');
check(UI.artnetInputAttrs().min === 0 && UI.artnetInputAttrs().max === 32767,
  'the Art-Net field spans the whole 15-bit Port-Address range');
UI.setArtnetStart(100);
check(UI.userInputAttrs().max === 32767 - 100 + 1,
  'the user field cannot reach past the top of the wire range once the show is offset',
  'got ' + UI.userInputAttrs().max);

// --- 8. round trips, both directions, across starts ------------------------
for (const start of [0, 1, 100, 32000]) {
  UI.setArtnetStart(start);
  for (const raw of [start, start + 1, start + 41, 32767]) {
    const u = UI.artnetToUser(raw);
    if (u === null) continue;
    check(UI.userToArtnet(u) === raw,
      `round trip broke at start ${start}: Art-Net ${raw} -> show ${u} -> Art-Net ${UI.userToArtnet(u)}`);
  }
}

// --- 9. a nonsense start is refused, not stored ----------------------------
UI.setArtnetStart(0);
UI.setArtnetStart(-5);
check(UI.getArtnetStart() === 0, 'a negative start was accepted; it must fall back rather than renumber a rig', 'got ' + UI.getArtnetStart());
UI.setArtnetStart(99999);
check(UI.getArtnetStart() === 0, 'a start past the Port-Address range was accepted', 'got ' + UI.getArtnetStart());

// --- 10. the label says which numbering, in words --------------------------
UI.setArtnetStart(0);
check(/Art-Net/.test(UI.universeScheme('artnet')),
  'the protocol scheme label does not name Art-Net', 'got ' + UI.universeScheme('artnet'));
check(/show/i.test(UI.universeScheme('user')),
  'the operator scheme label does not name the show numbering', 'got ' + UI.universeScheme('user'));
UI.setArtnetStart(7);
check(/7/.test(UI.universeScheme('user')),
  'the operator scheme label must state the actual correlation, so a tech can check it ' +
  'against their own patch rather than trusting the app',
  'got ' + UI.universeScheme('user'));

if (failures) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log('universe_scheme_test.js: all checks passed');
