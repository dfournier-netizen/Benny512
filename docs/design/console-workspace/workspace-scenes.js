// Included by prepare-mockups.mjs inside the preview's isolated closure.
// Fictional examples only: no fetch, sockets, MIDI or production state.
const coarse=matchMedia('(pointer:coarse)').matches||query.get('coarse')==='1';
document.documentElement.classList.toggle('coarse-preview',coarse);
const announce=text=>$('#workspaceStatus').textContent=text;
document.body.insertAdjacentHTML('beforeend','<p class="sr" id="workspaceStatus" role="status"></p>');
const newUse=(element,name)=>element?.querySelector('use')?.setAttribute('href','console-workspace-icons.svg#b5-icon-'+name);
newUse($('#fitBtn'),'zoom-fit');
$('#fitBtn').innerHTML=icon('zoom-fit')+'Fit';
$('#zoomIn').innerHTML=icon('zoom-in');$('#zoomOut').innerHTML=icon('zoom-out');
$$('[data-spacing]').forEach(b=>b.insertAdjacentHTML('afterbegin',icon('spacing-'+b.dataset.spacing)));
$$('[data-close]').forEach(b=>b.innerHTML=icon('close'));
$$('[data-resize]').forEach(b=>b.innerHTML=icon('resize-grip'));
$('#resetWins').insertAdjacentHTML('afterbegin',icon('window-reset'));
$$('svg').forEach(svg=>svg.setAttribute('aria-hidden','true'));
$$('.nav button:not([data-screen])').forEach(b=>{b.disabled=true;b.title='Design preview: this existing screen is unchanged';});
$('#layerPicker').addEventListener('change',ev=>{S.layer=ev.target.value;syncSeg('#layerSeg','layer',S.layer);renderAll();});
$$('#layerSeg button').forEach(b=>b.addEventListener('click',()=>$('#layerPicker').value=S.layer));
const originalSetSpacing=setSpacing;
$$('#m-view [data-view="live"]').forEach(b=>b.addEventListener('click',()=>document.documentElement.classList.toggle('hide-values',S.viewLive===false)));
describe=function(keys){
 const groups=new Map();keys.forEach(k=>{const f=keyFx(k);if(!groups.has(f.name))groups.set(f.name,[]);groups.get(f.name).push(k.includes('/')?f.fid+'.'+k.split('/')[1]:f.fid);});
 return [...groups].map(([name,ids])=>{const parents=ids.filter(x=>typeof x==='number').sort((a,b)=>a-b),parts=ids.filter(x=>typeof x==='string'),runs=[];for(let i=0;i<parents.length;){let j=i;while(j+1<parents.length&&parents[j+1]===parents[j]+1)j++;runs.push(j-i>1?parents[i]+'–'+parents[j]+' ('+(j-i+1)+')':parents.slice(i,j+1).join(', '));i=j+1;}return name+' '+[...runs,...parts].join(', ');}).join(' · ');
};
// Preserve Plan/Compact within this browser, shared by Console and Tests.
$$('[data-spacing]').forEach(b=>b.addEventListener('click',()=>store.set('b5workspace.spacing',S.spacing)));
S.spacing=store.get('b5workspace.spacing')||'plan';
if(scene==='compact'||scene==='cells')S.spacing='compact';
syncSeg('#spacingSeg','spacing',S.spacing);syncSeg('#spacingSeg2','spacing',S.spacing);
if(scene==='empty'){S.sel=[];S.vals={};}
if(scene==='unplaced'){S.layer='unplaced';syncSeg('#layerSeg','layer','unplaced');$('#layerPicker').value='unplaced';}
if(scene==='all-layers'){S.layer='all';syncSeg('#layerSeg','layer','all');$('#layerPicker').value='all';}
if(scene==='cells'){S.sel=['f401/2','f401/3'];S.vals['f401/3']={dim:.75,col:'#3d6bff'};}
if(scene==='stacked'){
 Object.assign(st('fw-order'),{open:true,docked:true,collapsed:true});
 Object.assign(st('fw-groups'),{open:true,docked:false,x:24,y:100,w:340,h:250});
}
if(scene==='resize'){
 Object.assign(st('fw-controls'),{open:true,docked:false,x:Math.max(16,innerWidth-580),y:16,w:520,h:400});
 $('#fw-controls').classList.add('resize-example');
}
if(scene==='drop'){
 Object.assign(st('fw-groups'),{open:true,docked:false,x:24,y:80,w:340,h:250});
 $('#wsConsole').classList.add('dock-hint');
}
if(scene==='dock320')dockW=320;
if(scene==='tests'){
 S.sel.forEach(k=>delete S.vals[k]); // Test example uses unclaimed targets; manual values still win.
 S.armed=true;
 $('#outState').classList.add('armed');$('#outWord').textContent='ARMED · LIVE';
 const b=$('[data-tests] .ttile');b.setAttribute('aria-pressed','true');b.querySelector('.st').textContent='ON';S.tests.add(b);
 show('tests');
}
if(scene.startsWith('controls-'))tab=scene.slice(9);
// Full selection tools on Tests; wrappers invoke the existing sample model.
const testsBar=$('#scr-tests .selbar');
testsBar.insertAdjacentHTML('beforeend','<div class="tests-tools"><button class="btn" data-tests-tool="lassoBtn" aria-pressed="false">Lasso</button><button class="btn" data-tests-tool="addBtn" aria-pressed="false">Add</button><button class="btn" data-menu="m-select" aria-haspopup="menu" aria-expanded="false">Select</button></div>');
$$('[data-tests-tool]').forEach(b=>b.onclick=()=>{const original=document.getElementById(b.dataset.testsTool);original.click();b.setAttribute('aria-pressed',original.getAttribute('aria-pressed'));b.innerHTML=(original.getAttribute('aria-pressed')==='true'?check:'')+(b.dataset.testsTool==='lassoBtn'?'Lasso':'Add');});
testsBar.querySelector('[data-menu]').onclick=ev=>{
 const b=ev.currentTarget,m=$('#m-select');closeMenus();const r=b.getBoundingClientRect();m.hidden=false;m.style.top=r.bottom+4+'px';m.style.left=Math.min(r.left,innerWidth-240)+'px';openMenu={m,b};b.setAttribute('aria-expanded','true');m.querySelector('button').focus();
};
// Show every test family separately without inventing production test IDs.
const combined=[...document.querySelectorAll('.tpanel h3')].find(h=>h.textContent.includes('COLOUR'));
combined.textContent='COLOUR';
const mixed=combined.nextElementSibling;
const tileList=[...mixed.querySelectorAll('.ttile')];
const beam=document.createElement('div');beam.className='ttiles';beam.append(tileList[1],tileList[2]);
const focus=document.createElement('div');focus.className='ttiles';focus.append(tileList[3]);
mixed.insertAdjacentHTML('afterend','<h3>BEAM</h3>');mixed.nextElementSibling.after(beam);
beam.insertAdjacentHTML('afterend','<h3>FOCUS</h3>');beam.nextElementSibling.after(focus);
focus.insertAdjacentHTML('afterend','<h3>SHAPER</h3><button class="ttile" type="button" id="shaperTest" aria-pressed="false"><span class="row">'+oldIcon('attr-shaper')+'<b>Blade sweep</b><span class="st">OFF</span></span><span class="muted">3 of 3 sample fixtures supported</span></button>');
$('#shaperTest').onclick=ev=>{const b=ev.currentTarget,on=b.getAttribute('aria-pressed')!=='true';b.setAttribute('aria-pressed',String(on));b.querySelector('.st').textContent=on?'ON':'OFF';on?S.tests.add(b):S.tests.delete(b);renderAll();};
const isolate=$('.tpanel input[type=checkbox]');
isolate.setAttribute('aria-label','Isolate: drive only the tested channel');
isolate.closest('.card').classList.add('isolate-card');
isolate.closest('.card').querySelector('p').textContent='Manual values win. Tested-cell masters stay open. Ends with tests, Disarm, either lease-loss policy or this browser leaving.';
const updateIsolate=()=>{isolate.disabled=!S.armed||!S.tests.size;if(isolate.disabled)isolate.checked=false;};
document.addEventListener('click',()=>queueMicrotask(updateIsolate));updateIsolate();
// Explicit keyboard tab contract; one selected panel, no piled-up families.
function param(label,value,help='3 of 3 sample fixtures',max=100){return '<label class="param">'+label+'<input type="range" min="0" max="'+max+'" value="'+value+'" aria-label="'+label+'"><output>'+value+(max===255?' DMX':' %')+'</output><small>'+help+'</small></label>';}
const details='<details class="more"><summary>Channel details — modes, delay, raw DMX</summary><p class="muted">Modes and delay use named profile ranges. Raw DMX appears here for unprofiled fixtures; unknown footprint offers Read RDM info, no invented faders.</p></details>';
renderAttr=function(){
 renderTabs();const body=$('#attrBody');
 $('#attrTabs').setAttribute('aria-label','Attribute families');
 $$('#attrTabs button').forEach(b=>{b.id='ws-tab-'+b.dataset.tab;b.setAttribute('aria-controls','attrBody');b.tabIndex=b.dataset.tab===tab?0:-1;});
 body.setAttribute('role','tabpanel');body.setAttribute('aria-labelledby','ws-tab-'+tab);
 if(!S.sel.length){body.innerHTML='<p>Select fixtures on the grid to control them.</p>';return;}
 let content='';
 if(tab==='dimmer')content='<h4>Master <span class="muted">multiplies parts</span></h4>'+param('Master',75)+ '<div class="row"><button class="btn" data-quick="0">0</button><button class="btn" data-quick="50">50 %</button><button class="btn" data-quick="100">FULL</button><button class="btn" data-fine aria-pressed="false">'+oldIcon('fine')+'Fine</button></div><h4>Parts</h4>'+param('Beam',100,'Sample part · outermost dimmer; inner dimmers held open')+param('Plate',60,'Sample part · Master multiplies this level')+'<p class="muted">Master 75% × Plate 60% = 45%. Sample type anatomy; real cards use selected fixture capabilities.</p>';
 if(tab==='position')content='<h4>Pan / Tilt</h4><div class="xy" role="img" aria-label="Pan and tilt reference; use labelled sliders"><span class="h"></span></div>'+param('Pan',50,'16-bit · Fine scales the complete value 1:10')+param('Tilt',35,'16-bit · first selected is the reference')+'<button class="btn" data-fine aria-pressed="false">Fine</button>';
 if(tab==='dimmer'&&scene==='cells')content='<h4>Cell intensity · MIXED 50–75 %</h4>'+param('Selected cells',50,'Virtual (scales colour) · first selected reference, absolute writes')+'<p class="muted">Select the whole fixture for its Master. Cell colour × virtual level × part/master composition; no independent cell dimmer when its part has a real dimmer.</p>';
 if(tab==='colour')content='<h4>Colour</h4><p class="muted">RGBW mixing · 3 of 3 sample fixtures · DMX values, not measured colour</p><div class="colour-field" aria-hidden="true"></div>'+param('Red',12)+param('Green',24)+param('Blue',80)+param('White',0);
 if(tab==='beam')content='<h4>Gobo wheel</h4><div class="slot-list"><button class="btn" aria-pressed="false">Open</button><button class="btn" aria-pressed="true">Breakup · IN</button><button class="btn">Slot 3 · media not reported</button></div><p class="muted">Names from file · missing media stays selectable</p><div class="row"><button class="btn">OPEN</button><button class="btn" aria-pressed="true">CLOSED · beam blocked</button></div>'+param('Gobo rotation',36,'Physical range not reported',255)+param('Strobe rate',0,'Hz not reported');
 if(tab==='focus')content='<h4>Focus</h4>'+param('Focus',50,'2 of 3 sample fixtures · physical range not reported')+param('Beam zoom',35,'2 of 3 sample fixtures; unrelated to grid zoom');
 if(tab==='shaper')content='<h4>Shaper · 4 blades</h4><svg class="blade-diagram" viewBox="0 0 200 150" role="img" aria-label="Four labelled blades"><circle cx="100" cy="75" r="55" fill="none" stroke="currentColor"/><path d="M65 30h70v90H65Z" fill="none" stroke="currentColor"/><text x="95" y="23">1</text><text x="145" y="78">2</text><text x="95" y="140">3</text><text x="40" y="78">4</text></svg>'+[1,2,3,4].map(n=>param('Blade '+n+' A',20)+param('Blade '+n+' B',25)).join('')+param('Assembly rotate',50);
 if(tab==='control')content='<h4>Fixture commands</h4><p>Control channels are commands. Fan and Set cannot send them.</p><div class="row"><button class="btn" data-command="Reset">Reset…</button><button class="btn" data-command="Lamp off">Lamp off…</button></div><button class="btn hold-example" '+(S.armed?'':'disabled')+'>HOLD 0.75 s · Lamp on</button><p class="muted">'+(S.armed?'Gesture example · sends once for 1 s in production':'Output is not live; HOLD unavailable. Nothing queued for Arm.')+'</p><button class="btn">Store Control preset</button><details class="more"><summary>Raw DMX / Tools</summary><p>No profile · RDM only · slot labels not reported. No footprint reported: Read RDM info first.</p><button class="btn">Read RDM info</button></details>';
 body.innerHTML='<div class="card">'+content+details+'</div>';
 body.querySelectorAll('input[type=range]').forEach(input=>input.oninput=()=>{input.nextElementSibling.textContent=input.value+(input.max==='255'?' DMX':' %');if(input.getAttribute('aria-label')==='Master'){S.sel.forEach(k=>S.vals[k]={...valOf(k),dim:input.value/100,unset:false});renderAll();}});
 body.querySelectorAll('[data-fine]').forEach(b=>b.onclick=()=>{b.setAttribute('aria-pressed',String(b.getAttribute('aria-pressed')!=='true'));b.innerHTML=(b.getAttribute('aria-pressed')==='true'?check:'')+oldIcon('fine')+'Fine';});
 body.querySelectorAll('[data-quick]').forEach(b=>b.onclick=()=>{S.sel.forEach(k=>S.vals[k]={...valOf(k),dim:Number(b.dataset.quick)/100,unset:false});renderAll();renderAttr();});
 body.querySelectorAll('[data-command]').forEach(b=>b.onclick=()=>openQuestion(b.dataset.command+' fixtures?',S.armed?'Sent once: after 1 s each channel returns to its previous value. Nothing is kept in the programmer.':'Output is not live. Nothing queued for Arm.',b,S.armed?b.dataset.command+' fixtures':null));
};
$('#attrTabs').addEventListener('keydown',ev=>{if(!['ArrowLeft','ArrowRight','Home','End'].includes(ev.key))return;ev.preventDefault();const tabs=$$('#attrTabs button');let i=tabs.indexOf(document.activeElement);i=ev.key==='Home'?0:ev.key==='End'?6:(i+(ev.key==='ArrowRight'?1:-1)+7)%7;tab=tabs[i].dataset.tab;renderAttr();$('#ws-tab-'+tab).focus();});
function openQuestion(title,text,trigger,action){
 let dlg=$('#question');if(!dlg){document.body.insertAdjacentHTML('beforeend','<dialog id="question" aria-labelledby="question-title"><h2 id="question-title"></h2><p></p><div class="row"><button class="btn" data-cancel>Cancel</button><button class="btn" data-confirm></button></div></dialog>');dlg=$('#question');}
 dlg.querySelector('h2').textContent=title;dlg.querySelector('p').textContent=text;const confirm=dlg.querySelector('[data-confirm]');confirm.hidden=!action;confirm.textContent=action||'';dlg.style.top=($('.hdr').getBoundingClientRect().bottom+64)+'px';
 const close=()=>{dlg.close();trigger?.focus();};dlg.querySelector('[data-cancel]').onclick=close;confirm.onclick=()=>{announce(action+' · design only');close();};dlg.onkeydown=ev=>{if(ev.key==='Escape'){ev.preventDefault();close();}};dlg.show();dlg.querySelector('[data-cancel]').focus();
}
$('[data-act="clear"]').onclick=ev=>openQuestion('Clear programmer values','Choose family on selection, selection values, or everything. Production preserves these separate scopes.',ev.currentTarget,'Clear family on selection');
$('[data-act="locate"]').onclick=()=>announce('Locate persists as SET in the programmer · design only');
$('[data-act="level"]').onclick=ev=>openQuestion('Level 20 % on selection','Sets the Master level on this selection. Part levels are retained.',ev.currentTarget,'Set Master to 20 %');
$('#stopBtn').addEventListener('click',()=>{updateIsolate();const dlg=$('#question');if(dlg?.open){dlg.querySelector('[data-confirm]').hidden=true;dlg.querySelector('p').textContent='Output is not live. Nothing queued for Arm.';}if(tab==='control')renderAttr();renderAll();});
$('#armBtn').addEventListener('click',()=>{if(tab==='control')renderAttr();renderAll();});
// Controls narrow-width and collapse icons follow the same symbol grammar.
$$('[data-collapse]').forEach(b=>b.addEventListener('click',()=>{b.querySelector('use').setAttribute('href','console-workspace-icons.svg#b5-icon-window-'+(b.getAttribute('aria-expanded')==='true'?'collapse':'expand'));announce(b.getAttribute('aria-expanded')==='true'?'Window expanded':'Window collapsed');}));
// Initial scenes are proposals, not production output/source telemetry.
document.querySelector('.show').setAttribute('title','Fictional sample show · intensity/source telemetry contract not verified');
syncWinMenu();
if(scene==='cells')requestAnimationFrame(()=>{const pane=$('#gridConsole'),cell=pane.querySelector('.cell.csel');if(cell){const a=pane.getBoundingClientRect(),b=cell.closest('.tile').getBoundingClientRect();pane.scrollTop+=Math.max(0,b.bottom-a.bottom+20);pane.scrollLeft+=Math.max(0,b.right-a.right+20);}});
