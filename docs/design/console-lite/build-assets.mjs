// Rebuild the dependency-free design assets: node docs/design/console-lite/build-assets.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const dir = path.dirname(fileURLToPath(import.meta.url));
const write = (name, value) => fs.writeFileSync(path.join(dir, name), value + '\n');
const p = d => `<path d="${d}"/>`;
const circle = (x=12,y=12,r=8) => `<circle cx="${x}" cy="${y}" r="${r}"/>`;
const rect = (x,y,w,h,extra='') => `<rect x="${x}" y="${y}" width="${w}" height="${h}" ${extra}/>`;
const icons = {
 'nav-console': rect(3,4,18,16,'rx="2"')+p('M7 8v8M12 8v8M17 8v8M5 11h4M10 14h4M15 10h4'),
 arm:p('M8 3H4v18h4M16 3h4v18h-4M10 8l6 4-6 4Z'),
 disarm:rect(4,4,16,16)+p('M7 17 17 7'),
 'armed-lock':rect(5,10,14,11,'rx="1"')+p('M8 10V7a4 4 0 0 1 8 0v3M12 14v3'),
 'lease-lost':p('m9 5 2-2a5 5 0 0 1 7 7l-2 2M8 12l-2 2a5 5 0 0 0 7 7l2-2M3 3l18 18'),
 'link-ok':p('m9 7 2-2a5 5 0 0 1 7 7l-2 2M8 10l-2 2a5 5 0 0 0 7 7l2-2M8 16l8-8'),
 simulated:rect(3,3,18,18,'rx="2" stroke-dasharray="3 3"')+p('m9 7 8 5-8 5Z'),
 blackout:circle()+p('M6 18 18 6M6 6l12 12'),
 highlight:circle(12,12,4)+p('M12 3v2M12 19v2M3 12h2M19 12h2M5 5l2 2M17 17l2 2M5 19l2-2M17 7l2-2'),
 lowlight:p('M16 5a8 8 0 1 0 3 12 7 7 0 0 1-3-12ZM4 21h16'),
 locate:circle(12,12,5)+p('M12 3v5M12 16v5M3 12h5M16 12h5'),
 'fan-linear':p('M4 18V15M9 18v-6M14 18V9M19 18V6M3 3h17l-3 3M20 3l-3 3'),
 'fan-reverse':p('M5 18V6M10 18V9M15 18v-6M20 18v-3M21 3H4l3 3'),
 'fan-center-out':p('M4 18V7M8 18v-5M12 18v-2M16 18v-5M20 18V7M10 5H3l3-2M3 5l3 2M14 5h7l-3-2M21 5l-3 2'),
 'fan-edges-in':p('M4 18v-2M8 18v-5M12 18V8M16 18v-5M20 18v-2M3 5h7L7 3M10 5 7 7M21 5h-7l3-2M14 5l3 2'),
 clear:p('m4 14 9-10 7 6-9 10H8l-4-4ZM8 10l7 6M12 20h9'),
 group:rect(3,4,7,6,'rx="1"')+rect(14,4,7,6,'rx="1"')+rect(8.5,14,7,6,'rx="1"')+p('M6 10v2h12v-2M12 12v2'),
 'store-group':rect(3,3,6,6)+rect(13,3,6,6)+rect(3,13,6,6)+p('M16 12v9M12 16h9'),
 'store-preset':p('M4 3h12l4 4v14H4ZM8 3v6h8V3M8 21v-7h8v7M12 15v5M10 17h4'),
 'attr-dimmer':circle()+p('M12 4v16M12 6l5 5M12 11l6 6M12 16l3 3'),
 'attr-position':p('M12 3v18M3 12h18M9 6l3-3 3 3M9 18l3 3 3-3M6 9l-3 3 3 3M18 9l3 3-3 3'),
 'attr-colour':circle(9,8,4)+circle(15,8,4)+circle(12,15,4),
 'attr-beam':p('M8 3h8v5H8ZM9 8 4 21M15 8l5 13M12 11v10M4 21h16'),
 'attr-focus':p('M8 3H3v5M16 3h5v5M3 16v5h5M21 16v5h-5')+circle(12,12,4),
 'attr-shaper':circle()+p('M5 8h12M16 5v12M19 16H7M8 19V7'),
 'attr-control':p('M5 3v18M12 3v18M19 3v18M3 8h4M10 16h4M17 10h4'),
 'gobo-placeholder':circle()+circle(12,12,2)+p('M12 4v3M12 17v3M4 12h3M17 12h3'),
 'colour-slot-placeholder':circle()+p('M10 9a2 2 0 1 1 3 2c-1 .5-1 1-1 2M12 16h.01'),
 'slot-open':circle(),
 'shutter-open':p('M8 4H3v16h5M16 4h5v16h-5M7 12h10M9 10l-2 2 2 2M15 10l2 2-2 2'),
 'shutter-closed':rect(3,4,18,16)+p('M12 4v16M7 9l10 6M17 9 7 15'),
 strobe:p('M13 3 5 14h6l-1 7 9-12h-6ZM3 4l2 2M19 18l2 2'),
 prism:p('m12 3 9 17H3ZM12 3v17M3 20l9-7 9 7'),
 frost:circle()+p('M8 8h.01M16 8h.01M12 12h.01M8 16h.01M16 16h.01'),
 iris:circle()+p('m8 5 8 3 3 8-8 3-6-8ZM8 5l3 14M16 8 5 11M19 16 8 5'),
 zoom:circle(10,10,6)+p('M15 15l6 6M7 10h6M10 7v6'),
 focus:p('M3 5l9 7-9 7M21 5l-9 7 9 7M12 3v3M12 18v3'),
 shaper:circle()+p('M5 8h14M16 5v14M5 16h11'),
 'gobo-rotate-cw':p('M19 10a7 7 0 1 0 0 5M19 4v6h-6')+circle(12,12,2),
 'gobo-rotate-ccw':p('M5 10a7 7 0 1 1 0 5M5 4v6h6')+circle(12,12,2),
 'gobo-shake':circle(12,12,4)+p('M3 6v12M6 8l-3-2M6 16l-3 2M21 6v12M18 8l3-2M18 16l3 2'),
 lamp:p('M9 17h6M9 20h6M8 13a6 6 0 1 1 8 0l-1 4H9ZM12 9v5'),
 reset:p('M8 3h8v5l-3 4 3 4v5H8v-5l3-4-3-4ZM5 3h14M5 21h14'),
 layers:p('m12 3 9 5-9 5-9-5ZM3 12l9 5 9-5M3 16l9 5 9-5'),
 'layer-up':p('m3 16 7 4 7-4M3 12l7 4 7-4M17 12V3M14 6l3-3 3 3'),
 'layer-down':p('m3 5 7 4 7-4M3 9l7 4 7-4M17 12v9M14 18l3 3 3-3'),
 'grid-snap':p('M3 8h18M3 16h18M8 3v18M16 3v18')+rect(9,9,6,6),
 'select-parent':rect(3,5,18,14,'rx="1" stroke-width="2.5"')+p('M9 8v8M15 8v8'),
 'select-cell':rect(3,5,18,14,'rx="1" stroke-dasharray="3 2"')+rect(9,7,6,10,'stroke-width="2.5"'),
 'select-all':rect(3,3,18,18,'stroke-dasharray="3 2"')+p('M7 12l3 3 7-7'),
 'select-none':rect(3,3,18,18,'stroke-dasharray="3 2"')+p('M7 7l10 10M17 7 7 17'),
 'select-invert':rect(3,3,18,18)+p('M12 3v18M12 5l7 7M12 11l7 7M12 17l3 3'),
 lasso:p('M5 16C1 10 5 3 12 4s10 6 7 10-12 5-14 2Zm0 0c-4 8 8 4 3-1'),
 'seq-back':p('M5 4v16M19 4 8 12l11 8Z'),
 'seq-next':p('M19 4v16M5 4l11 8-11 8Z'),
 'seq-auto':p('M4 6h14l-3-3M20 18H6l3 3M9 8l7 4-7 4Z'),
 test:p('M9 3h6M10 3v6L4 19a1 1 0 0 0 1 2h14a1 1 0 0 0 1-2L14 9V3M7 15h10'),
 fine:p('M3 6h18M6 3v6M10 4v4M14 3v6M18 4v4M3 17h18M9 13l-4 4 4 4M15 13l4 4-4 4'),
 'midi-connected':circle()+p('M7 13h.01M9 8h.01M15 8h.01M17 13h.01M12 6h.01M9 20v-4h6v4'),
 'midi-disconnected':circle()+p('M7 13h.01M9 8h.01M15 8h.01M17 13h.01M12 6h.01M3 3l18 18'),
 'proto-artnet':rect(3,4,18,10,'rx="1"')+p('M7 8h.01M11 8h.01M12 14v5M4 19h16'),
 'proto-sacn':p('M4 5h7v14H4ZM11 12h10M16 7l5 5-5 5'),
 theme:circle()+p('M12 4v16M12 6l5 5M12 11l6 6'),
 'rdm-only':rect(3,5,18,14,'stroke-dasharray="3 2"')+p('M7 10h10l-2-2M17 14H7l2 2'),
 'drag-handle':p('M8 5h.01M16 5h.01M8 12h.01M16 12h.01M8 19h.01M16 19h.01')
};
for (const family of ['dimmer','position','colour','beam','focus','shaper','control']) {
 icons['preset-'+family] = `<g transform="translate(1 0) scale(.75)">${icons['attr-'+family].replace(/<(path|circle|rect) /g,'<$1 vector-effect="non-scaling-stroke" ')}</g>`+p('M16 15h5v6l-2.5-2-2.5 2Z');
}
for(const name of Object.keys(icons)) icons[name]=icons[name].replaceAll(' stroke-width="2.5"','');
const glyphs = {
 'fx-moving-head':p('M12 17v14q0 7 12 7t12-7V17M24 38v5M16 43h16')+rect(16,11,16,22,'rx="6"')+`<ellipse data-beam="" cx="24" cy="17" rx="6" ry="3"/>`,
 'fx-wash':p('M10 18v15q14 12 28 0V18M24 39v4M17 43h14')+circle(24,24,13)+`<circle data-beam="" cx="24" cy="24" r="9"/>`,
 'fx-profile':rect(16,10,16,27,'rx="2"')+p('M13 22h22M13 26h22M20 37v6h8v-6M14 10h20')+`<path data-beam="" d="M19 14h10v6H19Z"/>`,
 'fx-pixel-bar':rect(5,15,38,19,'rx="2"')+[0,1,2,3].map(i=>`<rect data-cell="${i+1}" data-beam="" x="${8+i*9}" y="19" width="6" height="11"/>`).join(''),
 'fx-strobe':rect(7,15,34,20,'rx="2"')+`<path data-beam="" d="M10 19h28v12H10Z"/>`+p('m25 18-6 8h6l-2 7 8-10h-6'),
 'fx-dimmer':circle(24,24,13)+`<circle data-beam="" cx="24" cy="24" r="9"/>`+p('M18 18l12 12M30 18 18 30M24 37v6'),
 'fx-unknown':rect(10,11,28,29,'rx="3" stroke-dasharray="4 3"')+p('M19 20a5 5 0 1 1 8 4c-3 2-3 3-3 5M24 34h.01'),
 'fx-group':rect(9,16,27,23,'rx="2"')+p('M14 12h26v22M18 8h25v22M15 25h15M15 31h9')
};
const style='fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"';
function sprite(entries, size) { return `<svg xmlns="http://www.w3.org/2000/svg">\n<defs>\n`+Object.entries(entries).map(([name,body])=>`<symbol id="b5-icon-${name}" viewBox="0 0 ${size} ${size}" ${style}>${body}</symbol>`).join('\n')+'\n</defs>\n</svg>'; }
write('console-icons.svg',sprite(icons,24));
write('fixture-glyphs.svg',sprite(glyphs,48));
write('asset-manifest.json',JSON.stringify({icons:Object.keys(icons),fixtures:Object.keys(glyphs)},null,2));
const sample = (name,size,file='console-icons.svg')=>`<div><svg width="${size}" height="${size}" aria-hidden="true"><use href="${file}#b5-icon-${name}"/></svg><small>${size} px</small></div>`;
const gallery=Object.keys(icons).map(name=>`<article class="asset-card"><h3>${name}</h3><div class="asset-sizes">${[14,18,24,32].map(size=>sample(name,size)).join('')}</div></article>`).join('');
const fixtureGallery=Object.keys(glyphs).map(name=>`<article class="asset-card"><h3>${name}</h3><div class="asset-sizes">${[32,48,64].map(size=>sample(name,size,'fixture-glyphs.svg')).join('')}</div></article>`).join('');
const overlays=[['Unselected','',''],['Selected parent','selected','1'],['Programmer · P','selected','2'],['Test running · T','',''],['Highlighted','highlighted',''],['Outside layer','ghost',''],['RDM only','ghost',''],['Not on wire','offline',''],['Unknown class','','']].map(([label,cls,order])=>`<div class="fixture ${cls}">${order?`<span class="badge">${order}</span>`:''}${label.includes(' · ')?`<span class="tabs">${label.split(' · ')[1]}</span>`:''}<svg aria-hidden="true"><use href="fixture-glyphs.svg#b5-icon-fx-${label==='Unknown class'?'unknown':'moving-head'}"/></svg><span class="name">${label}</span></div>`).join('');
write('assets.html',`<!doctype html><html lang="en" data-theme="dark"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Console-lite · asset proof sheet</title><link rel="stylesheet" href="../../../internal/web/static/css/benny512-tokens.css"><link rel="stylesheet" href="console-tokens.css"><link rel="stylesheet" href="mockups.css"></head><body class="review"><header class="review-head"><div><p class="eyebrow">Benny512 / asset proof</p><h1>Shapes before colour</h1><p>72 symbols · 24-unit grid · 1.75 stroke · currentColor</p></div><a href="index.html">Screen mockups ↗</a></header><div class="review-toolbar"><label>Theme <select id="asset-theme"><option>dark</option><option>light</option></select></label><label><input type="checkbox" id="asset-grey"> Greyscale</label></div><h2 style="margin:24px 0">Fixture state overlays</h2><p>HTML overlays belong to the component: top-left order, top-right P/T; labels remain outside the beam area.</p><div class="overlay-gallery">${overlays}<div class="pixel"><span>Selected cell · parent unselected</span><div class="cells"><button>1</button><button class="cell-on" aria-pressed="true"><span class="badge">4</span>2</button><button>3</button></div></div></div><h2 style="margin:24px 0">Fixture glyphs</h2><div class="asset-grid">${fixtureGallery}</div><h2 style="margin:24px 0">Interface icons</h2><div class="asset-grid">${gallery}</div><script src="assets.js"></script></body></html>`);

