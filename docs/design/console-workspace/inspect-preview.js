// Browser verification of local design artifacts, not new production tests.
'use strict';
const sceneNames=['default','compact','stacked','resize','drop','empty','tests','cells','unplaced','dock320','all-layers','controls-dimmer','controls-position','controls-colour','controls-beam','controls-focus','controls-shaper','controls-control'];
const sizes=[[1024,768],[1280,800],[1440,900],[1920,1080]];
const results=[],interactions=[];
const frames=sizes.map(([w,h])=>{const frame=document.createElement('iframe');frame.width=w;frame.height=h;document.body.append(frame);return frame;});
async function load(frame,url){return new Promise(resolve=>{frame.onload=()=>resolve(frame.contentWindow);frame.src=url;});}
const visible=el=>{const r=el.getBoundingClientRect();return r.width>0&&r.height>0;};
function inspect(win,scene,mode,coarse){
 const doc=win.document,stop=doc.querySelector('#stopBtn'),workspace=doc.querySelector(scene==='tests'?'#wsTests':'#wsConsole');
 if(!stop||!workspace)return {scene,mode,coarse,error:'screen did not initialize'};
 const r=stop.getBoundingClientRect(),ws=workspace.getBoundingClientRect();
 const undersized=[...doc.querySelectorAll('button,select,summary')].filter(visible).filter(el=>{const r=el.getBoundingClientRect();return coarse&&(r.width<43.5||r.height<43.5);}).map(el=>({name:el.getAttribute('aria-label')||el.textContent.trim(),w:el.getBoundingClientRect().width,h:el.getBoundingClientRect().height}));
 const pane=doc.querySelector(scene==='tests'?'#gridTests':'#gridConsole'),p=pane.getBoundingClientRect(),dock=doc.querySelector('#dock'),d=dock.getBoundingClientRect();
 const floating=[...doc.querySelectorAll('.fw:not(.docked)')].filter(visible).map(el=>{const r=el.getBoundingClientRect();return {id:el.id,inside:r.left>=ws.left-.5&&r.right<=ws.right+.5&&r.top>=ws.top-.5&&r.bottom<=ws.bottom+.5};});
 const rows=[...pane.querySelectorAll('.canvas')].map(canvas=>[...canvas.querySelectorAll('.tile')]);let overlaps=0;
 rows.forEach(tiles=>tiles.forEach((a,i)=>tiles.slice(i+1).forEach(b=>{const x=a.getBoundingClientRect(),y=b.getBoundingClientRect();if(x.left<y.right-.5&&x.right>y.left+.5&&x.top<y.bottom-.5&&x.bottom>y.top+.5)overlaps++;})));
 const toolbar=doc.querySelector('#scr-console .toolbar'),t=toolbar.getBoundingClientRect();
 const percentClipping=[...pane.querySelectorAll('.t-int')].filter(visible).filter(el=>el.getBoundingClientRect().bottom>el.closest('.tile').getBoundingClientRect().bottom+.5).length;
 return {scene,mode,coarse,width:win.innerWidth,height:win.innerHeight,bodyOverflow:doc.documentElement.scrollWidth>win.innerWidth||doc.documentElement.scrollHeight>win.innerHeight,stopVisible:r.left>=0&&r.right<=win.innerWidth&&r.top>=0&&r.bottom<=win.innerHeight,grid:{width:p.width,height:p.height},dockWidth:d.width,gridExcludesDock:scene==='tests'||!d.width||p.right<=d.left+.5,toolbarHeight:t.height,undersized,floating,overlappingTiles:overlaps,percentClipping,defaultOnlyControls:scene!=='default'||[...doc.querySelectorAll('.fw')].filter(visible).map(x=>x.id).join(',')==='fw-controls',tileCount:pane.querySelectorAll('.tile').length};
}
function click(doc,selector){const el=doc.querySelector(selector);if(!el)throw Error('Missing '+selector);el.click();return el;}
function key(win,selector,key,shiftKey=false){const el=win.document.querySelector(selector);el.focus();el.dispatchEvent(new win.KeyboardEvent('keydown',{key,shiftKey,bubbles:true,cancelable:true}));}
async function inspectInteractions(){
 const frame=frames[2],win=await load(frame,'screen.html?scene=default'),doc=win.document;
 click(doc,'#resetWins');
 const edge=doc.querySelector('#dockEdge'),before=doc.querySelector('#dock').getBoundingClientRect().width;
 key(win,'#dockEdge','ArrowLeft');interactions.push({name:'dock-keyboard-20px',pass:doc.querySelector('#dock').getBoundingClientRect().width===before+20});
 click(doc,'#fw-controls [data-dock]');interactions.push({name:'float-removes-empty-dock',pass:doc.querySelector('#dock').hidden&&!doc.querySelector('#fw-controls').classList.contains('docked')});
 const fw=doc.querySelector('#fw-controls'),w0=fw.getBoundingClientRect().width;
 key(win,'#fw-controls [data-resize]','ArrowLeft');interactions.push({name:'resize-keyboard-20px',pass:Math.abs(fw.getBoundingClientRect().width-(w0-20))<.5});
 key(win,'#fw-controls [data-drag]','ArrowLeft');
 const rect=fw.getBoundingClientRect();interactions.push({name:'move-keyboard-inside',pass:rect.left>=0&&rect.right<=1440});
 click(doc,'#fw-controls [data-collapse]');interactions.push({name:'collapse',pass:fw.classList.contains('collapsed')&&fw.querySelector('.fw-body').getBoundingClientRect().height===0});
 click(doc,'#fw-controls [data-collapse]');click(doc,'#fw-controls [data-dock]');
 click(doc,'#fw-controls [data-close]');interactions.push({name:'hide-focus-windows',pass:fw.hidden&&doc.activeElement.dataset.menu==='m-windows'});
 click(doc,'[data-menu="m-windows"]');click(doc,'#m-windows [data-win="fw-controls"]');interactions.push({name:'reopen-focus-title',pass:!fw.hidden&&doc.activeElement.hasAttribute('data-drag')});
 click(doc,'#addBtn');click(doc,'[data-key="f401/2"]');
 const count=doc.querySelector('#selCount').textContent;
 click(doc,'.nav [data-screen="tests"]');interactions.push({name:'shared-selection-tests',pass:doc.querySelector('#selCount2').textContent===count});
 click(doc,'#gridTests [data-key="f101"]');click(doc,'[data-tests] .ttile');interactions.push({name:'test-tag-in-grid',pass:doc.querySelector('#gridTests .tag--test')!==null});
 click(doc,'#m-view [data-view="live"]');interactions.push({name:'live-off-retains-test-state',pass:doc.querySelector('#gridTests .tag--test')!==null&&win.getComputedStyle(doc.querySelector('#gridTests .t-int-word')).display==='none'});click(doc,'#m-view [data-view="live"]');
 click(doc,'.nav [data-screen="console"]');click(doc,'#attrTabs [data-tab="control"]');click(doc,'[data-command="Reset"]');
 interactions.push({name:'disarmed-command',pass:doc.querySelector('#question').open&&doc.querySelector('#question [data-confirm]').hidden&&doc.activeElement.hasAttribute('data-cancel')});
 key(win,'#question [data-cancel]','Escape');interactions.push({name:'dialog-escape-restores',pass:!doc.querySelector('#question').open&&doc.activeElement.dataset.command==='Reset'});
 click(doc,'#armBtn');click(doc,'[data-command="Reset"]');click(doc,'#stopBtn');
 interactions.push({name:'disarm-hides-send',pass:doc.querySelector('#question [data-confirm]').hidden});
 click(doc,'#question [data-cancel]');click(doc,'#resetWins');
 click(doc,'#fw-controls [data-dock]');key(win,'#fw-controls [data-resize]','ArrowLeft');
 const rememberedWidth=doc.querySelector('#fw-controls').getBoundingClientRect().width;
 const reloaded=await load(frame,'screen.html?scene=default'),reloadedDoc=reloaded.document;
 interactions.push({name:'window-memory-reload',pass:reloadedDoc.querySelector('#dock').hidden&&Math.abs(reloadedDoc.querySelector('#fw-controls').getBoundingClientRect().width-rememberedWidth)<.5});
 click(reloadedDoc,'#resetWins');
}
(async()=>{
 try{
  for(const coarse of [false,true])for(const mode of ['dark','light','greyscale','dim'])for(const scene of sceneNames){
   await Promise.all(frames.map(async frame=>{const win=await load(frame,'screen.html?scene='+scene+'&theme='+(mode==='light'?'light':'dark')+(mode==='greyscale'?'&grey=1':'')+(mode==='dim'?'&grey=1&dim=1':'')+(coarse?'&coarse=1':''));results.push(inspect(win,scene,mode,coarse));}));
  }
  await inspectInteractions();
  document.getElementById('report').textContent=JSON.stringify({checks:results,interactions});
  document.title='Workspace verification complete';
 }catch(error){document.getElementById('report').textContent=JSON.stringify({checks:results,interactions,error:error.stack});document.title='Workspace verification failed';}
})();
