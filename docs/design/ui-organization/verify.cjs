// Artifact verification only; never connects to the production application.
const {chromium}=require('C:/Users/CDT_LD/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const fs=require('node:fs'),path=require('node:path');
const out=path.join(__dirname,'evidence');fs.mkdirSync(out,{recursive:true});
(async()=>{
 process.env.B5_REVIEW_PORT='5838'; const {server}=await import('./serve.mjs');
 const browser=await chromium.launch({executablePath:'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',headless:true});
 const page=await browser.newPage(),errors=[],results=[],pairs=[],interactions=[];
 page.on('pageerror',e=>errors.push(String(e)));page.on('response',r=>{if(r.status()>=400)errors.push(r.status()+' '+r.url());});
 const base='http://127.0.0.1:5838/docs/design/ui-organization/screen.html';
 for(const [w,h] of [[1440,900],[1024,768]])for(const mode of ['dark','light','grey'])for(const scene of ['patch','settings','devices','nodes','analyzer','walk','nav-groups','nav-divider','nav-existing'])for(const coarse of [false,true]){
  await page.setViewportSize({width:w,height:h});await page.goto(`${base}?scene=${scene.startsWith('nav-')?'patch':scene}&mode=${mode}&nav=${scene.startsWith('nav-')?scene.slice(4):'groups'}${coarse?'&coarse=1':''}`);
  const r=await page.evaluate(({coarse})=>{
   const rect=e=>{let r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom};},stop=rect(document.querySelector('#stopBtn'));
   const visible=e=>{const r=e.getBoundingClientRect();return r.width>0&&r.height>0;};
   const small=[...document.querySelectorAll('button,select,summary')].filter(visible).filter(e=>{let r=rect(e);return coarse&&(r.w<43.5||r.h<43.5);}).map(e=>e.getAttribute('aria-label')||e.textContent.trim());
   let scroller=document.querySelector('.table-scroll'),first=document.querySelector('tbody tr'),tableWidth=document.querySelector('table')?.getBoundingClientRect().width;
   return {overflow:document.documentElement.scrollHeight>innerHeight||document.documentElement.scrollWidth>innerWidth,stop,stopVisible:stop.x>=0&&stop.right<=innerWidth&&stop.bottom<=innerHeight,small,firstRow:first?rect(first):null,tableWidth,scroller:scroller?rect(scroller):null,rows:document.querySelectorAll('tbody tr').length,dockExcludesList:!document.querySelector('.inspector.dock')||rect(document.querySelector('.list-pane')).right<=rect(document.querySelector('.inspector.dock')).x+1,footerVisible:rect(document.querySelector('.fbar')).bottom<=innerHeight};
  },{coarse});results.push({w,h,mode,scene,coarse,...r});
  if(!coarse)await page.screenshot({path:path.join(out,`${w}-${scene}-${mode}.png`)});
 }
 await page.setViewportSize({width:1440,height:900});await page.goto(base+'?scene=patch');
 const check=async(name,fn)=>{try{interactions.push({name,pass:!!await fn()});}catch(e){interactions.push({name,pass:false,error:String(e)});}};
 await check('90 fictional Patch rows',async()=>await page.locator('tbody tr').count()===90);
 await check('sticky header during table scroll',async()=>page.evaluate(()=>{let s=document.querySelector('.table-scroll'),th=s.querySelector('th'),before=th.getBoundingClientRect().top;s.scrollTop=1000;return Math.abs(th.getBoundingClientRect().top-before)<1;}));
 await page.locator('.table-scroll').evaluate(e=>e.scrollTop=0);
 await check('row opens matching inspector',async()=>{await page.locator('[data-row="5"]').click();return (await page.locator('.fw-title').textContent()).includes('106');});
 await check('checkbox selection independent of inspector',async()=>{await page.locator('[data-check="104"]').check();return (await page.locator('#count').textContent()).includes('4 selected')&&(await page.locator('.fw-title').textContent()).includes('106');});
 await check('menu Escape restores trigger focus',async()=>{await page.locator('[data-menu="import"]').click();await page.keyboard.press('ArrowDown');await page.keyboard.press('Escape');return page.locator('[data-menu="import"]').evaluate(e=>e===document.activeElement&&e.getAttribute('aria-expanded')==='false');});
 await check('dock keyboard resizes 20px',async()=>{let edge=page.locator('.dock-edge'),w=await page.locator('.inspector').evaluate(e=>e.getBoundingClientRect().width);await edge.focus();await page.keyboard.press('ArrowLeft');return page.locator('.inspector').evaluate((e,w)=>Math.abs(e.getBoundingClientRect().width-w-20)<1,w);});
 await check('float removes reserved dock space',async()=>{let w=await page.locator('.table-scroll').evaluate(e=>e.clientWidth);await page.locator('[data-float]').click();return (await page.locator('.table-scroll').evaluate(e=>e.clientWidth))>w;});
 await check('collapse hides inspector controls',async()=>{await page.locator('[data-collapse]').click();return await page.locator('.fw-body').isHidden();});
 await page.goto(base+'?scene=settings');
 await check('field change stages before Apply',async()=>{await page.locator('[aria-label="Poll interval (ms)"]').fill('4000');return (await page.locator('.apply-status').textContent()).includes('staged');});
 await check('Apply action reports preview only',async()=>{await page.locator('[data-apply]').click();return (await page.locator('.apply-status').textContent()).includes('preview only');});
 await check('Settings index scroll stays internal',async()=>{await page.locator('a[href="#setting-6"]').click();return page.evaluate(()=>document.querySelector('.settings-scroll').scrollTop>0&&scrollY===0&&document.querySelector('#stopBtn').getBoundingClientRect().height===60);});
 await check('typed RESET is retained confirmation',async()=>{await page.locator('#resetWord').fill('reset');let a=await page.locator('[data-reset]').isDisabled();await page.locator('#resetWord').fill('RESET');return a&&await page.locator('[data-reset]').isEnabled();});
 await page.goto(base+'?scene=walk');
 await check('walk Next advances one fixture',async()=>{await page.locator('[data-step="1"]').click();return (await page.locator('#walkCount').textContent()).includes('14 / 90');});
 // Measure every new content text/surface and essential boundary role in browser.
 const rolePairs=[['text-primary','bg-canvas',4.5],['text-primary','bg-surface',4.5],['text-primary','bg-surface-raised',4.5],['text-primary','bg-surface-overlay',4.5],['text-primary','bg-inset',4.5],['text-primary','bg-pressed',4.5],['text-secondary','bg-canvas',4.5],['text-secondary','bg-surface',4.5],['text-secondary','bg-pressed',4.5],['text-danger','bg-surface-overlay',4.5],['border-control','bg-canvas',3],['border-control','bg-surface',3],['border-control','bg-surface-raised',3],['border-control','bg-inset',3],['border-control','bg-surface-overlay',3],['accent-border','bg-canvas',3],['accent-border','bg-pressed',3],['border-focus','bg-surface',3],['text-primary','bg-hover',4.5],['text-secondary','bg-inset',4.5],['border-control','bg-hover',3]];
 for(const theme of ['dark','light']){
  await page.goto(base+'?scene=patch&mode='+theme);
  let p=await page.evaluate(rolePairs=>{
   const rgb=role=>{let e=document.createElement('i');e.style.color=`var(--b5-color-${role})`;document.body.append(e);let c=getComputedStyle(e).color.match(/[\d.]+/g).map(Number);e.remove();return c;};
   const lum=c=>c.slice(0,3).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4;}).reduce((a,x,i)=>a+x*[.2126,.7152,.0722][i],0);
   return rolePairs.map(([fg,bg,min])=>{let f=rgb(fg),b=rgb(bg),a=lum(f),c=lum(b),ratio=(Math.max(a,c)+.05)/(Math.min(a,c)+.05);return {fg,bg,foreground:f,background:b,ratio,min,pass:ratio>=min};});
  },rolePairs);pairs.push(...p.map(x=>({theme,...x})));
 }
 fs.writeFileSync(path.join(out,'browser-checks.json'),JSON.stringify({results,interactions,errors},null,2));fs.writeFileSync(path.join(__dirname,'contrast.json'),JSON.stringify(pairs,null,2));
 console.log(JSON.stringify({cases:results.length,failures:results.filter(x=>x.overflow||!x.stopVisible||x.small.length||!x.footerVisible||!x.dockExcludesList).map(x=>({w:x.w,scene:x.scene,mode:x.mode,coarse:x.coarse,small:x.small.slice(0,3),overflow:x.overflow,dock:x.dockExcludesList})),interactions,errors,contrastFailures:pairs.filter(x=>!x.pass)},null,2));await browser.close();server.close();
})().catch(e=>{console.error(e);process.exitCode=1;});
