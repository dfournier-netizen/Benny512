// One persistent, rig-independent library; no duplicate store in the browser.
const LibraryPanel=(()=>{
  let dialog, records=[], entries=[], query='', verifiedOnly=false, chosen=null, pending=null, busy=false;
  const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const verified=m=>!!m.verifiedHash && !!m.verifiedAt && !m.verifiedAt.startsWith('0001-');
  // Full channel detail (every function, set and wheel) exists only for modes
  // read by the current GDTF parser; older imports and patch harvests say so.
  const detailKnown=m=>!!m.wheelsKnown||Object.values(m.channelFunctions||{}).some(cf=>cf.functionsKnown);
  function status(s){dialog.querySelector('[data-library-status]').textContent=s;}
  async function refresh(){records=(await Api.getLibrary()).records;const p=await Api.getPatch();entries=p.patch?p.patch.entries:[];if(chosen){const r=records.find(r=>r.key===chosen.r.key),m=r?.modes.find(m=>m.name===chosen.m.name);chosen=m?{r,m}:null;}}
  async function open(){
    if(!dialog){dialog=document.createElement('dialog');dialog.className='b5-workspace-dialog';dialog.setAttribute('aria-label','Fixture library');document.body.appendChild(dialog);}
    try {await refresh();chosen=null;pending=null;render();if(!dialog.open)dialog.showModal();}catch(e){if(!dialog.open){dialog.innerHTML='<p data-library-status></p><form method="dialog"><button class="b5-btn">Close</button></form>';dialog.showModal();}status(e.message);}
  }
  async function action(fn,message){
    if(busy)return;busy=true;
    dialog.querySelectorAll('button:not([data-close-library]),input[type=file]').forEach(b=>b.disabled=true);
    try{const result=await fn();await refresh();render();status(typeof message==='function'?message(result):message);}
    catch(e){status(e.message);}finally{busy=false;dialog.querySelectorAll('button,input[type=file]').forEach(b=>b.disabled=false);}
  }
  function render(){
    dialog.innerHTML=`<div class="b5-row"><h2>Fixture library</h2><button class="b5-btn" data-close-library>Close</button></div>
      <p class="b5-caption">Shared across shows. Saved on this PC. Verification is per mode, by the operator.</p>
      <p data-library-status role="status"></p>
      <div class="b5-row"><label class="b5-btn">Import GDTF / library JSON<input data-library-file type="file" accept=".gdtf,.json" class="b5-visually-hidden"></label><button class="b5-btn" data-library-harvest>Save profiles from this patch</button><a class="b5-btn" href="${Api.libraryExportUrl()}">Export library JSON</a></div>
      <p class="b5-caption">Exports profiles, verification records, and original GDTFs imported here.</p>
      <div data-library-import></div>
      <div class="b5-row"><label>Search <input class="b5-input" type="search" data-library-search value="${esc(query)}" placeholder="Manufacturer or fixture"></label><label><input type="checkbox" data-library-verified ${verifiedOnly?'checked':''}>Verified modes only</label></div>
      <div class="b5-workspace-list" data-library-records></div><section data-library-use></section>`;
    dialog.querySelector('[data-close-library]').onclick=()=>dialog.close();
    dialog.querySelector('[data-library-search]').oninput=e=>{query=e.target.value;paintRecords();};
    dialog.querySelector('[data-library-verified]').onchange=e=>{verifiedOnly=e.target.checked;paintRecords();};
    dialog.querySelector('[data-library-file]').onchange=importChosen;
    dialog.querySelector('[data-library-harvest]').onclick=()=>action(()=>Api.libraryFromPatch([]),r=>`${r.added} added · ${r.updated} updated · ${r.unchanged} unchanged · ${r.skipped} skipped`);
    paintRecords();paintImport();paintUse();
  }
  function paintRecords(){
    const list=dialog.querySelector('[data-library-records]');
    list.innerHTML=records.map((r,ri)=>{
      if(!(r.manufacturer+' '+r.model).toLowerCase().includes(query.toLowerCase()))return '';
      const modes=r.modes.map((m,mi)=>verifiedOnly&&!verified(m)?'':`<div class="b5-workspace-item"><strong>${esc(m.name||'Unnamed mode')}</strong> · ${m.footprint} channels <span class="b5-pill b5-pill--tag ${verified(m)?'b5-pill--ok':'b5-pill--unread'}">${verified(m)?'Operator verified':'Not verified'}</span><div class="b5-caption">${esc(m.origin?.detail||m.origin?.source||'Source unknown')}${verified(m)?' · '+esc(new Date(m.verifiedAt).toLocaleDateString())+' · '+esc(m.verificationNote):''} · Channel detail: ${detailKnown(m)?'full':'not imported'}</div><div class="b5-row"><button class="b5-btn b5-btn--sm" data-use-mode="${ri}:${mi}">Use in patch</button><button class="b5-btn b5-btn--sm" data-verify-mode="${ri}:${mi}">${verified(m)?'Clear verification':'Mark verified…'}</button></div></div>`).join('');
      const missing=r.modes.some(m=>!detailKnown(m)),files=r.sourceFiles||[];
      const reread=!missing?'':files.length?files.map((f,fi)=>`<button class="b5-btn b5-btn--sm" data-reread="${ri}:${fi}">Re-read channel detail from ${esc(f.name)}</button>`).join(''):'<p class="b5-caption" data-detail-missing>Channel detail not imported — no original GDTF is stored for this type. Re-import the GDTF to get full channel detail.</p>';
      return modes?`<section><h3>${esc(r.manufacturer+' '+r.model)}</h3><div class="b5-row">${files.map(f=>`<a class="b5-btn b5-btn--sm" href="/api/library/source?key=${encodeURIComponent(r.key)}&amp;hash=${encodeURIComponent(f.sha256)}" download>${esc(f.name)}</a>`).join('')}</div>${reread?`<div class="b5-row">${reread}</div>`:''}${modes}</section>`:'';
    }).join('')||'<p>No matching modes. Import a GDTF or save profiles from a patch.</p>';
    list.querySelectorAll('[data-use-mode]').forEach(b=>b.onclick=()=>{const [ri,mi]=b.dataset.useMode.split(':').map(Number);chosen={r:records[ri],m:records[ri].modes[mi]};paintUse();const area=dialog.querySelector('[data-library-use]');area.scrollIntoView({block:'start'});area.querySelector('input').focus({preventScroll:true});});
    list.querySelectorAll('[data-reread]').forEach(b=>b.onclick=()=>{
      const [ri,fi]=b.dataset.reread.split(':').map(Number),r=records[ri],f=r.sourceFiles[fi];
      action(async()=>{const buf=await Api.getLibrarySourceBytes(r.key,f.sha256);return MvrImport.rereadGdtfIntoLibrary(buf,f.name);},
        x=>`Re-read ${f.name}: ${x.result.added} added · ${x.result.updated} updated · ${x.result.skipped} unchanged${(x.parsed.warnings||[]).length?` · ${x.parsed.warnings.length} file warning${x.parsed.warnings.length===1?'':'s'}`:''}. Changed modes need verifying again.`);
    });
    list.querySelectorAll('[data-verify-mode]').forEach(b=>b.onclick=async()=>{
      const [ri,mi]=b.dataset.verifyMode.split(':').map(Number),r=records[ri],m=r.modes[mi];
      if(!await Workspace.ask(verified(m)?`Clear verification for ${r.model} / ${m.name}?`:`Confirm you tested ${r.model} / ${m.name} on hardware and verified its footprint and channel functions?`))return;
      await action(()=>Api.verifyLibraryMode({key:r.key,mode:m.name,verified:!verified(m),note:'Operator confirmed hardware test',expected:m}),'Verification updated');
    });
  }
  async function importChosen(e){
    const f=e.target.files[0];if(!f)return;
    try {
      const isGdtf=f.name.toLowerCase().endsWith('.gdtf');
      if(f.size>(isGdtf?8:120)*1024*1024)throw Error(isGdtf?'GDTF files up to 8 MB.':'Library exports up to 120 MB.');
      if(f.name.toLowerCase().endsWith('.gdtf')){
        // MvrImport.libraryDocFromGdtf is the shared builder every GDTF
        // entry point uses (see its doc comment). This dialog used to build
        // the record inline; patch.js now imports GDTFs into the library
        // too, and two hand-rolled copies of one shape is how they drift.
        const buf=await f.arrayBuffer(),p=await MvrImport.parseGdtfFile(buf);
        pending={doc:MvrImport.libraryDocFromGdtf(p,f.name,buf),name:f.name,warnings:p.warnings||[]};
      }else{pending={doc:JSON.parse(await f.text()),name:f.name,warnings:[]};if(pending.doc.format!=='benny512-fixture-library')throw Error('Choose a Benny512 library export, not a patch JSON.');}
      paintImport();status('Review the import, then apply. Existing types are merged.');
    }catch(e){pending=null;paintImport();status(e.message);}
  }
  function paintImport(){
    const area=dialog.querySelector('[data-library-import]');
    area.innerHTML=pending?`<div class="b5-inset"><strong>${esc(pending.name)}</strong> · ${pending.doc.records?.length||0} fixture types${pending.warnings.map(w=>`<p>${esc(typeof w==='string'?w:JSON.stringify(w))}</p>`).join('')}<div class="b5-row"><button class="b5-btn b5-btn--primary" data-apply-library>Apply import</button><button class="b5-btn" data-cancel-library>Cancel</button></div></div>`:'';
    if(!pending)return;
    area.querySelector('[data-cancel-library]').onclick=()=>{pending=null;paintImport();};
    area.querySelector('[data-apply-library]').onclick=()=>action(async()=>{const res=await Api.importLibrary(pending.doc,'merge');pending=null;chosen=null;return res;},'Library imported; changed mode data invalidates its old verification');
  }
  function paintUse(){
    const area=dialog.querySelector('[data-library-use]');if(!chosen){area.innerHTML='';return;}
    const {r,m}=chosen;
    area.innerHTML=`<h3>${esc(r.model)} · ${esc(m.name)}</h3><p>${m.footprint} channels · ${verified(m)?'Operator verified':'Not verified'}</p>
      <form data-library-add class="b5-stack"><div class="b5-row"><label>Name <input class="b5-input" name="fixtureName" value="${esc(r.model)}" required></label><label>Universe <input class="b5-input" name="universe" type="number" min="${UI.userInputAttrs().min}" max="${UI.userInputAttrs().max}" value="${UI.formatUser(0)}" required></label><label>Address <input class="b5-input" name="address" type="number" min="1" max="512" value="1" required></label></div><button class="b5-btn b5-btn--primary" type="submit">Add new patch entry</button></form>
      <details><summary>Apply profile to existing entries</summary><div class="b5-workspace-list">${entries.map(e=>`<label class="b5-workspace-item"><input type="checkbox" data-profile-entry="${esc(e.id)}">${esc(e.name||e.id)} · ${UI.formatUser(e.universe)} / ${e.startAddress}</label>`).join('')}</div><button class="b5-btn" data-apply-profile>Apply to selected entries…</button></details>`;
    area.querySelector('[data-library-add]').onsubmit=async e=>{
      e.preventDefault();const form=e.target,values=new FormData(form),address=Number(values.get('address'));
      if(address+m.footprint-1>512){status('This profile extends past channel 512. Choose another address.');return;}
      await action(()=>Api.createPatchEntry({name:values.get('fixtureName'),fixtureType:(r.manufacturer+' '+r.model).trim(),mode:m.name,footprint:m.footprint,universe:UI.parseUser(values.get('universe')),startAddress:address,channelFunctions:m.channelFunctions,wheels:m.wheels||[],wheelsKnown:!!m.wheelsKnown}),'Added to the active patch; commit it to a physical fixture in Reconcile. Check for address overlaps.');
    };
    area.querySelector('[data-apply-profile]').onclick=async()=>{
      const ids=[...area.querySelectorAll('[data-profile-entry]:checked')].map(e=>e.dataset.profileEntry);
      if(!ids.length){status('Select patch entries first.');return;}
      if(!await Workspace.ask(`Replace the mode, footprint and channel map of ${ids.length} entries with ${r.model} / ${m.name}? Addresses stay fixed; check for overlaps afterwards.`))return;
      await action(()=>Api.libraryReprofile({key:r.key,mode:m.name,entryIds:ids}),'Profile applied. Check the patch for address overlaps.');
    };
  }
  window.addEventListener('b5-show-changed',()=>{chosen=null;entries=[];if(dialog?.open)dialog.close();});
  return {open};
})();