// Opaque role values make the contrast contract independent of compositing order.
const dark = { 'bg-canvas':'#100e0b','bg-surface':'#1c1815','bg-surface-raised':'#241f1a','bg-surface-overlay':'#2c261f','bg-inset':'#0b0a08','bg-hover':'#2c261f','bg-selected':'#23332f',
 'text-primary':'#f3f0e9','text-secondary':'#cbc5ba','text-muted':'#b2a99b','text-disabled':'#80786b','text-accent':'#7fd0c9','text-info':'#aac3ec','text-danger':'#ff9a93','text-success':'#a2dba8','text-warning':'#f0c163','text-on-accent':'#100e0b','text-on-danger':'#ffffff',
 'border-control':'#a29a8c','border-focus':'#b1dfdf','border-subtle':'#3a332b','border-default':'#3a332b','border-strong':'#a29a8c',
 'ok-border':'#a2dba8','ok-fill':'#18291d','warn-border':'#f0c163','warn-fill':'#2b230e','danger-border':'#ff9a93','danger-fill':'#341c19','accent-border':'#7fd0c9','accent-fill':'#152923',
 'armed-border':'#c2aff0','armed-fill':'#292335','armed-text':'#ded2ff','disarm-slab-bg':'#931b18','disarm-slab-text':'#ffffff','arm-pill-border':'#7fd0c9','arm-pill-text':'#7fd0c9','simulated-text':'#cbc5ba','lease-lost-border':'#f0c163',
 'fixture-idle':'#b2a99b','fixture-selected':'#7fd0c9','fixture-cell-selected':'#b1dfdf','fixture-touched':'#cbc5ba','fixture-test':'#aac3ec','fixture-highlight':'#f0c163','fixture-ghost':'#a29a8c','fixture-unprofiled':'#b2a99b','fixture-offline':'#f0c163',
 'value-set':'#7fd0c9','value-default':'#b2a99b','value-test':'#aac3ec','value-mixed-band':'#a29a8c',
 'grid-bg':'#15130f','grid-line':'#29251e','grid-line-major':'#40392f','grid-layer-label':'#cbc5ba','track':'#0b0a08','track-border':'#a29a8c','thumb':'#cbc5ba','thumb-border':'#f3f0e9','xypad-bg':'#15130f','xypad-crosshair':'#a29a8c' };
