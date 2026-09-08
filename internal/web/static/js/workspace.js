// Shared show tools. Reads use cached evidence and never keep a test alive.
const Workspace = (() => {
  let data = null, selected = [], dialog = null, loading = false;
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  function selection(ids) {
    selected = [...new Set(ids || [])];
    const count = dialog && dialog.querySelector('[data-selection-count]');
    if (count) count.textContent = `Selection & groups · ${selected.length} selected`;
    window.dispatchEvent(new CustomEvent('b5-selection', {detail:selected.slice()}));
  }
  async function context() {
    const root = document.getElementById('showContext');
    try {
      const c = await Api.getContext();
      root.querySelector('[data-show]').textContent = c.name || 'No active show';
      root.querySelector('[data-nic]').textContent = c.nic || 'Interface not reported';
      root.querySelector('[data-output]').textContent = c.simulation ? (c.output ? 'SIMULATED output' : 'REHEARSAL · stopped') : (c.output ? 'Output LIVE' : 'Output stopped');
    } catch (_) { root.querySelector('[data-output]').textContent = 'Connection lost — output unknown'; }
  }
  function init() {
    const root = document.getElementById('showContext');
    root.innerHTML = `<strong data-show class="b5-show-context__name">Loading show…</strong><span data-nic class="b5-show-context__nic b5-text-sm"></span><span data-output role="status">Output unknown</span><button class="b5-btn b5-btn--sm" data-tools>Show tools</button><button class="b5-btn b5-btn--sm" data-library>Fixture library</button><button class="b5-btn b5-btn--sm b5-btn--danger" data-stop>Stop all output</button>`;
    root.querySelector('[data-tools]').onclick = open;
    root.querySelector('[data-library]').onclick = LibraryPanel.open;
    root.querySelector('[data-stop]').onclick = async () => {
      try { await Api.stopAllOutput(); await context(); }
      catch (e) {root.querySelector('[data-output]').textContent='Stop failed: '+e.message;}
    };
    window.addEventListener('b5-show-changed', () => { data=null; selection([]); context(); if(dialog && dialog.open) dialog.close(); });
    window.addEventListener('b5-patch-selection', e => selection(e.detail));
    window.addEventListener('b5-device-selection', async e => {
      try { const w=await Api.getWorkspace(); selection(w.entries.filter(x=>x.uid===e.detail).map(x=>x.id)); } catch (_) {}
    });
    context(); setInterval(context, 2000);
  }
  async function open() {
    if (loading) return;
    loading=true;
    try {
      await Api.getPatch(); // refresh the show identity before showing its tools
      data=await Api.getWorkspace();
      selected=selected.filter(id=>data.entries.some(e=>e.id===id));
      if(!dialog) {dialog=document.createElement('dialog');dialog.className='b5-workspace-dialog';document.body.appendChild(dialog);}
      render(); if(!dialog.open) dialog.showModal();
    } catch(e) {document.querySelector('[data-output]').textContent=e.message;}
    finally {loading=false;}
  }
  function status(message) {const el=dialog.querySelector('[data-status]');if(el) el.textContent=message;}
  async function action(fn,message) {
    try {await fn(); data=await Api.getWorkspace();render();status(message);await context();}
    catch(e) {status(e.message);}
  }
  function ask(question, needsName=false) {
    return new Promise(resolve => {
      const box=document.createElement('dialog');box.className='b5-workspace-dialog';
      box.innerHTML=`<form method="dialog"><h3>${esc(question)}</h3>${needsName?'<label>Name <input class="b5-input" name="itemName" maxlength="80" required autofocus></label>':''}<div class="b5-row"><button class="b5-btn" type="button" data-cancel>Cancel</button><button class="b5-btn b5-btn--primary" type="submit">${needsName?'Save':'Confirm'}</button></div></form>`;
      let value=null;
      box.querySelector('form').onsubmit=e=>{e.preventDefault();value=needsName?box.querySelector('input').value.trim():true;if(needsName&&!value)return;box.close();};
      box.querySelector('[data-cancel]').onclick=()=>box.close();
      box.onclose=()=>{box.remove();resolve(value);};
      document.body.appendChild(box);box.showModal();
    });
  }
  async function named(actionName, message, extra={}) {
    const name=await ask(actionName==='snapshot'?'Record rig baseline':actionName==='save-preset'?'Save test preset':'Save fixture group',true);if(!name)return;
    action(()=>Api.workspaceAction(actionName,{name,...extra}),message);
  }
  function navigate(view,id,uid) {
    dialog.close();if(id) selection([id]);
    if(view==='devices' && uid) {
      window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'devices'}));DevicesScreen.inspect(uid);return;
    }
    window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'patch'}));
    PatchScreen.focusEntry(view,id);
  }
  function render() {
    const expanded=[...dialog.querySelectorAll('details')].map(d=>d.open);
    dialog.innerHTML=`<div class="b5-row"><h2>${esc(data.name || 'Show tools')}</h2><button class="b5-btn" data-close>Close</button></div>
      <p data-status role="status" class="b5-text-sm"></p>
      <section><div class="b5-row"><h3>Needs attention · ${data.issues.length}</h3><button class="b5-btn b5-btn--sm" data-refresh>Refresh</button></div>
      <p class="b5-caption">Cached observations. Discover and re-read for fresh evidence.</p>
      <div class="b5-workspace-list">${data.issues.map((i,n)=>`<div class="b5-workspace-item"><strong>${esc(i.name||i.entryId)}</strong> · ${esc(i.message)} <button class="b5-btn b5-btn--sm" data-issue="${n}">${i.action==='devices'?'Inspect':i.action==='entries'?'Edit entry':'Reconcile'}</button></div>`).join('') || '<p>No issues in the current evidence.</p>'}</div></section>
      <details><summary data-selection-count>Selection & groups · ${selected.length} selected</summary>
      <div class="b5-row"><button class="b5-btn" data-save-group>Save selection as group</button><button class="b5-btn" data-test-selection>Use selection in Function check</button></div>
      <div class="b5-row">${data.groups.map((g,i)=>`<span><button class="b5-btn" data-group="${i}">${esc(g.name)} · ${g.entryIds.length}</button><button class="b5-btn b5-btn--sm" data-delete-group="${i}" aria-label="Delete group ${esc(g.name)}">Delete</button></span>`).join('')}</div>
      <div class="b5-workspace-list">${data.entries.map(e=>`<div class="b5-workspace-item"><label><input type="checkbox" data-entry="${esc(e.id)}" ${selected.includes(e.id)?'checked':''}>${esc(e.name||e.id)} · U${UI.formatUniverse(e.universe)} / ${e.startAddress}</label><span class="b5-caption">${e.phaseCount} phase slots · ${esc(e.phaseSource)}</span></div>`).join('')}</div></details>
      <details><summary>Test presets · ${data.presets.length}</summary><button class="b5-btn" data-save-preset>Save current function tests</button><p class="b5-caption">Loads settings and exact fixture selection. Output stays stopped.</p>
      <div class="b5-row">${data.presets.map((p,i)=>`<span><button class="b5-btn" data-preset="${i}">Load ${esc(p.name)}</button><button class="b5-btn b5-btn--sm" data-delete-preset="${i}" aria-label="Delete preset ${esc(p.name)}">Delete</button></span>`).join('')}</div></details>
      <details><summary>Rig baselines · ${data.baselines.length}</summary><button class="b5-btn" data-snapshot>Record baseline…</button><p class="b5-caption">Records the patch, cached fixture configuration, and open issues.</p>
      <div class="b5-workspace-list">${data.baselines.map((b,i)=>`<div class="b5-workspace-item"><strong>${esc(b.name)}</strong> · ${esc(new Date(b.at).toLocaleString())} · ${b.issues} issues at capture <button class="b5-btn b5-btn--sm" data-report="${i}">Compare now</button><a class="b5-btn b5-btn--sm" href="/api/patch/workspace/report/${encodeURIComponent(b.id)}?format=txt">Export report</a><button class="b5-btn b5-btn--sm" data-delete-baseline="${i}" aria-label="Delete baseline ${esc(b.name)}">Delete</button></div>`).join('')}</div><div data-report-body></div></details>
      ${data.canRehearse ? `<details><summary>Offline rehearsal</summary><label>Scenario <select class="b5-select" data-fault><option value="none">As patched</option><option value="missing">Missing fixtures</option><option value="address">Wrong addresses</option><option value="slow">Delayed RDM responses</option></select></label><button class="b5-btn" data-rehearse>Build rehearsal…</button><p class="b5-caption">Stops live output. Opens an in-memory rig on fake lighting transport. Replaces any earlier rehearsal.</p><div data-rehearsal-link></div></details>` : ''}
      <details><summary>Show recovery</summary><div class="b5-row"><button class="b5-btn" data-recover>Restore preceding save…</button><button class="b5-btn b5-btn--danger" data-reset>Reset this show…</button><a class="b5-btn" href="${Api.patchExportUrl('json')}">Export show JSON</a></div><p class="b5-caption">Reset keeps other shows and the Fixture Library. The preceding save is a single-step recovery copy.</p></details>`;
    dialog.querySelector('[data-close]').onclick=()=>dialog.close();
    dialog.querySelectorAll('details').forEach((d,i)=>{d.open=!!expanded[i];});
    for(const [kind,items] of [['group',data.groups],['preset',data.presets],['baseline',data.baselines]]) {
      dialog.querySelectorAll(`[data-delete-${kind}]`).forEach(b=>b.onclick=async()=>{
        const item=items[Number(b.getAttribute(`data-delete-${kind}`))];
        if(await ask(`Delete ${kind} “${item.name}”?`))action(()=>Api.workspaceAction(`delete-${kind}`,{id:item.id}),'Deleted; preceding save available for recovery');
      });
    }
    dialog.querySelector('[data-refresh]').onclick=()=>action(async()=>{},'Updated cached evidence');
    dialog.querySelectorAll('[data-issue]').forEach(b=>b.onclick=()=>{const i=data.issues[Number(b.dataset.issue)];navigate(i.action,i.entryId,i.uid);});
    dialog.querySelectorAll('[data-entry]').forEach(b=>b.onchange=()=>selection([...dialog.querySelectorAll('[data-entry]:checked')].map(x=>x.dataset.entry)));
    dialog.querySelector('[data-save-group]').onclick=()=>named('save-group','Group saved',{entryIds:selected});
    dialog.querySelectorAll('[data-group]').forEach(b=>b.onclick=()=>{
      const ids=data.groups[Number(b.dataset.group)].entryIds;
      if(ids.some(id=>!data.entries.some(e=>e.id===id))) {status('Group contains removed fixtures; select a new group.');return;}
      selection(ids);render();dialog.querySelector('details').open=true;
    });
    dialog.querySelector('[data-test-selection]').onclick=()=>action(async()=>{
      if(!selected.length)throw new Error('Select at least one fixture.');
      await Api.rigCheckStop();await Api.patternSetScope({scopeKind:'selection',entryIds:selected});
      dialog.close();window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'patch'}));await PatchScreen.openFunctionCheck();
    },'Selection ready; output stopped');
    dialog.querySelector('[data-save-preset]').onclick=()=>named('save-preset','Test preset saved');
    dialog.querySelectorAll('[data-preset]').forEach(b=>b.onclick=()=>action(async()=>{
      const p=data.presets[Number(b.dataset.preset)];await Api.workspaceAction('load-preset',{id:p.id});selection(p.entryIds);
      dialog.close();window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'patch'}));await PatchScreen.openFunctionCheck();
    },'Preset loaded; output stopped'));
    dialog.querySelector('[data-snapshot]').onclick=()=>named('snapshot','Baseline recorded');
    const rehearsal=dialog.querySelector('[data-rehearse]');
    if(rehearsal) rehearsal.onclick=async()=>{
      if(!await ask('Stop live output and build a separate simulated copy of this show?'))return;
      rehearsal.disabled=true;
      try {
        const result=await Api.workspaceAction('rehearse',{confirm:'REHEARSE',fault:dialog.querySelector('[data-fault]').value});
        const url=new URL(location.href);url.port=String(result.port);url.pathname='/';url.search='';url.hash='';
        dialog.querySelector('[data-rehearsal-link]').innerHTML=`<a class="b5-btn b5-btn--primary" href="${esc(url.href)}" target="_blank" rel="noopener">Open rehearsal</a>`;
        status('Rehearsal ready. Live output is stopped.');await context();
      }catch(e){status(e.message);}finally{rehearsal.disabled=false;}
    };
    dialog.querySelectorAll('[data-report]').forEach(b=>b.onclick=async()=>{
      try {const r=await Api.baselineReport(data.baselines[Number(b.dataset.report)].id);dialog.querySelector('[data-report-body]').innerHTML=`<p>${esc(r.evidence)}</p>${r.changes.map(c=>`<p>${esc(c)}</p>`).join('')||'<p>No changes in recorded evidence.</p>'}`;}catch(e){status(e.message);}
    });
    dialog.querySelector('[data-recover]').onclick=async()=>{if(await ask('Replace this show with its preceding save? Output stops.'))action(async()=>{await Api.recoverShow();if(!dialog.open)dialog.showModal();},'Preceding save restored');};
    dialog.querySelector('[data-reset]').onclick=async()=>{if(await ask(`Empty “${data.name}” only? Other shows and the Fixture Library stay. Output stops.`))action(async()=>{await Api.resetActiveShow();if(!dialog.open)dialog.showModal();},'Active show emptied; preceding save available for recovery');};
  }
  return {init,open,selection,ask};
})();
