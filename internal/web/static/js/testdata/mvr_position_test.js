'use strict';

// Literal browser-parser regression: fixture Position values are UUID
// references, so the label must come from Scene/Positions rather than the
// enclosing Layer. The second fixture pins the deliberate Layer fallback.
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { DOMParser } = require('./tinydom');

const src = fs.readFileSync(path.join(__dirname, '..', 'mvrparse.js'), 'utf8');
const MvrParse = vm.runInContext(src + '\nMvrParse;', vm.createContext({ DOMParser }), { filename: 'mvrparse.js' });
const xml = `<?xml version="1.0"?><GeneralSceneDescription><Scene>
  <Positions><Position name="LX 3" uuid="POS-LX3"/></Positions>
  <Layers><Layer name="Fallback layer"><ChildList>
    <Fixture name="Fixture A"><GDTFSpec>A.gdtf</GDTFSpec><Position>pos-lx3</Position><Addresses><Address>1</Address></Addresses></Fixture>
    <Fixture name="Fixture B"><GDTFSpec>B.gdtf</GDTFSpec><Addresses><Address>513</Address></Addresses></Fixture>
  </ChildList></Layer></Layers>
</Scene></GeneralSceneDescription>`;
const parsed = MvrParse.parseGeneralSceneDescriptionXml(xml);
if (parsed.fixtures.length !== 2) throw new Error('fixture count: ' + parsed.fixtures.length);
if (parsed.fixtures[0].position !== 'LX 3') throw new Error('UUID position label: ' + parsed.fixtures[0].position);
if (parsed.fixtures[1].position !== 'Fallback layer') throw new Error('Layer fallback: ' + parsed.fixtures[1].position);
console.log('mvr position UUID lookup: ok');