const light = { 'bg-canvas':'#eeece6','bg-surface':'#fefefe','bg-surface-raised':'#f7f5f0','bg-surface-overlay':'#ffffff','bg-inset':'#e7e4dc','bg-hover':'#e7e4dc','bg-selected':'#d6e8e3',
 'text-primary':'#1c1815','text-secondary':'#423c33','text-muted':'#5d5448','text-disabled':'#80786b','text-accent':'#075e58','text-info':'#2a4d85','text-danger':'#a10500','text-success':'#23522b','text-warning':'#684400','text-on-accent':'#ffffff','text-on-danger':'#ffffff',
 'border-control':'#6b6255','border-focus':'#075e58','border-subtle':'#c7c0b0','border-default':'#c7c0b0','border-strong':'#6b6255',
 'ok-border':'#23522b','ok-fill':'#dcebdd','warn-border':'#684400','warn-fill':'#f3e6c9','danger-border':'#a10500','danger-fill':'#f8e2dd','accent-border':'#075e58','accent-fill':'#d6e8e3',
 'armed-border':'#674283','armed-fill':'#ece3f4','armed-text':'#573474','disarm-slab-bg':'#931b18','disarm-slab-text':'#ffffff','arm-pill-border':'#075e58','arm-pill-text':'#075e58','simulated-text':'#423c33','lease-lost-border':'#684400',
 'fixture-idle':'#5d5448','fixture-selected':'#075e58','fixture-cell-selected':'#2a4d85','fixture-touched':'#423c33','fixture-test':'#2a4d85','fixture-highlight':'#684400','fixture-ghost':'#6b6255','fixture-unprofiled':'#5d5448','fixture-offline':'#684400',
 'value-set':'#075e58','value-default':'#5d5448','value-test':'#2a4d85','value-mixed-band':'#6b6255',
 'grid-bg':'#f7f5f0','grid-line':'#ded9ce','grid-line-major':'#c7c0b0','grid-layer-label':'#423c33','track':'#e7e4dc','track-border':'#6b6255','thumb':'#423c33','thumb-border':'#1c1815','xypad-bg':'#f7f5f0','xypad-crosshair':'#6b6255' };
