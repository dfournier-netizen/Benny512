// Design assets only. Zero dependencies; never imports production controllers.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const dir=path.dirname(fileURLToPath(import.meta.url));
const write=(name,text)=>fs.writeFileSync(path.join(dir,name),text);
const production=fs.readFileSync(path.join(dir,'../../../internal/web/static/css/console-tokens.css'),'utf8');
const icons={
 check:'<path d="m5 12 4 4L19 6"/>',
 close:'<path d="m6 6 12 12M18 6 6 18"/>',
 window:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M6 6.5h.01M9 6.5h.01"/>',
 'window-collapse':'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 16l4-4 4 4"/>',
 'window-expand':'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 12l4 4 4-4"/>',
 'window-reset':'<path d="M6 8a8 8 0 1 1-1 9M6 3v5H1"/><rect x="8" y="9" width="10" height="8" rx="1"/>',
 'dock-right':'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M15 4v16M6 12h6m-3-3 3 3-3 3"/>',
 float:'<path d="M13 5H3v15h15v-9"/><rect x="10" y="3" width="11" height="10" rx="1"/>',
 'resize-grip':'<path d="m6 20 14-14M12 20l8-8M18 20l2-2"/>',
 'zoom-fit':'<path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5"/><rect x="7" y="7" width="10" height="10" rx="1"/>',
 'zoom-in':'<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6M7 10h6m-3-3v6"/>',
 'zoom-out':'<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6M7 10h6"/>',
 'spacing-plan':'<path d="M3 3v18h18M7 8h2v2H7zm9-3h2v2h-2zM13 15h2v2h-2z"/>',
 'spacing-compact':'<rect x="3" y="4" width="5" height="6" rx="1"/><rect x="10" y="4" width="5" height="6" rx="1"/><rect x="17" y="4" width="4" height="6" rx="1"/><path d="M3 14h18M3 19h18"/>',
 more:'<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
 view:'<path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
 ghosts:'<path stroke-dasharray="3 3" d="M5 20V9a7 7 0 0 1 14 0v11l-4-2-3 2-3-2Z"/><path d="M9 9h.01M15 9h.01"/>',
 'nav-tests':'<path d="M9 3h6M10 3v6L4 19q-1 2 2 2h12q3 0 2-2L14 9V3M8 14h8"/><path d="m10 17 1.5 1.5L15 15"/>',
 'edit-layout':'<path d="M3 3v18h18M5 7h6M7 5v6m8-2 4 4m-9 5 2-6 7-7 3 3-7 7Z"/>'
};
const symbols=Object.entries(icons).map(([name,body])=>`<symbol id="b5-icon-${name}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">${body}</symbol>`).join('\n');
write('console-workspace-icons.svg',`<svg xmlns="http://www.w3.org/2000/svg"><defs>\n${symbols}\n</defs></svg>\n`);
// Aliases bind to C9-validated semantic roles; palette ramps stay private.
const roles={
 'window-bg':'bg-surface','window-border':'border-control','dock-border':'border-strong',
 'drop-bg':'accent-fill','drop-border':'accent-border',
 'intensity-track':'bg-inset','intensity-fill':'accent-fill','intensity-text':'text-primary','intensity-edge':'accent-border',
 'tag-bg':'bg-surface','tag-set':'text-accent','tag-test':'text-info','tag-highlight':'text-warning'
};
let css='/* Proposed workspace roles only; load after production console-tokens.css. */\n';
for(const selector of [':root, [data-theme="dark"]','[data-theme="light"]'])css+=selector+' {\n'+Object.entries(roles).map(([name,role])=>`  --b5-workspace-${name}: var(--b5-color-${role});`).join('\n')+'\n  --b5-workspace-shadow: var(--b5-shadow-lg); /* decorative; boundary carries contrast */\n}\n';
write('tokens-additions.css',css);
const blocks=[...production.matchAll(/(?:^|\n)(:root, \[data-theme="dark"\]|\[data-theme="light"\])\s*\{([^}]+)\}/g)];
const palettes={dark:{},light:{}};
for(const [,selector,body] of blocks)for(const [,name,value] of body.matchAll(/--b5-color-([\w-]+):\s*(#[\da-f]{6})/gi))palettes[selector.includes(':root')?'dark':'light'][name]=value;
function luminance(hex){return hex.slice(1).match(/../g).map(v=>parseInt(v,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);}
const ratio=(a,b)=>(Math.max(luminance(a),luminance(b))+.05)/(Math.min(luminance(a),luminance(b))+.05);
const contracts={
 'window-border':[['grid-bg','bg-canvas','bg-surface','bg-surface-raised','bg-selected'],3],
 'dock-border':[['grid-bg','bg-surface'],3],
 'drop-border':[['grid-bg','accent-fill'],3],
 'intensity-edge':[['bg-inset','accent-fill'],3],
 'intensity-text':[['bg-inset','accent-fill'],7],
 'tag-set':[['bg-surface'],4.5], 'tag-test':[['bg-surface'],4.5], 'tag-highlight':[['bg-surface'],4.5]
};
const pairs=[];
for(const [theme,palette] of Object.entries(palettes)){
 for(const [role,[against,min]] of Object.entries(contracts))for(const bg of against){const value=palette[roles[role]],back=palette[bg];if(!value||!back)throw Error('Unknown role '+role+' / '+bg);const measured=ratio(value,back);pairs.push({theme,role,value,against:bg,background:back,minimum:min,ratio:measured,pass:measured>=min});}
 for(const backgroundRole of ['window-bg','drop-bg','intensity-track','intensity-fill','tag-bg'])for(const foreground of ['text-primary','text-secondary','text-muted']){const background=palette[roles[backgroundRole]],value=palette[foreground],measured=ratio(value,background);pairs.push({theme,role:backgroundRole,value:background,against:foreground,background:value,minimum:4.5,ratio:measured,pass:measured>=4.5});}
}
write('contrast.json',JSON.stringify({method:'WCAG relative sRGB luminance; unrounded thresholds; all permitted new-role pairs. Shadows and grid lines decorative; no boundary is exempt.',roles,pairs},null,2)+'\n');
write('contrast.md','# Workspace contrast\n\nRebuild: `node docs/design/console-workspace/build-package.mjs`. Values resolve from production C9 roles. All permitted pairs, not just worst cases, are recorded below and in contrast.json. Essential boundaries ≥3:1, normal text ≥4.5:1, intensity words ≥7:1 on both track and fill. Shadows are decorative; visible opaque borders carry separation. Grid lines are decorative. Custom themes must validate these same aliases/pairs.\n\n| Theme | Workspace role | Against | Ratio | Minimum | Result |\n|---|---|---|---:|---:|---|\n'+pairs.map(p=>`| ${p.theme} | ${p.role} | ${p.against} | ${p.ratio.toFixed(2)} | ${p.minimum} | ${p.pass?'PASS':'FAIL'} |`).join('\n')+'\n');
write('assets.html',`<!doctype html><html lang="en" data-theme="dark"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Workspace icon proof</title><link rel="stylesheet" href="../../../internal/web/static/css/benny512-tokens.css"><link rel="stylesheet" href="../../../internal/web/static/css/console-tokens.css"><link rel="stylesheet" href="tokens-additions.css"><style>body{margin:24px;background:var(--b5-color-bg-canvas);color:var(--b5-color-text-primary);font:16px system-ui}a{color:var(--b5-color-text-accent)}.toolbar{display:flex;gap:16px;align-items:center;flex-wrap:wrap}button,select{min-height:44px;min-width:44px;background:var(--b5-color-bg-surface);color:inherit;border:1px solid var(--b5-color-border-control)}main{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px;margin-top:24px}article{padding:16px;border:1px solid var(--b5-workspace-window-border);background:var(--b5-workspace-window-bg)}.sizes{display:flex;gap:20px;align-items:end}figure{margin:0}figcaption{font-size:12px;margin-top:8px}.grey{filter:grayscale(1)}svg{fill:none;stroke:currentColor}.toggle[aria-pressed=true]{border:2px solid var(--b5-color-accent-border);font-weight:700;background:var(--b5-color-bg-pressed)}.toggle svg{display:none}.toggle[aria-pressed=true] svg{display:inline;width:18px;height:18px}</style></head><body><a href="index.html">Workspace review</a><h1>Workspace symbols</h1><p>${Object.keys(icons).length} new 24-grid symbols · 1.75 stroke · no metadata. Grid zoom is distinct from beam Zoom. Nav Tests has its own flask/check glyph.</p><div class="toolbar"><label>Theme <select id="theme"><option value="dark">Dark</option><option value="light">Daylight</option></select></label><label><input type="checkbox" id="grey">Greyscale</label><button class="toggle" aria-pressed="false"><svg aria-hidden="true"><use href="console-workspace-icons.svg#b5-icon-check"/></svg>Lasso</button><span>ON = check + thicker border + bold + pressed fill</span></div><main>${Object.keys(icons).map(name=>`<article><h2 style="font-size:16px">${name}</h2><div class="sizes">${[14,18,24,32].map(size=>`<figure><svg width="${size}" height="${size}" aria-label="${name}"><use href="console-workspace-icons.svg#b5-icon-${name}"/></svg><figcaption>${size} px</figcaption></figure>`).join('')}</div></article>`).join('')}</main><script src="assets.js"></script></body></html>`);
console.log(`${Object.keys(icons).length} symbols; ${pairs.length} contrast pairs; ${pairs.filter(p=>!p.pass).length} failures`);
if(pairs.some(p=>!p.pass))process.exitCode=1;