const sizes={'icon-xl':'32px','control-lg':'60px','tile-min':'88px','xypad-min':'240px','xypad-max':'340px','slot-btn':'64px','fader-track':'8px','fixture-glyph':'48px','thumb-mouse':'28px','fader-length':'176px','fader-dimmer-length':'240px','pad-handle':'var(--b5-size-touch-min)','state-border':'3px','group-fader-width':'160px'};
const surfaces=['bg-canvas','bg-surface','bg-surface-raised','bg-surface-overlay','bg-inset','bg-hover','bg-selected','grid-bg','xypad-bg','ok-fill','warn-fill','danger-fill','accent-fill','armed-fill'];
function contract(key) {
 if (key==='disarm-slab-text'||key==='text-on-danger') return {against:['disarm-slab-bg'],min:4.5};
 if (key==='text-on-accent') return {against:['thumb'],min:4.5};
 if (key==='text-disabled') return {against:surfaces,min:0,note:'Disabled: reason word required; exempt'};
 if (key==='text-primary') return {against:surfaces,min:7};
 if (key.startsWith('text-')||['armed-text','arm-pill-text','simulated-text','grid-layer-label','value-set','value-default','value-test'].includes(key)) return {against:surfaces,min:4.5};
 if (['border-subtle','border-default','grid-line','grid-line-major'].includes(key)) return {against:['grid-bg','bg-surface'],min:0,note:'Decoration only; never an essential boundary'};
 if (key==='thumb-border') return {against:['track',...surfaces],min:3};
 if (key==='track-border') return {against:['track',...surfaces],min:3};
 if (key==='thumb') return {against:['track',...surfaces],min:3};
 if (/border|^fixture-|crosshair|mixed-band/.test(key)) return {against:surfaces,min:3};
 return {against:['text-primary','text-secondary','text-muted'],min:4.5,note:'Background: measured against text; tone borders checked separately'};
}
function rgb(hex){return hex.slice(1).match(/../g).map(x=>parseInt(x,16)/255);}
function lum(hex){return rgb(hex).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((s,v,i)=>s+v*[.2126,.7152,.0722][i],0);}
function ratio(a,b){const x=lum(a),y=lum(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);}
function oklab(hex){
 const [r,g,b]=rgb(hex).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);
 const l=Math.cbrt(.4122214708*r+.5363325363*g+.0514459929*b);
 const m=Math.cbrt(.2119034982*r+.6806995451*g+.1073969566*b);
 const s=Math.cbrt(.0883024619*r+.2817188376*g+.6299787005*b);
 return [.2104542553*l+.793617785*m-.0040720468*s,1.9779984951*l-2.428592205*m+.4505937099*s,.0259040371*l+.7827717662*m-.808675766*s];
}
const toneDistances=[];
for(const [theme,roles] of Object.entries({dark,light})) {
 const names=['armed-border','danger-border','warn-border','ok-border','accent-border'];
 for(let i=0;i<names.length;i++)for(let j=i+1;j<names.length;j++){
  const a=oklab(roles[names[i]]),b=oklab(roles[names[j]]);
  const delta=Math.hypot(...a.map((v,k)=>v-b[k]));
  toneDistances.push({theme,a:names[i],b:names[j],delta,pass:delta>=.04});
 }
}
write('theme-validation.json',JSON.stringify({toneDistances,darkLuminance:Object.fromEntries(['bg-canvas','bg-surface','bg-surface-raised','bg-surface-overlay','bg-selected','ok-fill','warn-fill','danger-fill','accent-fill','armed-fill'].map(k=>[k,lum(dark[k])]))},null,2));
if(toneDistances.some(p=>!p.pass))throw new Error('Tone separation below 0.04 OKLab');
// Slab is never a surface for generic text. Its paired foreground is mandatory.
const contracts=Object.fromEntries(Object.keys(dark).map(k=>[k,contract(k)]));
contracts['disarm-slab-bg']={against:['disarm-slab-text'],min:4.5};
const report=[];
for(const [theme,roles] of Object.entries({dark,light})) for(const [role,hex] of Object.entries(roles)) {
 const c=contracts[role]; const values=c.against.map(bg=>({against:bg,ratio:ratio(hex,roles[bg])}));
 const worst=values.reduce((a,b)=>a.ratio<b.ratio?a:b);
 report.push({theme,role,hex,minimum:c.min,worst:worst.against,ratio:worst.ratio,pass:worst.ratio>=c.min,pairs:values,note:c.note||''});
}
function block(selector,roles){return selector+' {\n'+Object.entries(roles).map(([k,v])=>`  --b5-color-${k}: ${v}; /* ${contracts[k].min||'exempt'}:1 ${contracts[k].note||'minimum; pairs in contrast.json'} */`).join('\n')+'\n  --b5-color-simulated-bg: repeating-linear-gradient(135deg, var(--b5-color-bg-surface) 0 6px, var(--b5-color-bg-surface-overlay) 6px 12px);\n  --b5-shadow-focus: 0 0 0 3px var(--b5-color-border-focus);\n}\n';}
write('console-tokens.css','/* Load after benny512-tokens.css. Proposed C9 roles; scoped preview use only.\n   Semantic overrides repair muted text, focus opacity and theme contrast. */\n'+block(':root, [data-theme="dark"]',dark)+block('[data-theme="light"]',light)+':root {\n'+Object.entries(sizes).map(([k,v])=>`  --b5-size-${k}: ${v};`).join('\n')+'\n  --b5-preview-max-brightness: 0.12; /* content only; not a state signal */\n}\n');
write('contrast.json',JSON.stringify({method:'WCAG relative sRGB luminance, unrounded pass/fail. Every permitted surface, opaque role values.',surfaces,report},null,2));
write('contrast.md','# Computed contrast\n\nRegenerate with `node docs/design/console-lite/build-assets.mjs`. All permitted pairs are listed in `contrast.json`; this table gives the worst pair per role. Ratios are rounded for display only. A zero minimum means decorative/disabled, not permission to use it for essential information.\n\n| Theme | Role (prefix --b5-color-) | Value | Worst background / foreground | Ratio | Required | Result |\n|---|---|---|---|---:|---:|---|\n'+report.map(r=>`| ${r.theme} | ${r.role} | ${r.hex} | ${r.worst} | ${r.ratio.toFixed(2)} | ${r.minimum||'exempt'} | ${r.pass?'PASS':'FAIL'} |`).join('\n')+'\n\nPatterns: both simulated stripe endpoints are covered by surface and overlay pairs. Focus shadow uses the opaque border-focus role. Sizes and preview multiplier have no intrinsic contrast ratio. Grid minor/major lines are decorative; labels and fixture positions remain readable without them. Ghost glyphs stay at full opacity to retain the boundary contrast; dashed outlines and an “Outside layer” label carry that state.\n');
console.log(`Built ${Object.keys(icons).length} icons, ${Object.keys(glyphs).length} fixture glyphs; ${report.filter(r=>!r.pass).length} failing contrast roles.`);
if(report.some(r=>!r.pass)) { console.table(report.filter(r=>!r.pass).map(({theme,role,ratio,worst})=>({theme,role,ratio,worst}))); process.exitCode=1; }

