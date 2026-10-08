// Shared show tools, and the persistent strip with the MASTER OUTPUT control.
//
// MASTER ARM / DISARM (C3, owner decisions 2026-10-06/07). One control,
// always in the strip on every screen, governs ALL DMX data Benny512 sends.
// Its state is a word plus an icon, never colour alone: Disarmed, Armed,
// Lease lost — holding, Simulated, Output error. ARM is the confirm step
// (one press, no dialog); DISARM and Stop all output black out.
//
// THE LEASE. Every open page heartbeats POST /api/output/heartbeat every
// second with its own client id, and that is what keeps an armed output
// alive: any connected browser will do (a laptop and a phone together), and
// the lease is lost only when every page has been silent for 5 s. Leaving a
// screen does nothing to output. Unloading the page sends a keepalive
// goodbye: the server drops this browser at once, and if no other browser is
// connected the lease is lost then (Blackout or Hold per Settings) instead of
// 5 s later.
const Workspace = (() => {
  let data = null, selected = [], dialog = null, loading = false;
  // CLIENT: this page's id for the lease. crypto.randomUUID is only present
  // in secure contexts and Benny512 is served over plain http on the LAN, so
  // fall back to getRandomValues (available everywhere), then Math.random.
  const CLIENT = (() => {
    const c = (typeof crypto !== 'undefined') ? crypto : null;
    if (c && typeof c.randomUUID === 'function') return c.randomUUID();
    if (c && typeof c.getRandomValues === 'function') {
      const b = new Uint8Array(12); c.getRandomValues(b);
      return Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
    }
    return Date.now().toString(36) + Math.random().toString(36).slice(2);
  })();
  let out = null; // last /api/output status; null = not heard from the server
  let outBusy = false;
  const HEARTBEAT_MS = 1000;
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
    } catch (_) { /* the heartbeat reports a lost connection */ }
  }
  // outputWords: the strip's state in words, and the icon and button that go
  // with it. Disarmed/holding offer ARM; armed offers DISARM.
  function outputWords(o) {
    if (!o) return { word: 'Connection lost — output unknown', icon: 'status-warning', tone: 'warn', arm: true };
    const sim = o.simulated ? 'Simulated · ' : '';
    if (o.state === 'armed') {
      if (o.error) return { word: sim + 'Output error · Armed', icon: 'status-error', tone: 'error', arm: false };
      return { word: sim + 'Armed', icon: 'status-ok', tone: 'ok', arm: false };
    }
    if (o.state === 'holding') return { word: sim + 'Lease lost — holding', icon: 'status-warning', tone: 'warn', arm: true };
    return { word: sim + 'Disarmed' + (o.lastDisarm === 'lease' ? ' · browser heartbeat lost' : ''), icon: 'status-pending', tone: 'off', arm: true };
  }
  function renderOutput() {
    // G3: the fader bar's disarmed hint follows this heartbeat (no second poll).
    window.dispatchEvent(new CustomEvent('b5-output', { detail: out }));
    const root = document.getElementById('showContext');
    const box = root && root.querySelector('[data-outbox]');
    if (!box) return;
    const w = outputWords(out);
    const title = out && out.error ? esc(out.error) : (out && out.state === 'holding' ? 'Every browser went silent for 5 s. The last look is still being sent; changes do not reach the rig until someone presses ARM.' : '');
    box.innerHTML = `<span data-output role="status" class="b5-out-state b5-out-state--${w.tone}" title="${title}">${UI.icon(w.icon)}${esc(w.word)}</span>` +
      (w.arm
        ? `<button type="button" class="b5-btn b5-btn--sm b5-out-arm b5-out-arm--go" data-arm aria-label="ARM: let DMX output reach the rig">${UI.icon('status-ok')}ARM</button>`
        : `<button type="button" class="b5-btn b5-btn--sm b5-out-arm b5-out-arm--stop" data-arm aria-label="DISARM: black out and stop all DMX output">${UI.icon('status-error')}DISARM</button>`);
    const btn = box.querySelector('[data-arm]');
    if (btn) {
      btn.disabled = outBusy;
      btn.onclick = async () => {
        if (outBusy) return;
        outBusy = true;
        try { out = await (w.arm ? Api.outputArm(CLIENT) : Api.outputDisarm(CLIENT)); }
        catch (e) { out = null; }
        finally { outBusy = false; renderOutput(); }
      };
    }
  }
  async function heartbeat() {
    try { out = await Api.outputHeartbeat(CLIENT); } catch (_) { out = null; }
    renderOutput();
  }
  function init() {
    const root = document.getElementById('showContext');
    root.innerHTML = `<strong data-show class="b5-show-context__name">Loading show…</strong><span data-nic class="b5-show-context__nic b5-text-sm"></span><span data-outbox class="b5-out"></span><button class="b5-btn b5-btn--sm" data-tools>Show tools</button><button class="b5-btn b5-btn--sm" data-library>Fixture library</button><button class="b5-btn b5-btn--sm b5-btn--danger" data-stop>Stop all output</button>`;
    root.querySelector('[data-tools]').onclick = open;
    root.querySelector('[data-library]').onclick = LibraryPanel.open;
    // Stop all output = DISARM (blackout), from any screen.
    root.querySelector('[data-stop]').onclick = async () => {
      try { await Api.stopAllOutput(); } catch (_) { /* the heartbeat shows the truth */ }
      await heartbeat();
    };
    window.addEventListener('b5-show-changed', () => { data=null; selection([]); context(); if(dialog && dialog.open) dialog.close(); });
    window.addEventListener('b5-patch-selection', e => selection(e.detail));
    window.addEventListener('b5-device-selection', async e => {
      try { const w=await Api.getWorkspace(); selection(w.entries.filter(x=>x.uid===e.detail).map(x=>x.id)); } catch (_) {}
    });
    renderOutput();
    context(); setInterval(context, 2000);
    heartbeat(); setInterval(heartbeat, HEARTBEAT_MS);
    window.addEventListener('pagehide', () => Api.outputGoodbyeBeacon(CLIENT));
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
    } catch(e) {const o=document.querySelector('[data-output]'); if(o) o.textContent=e.message;}
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
      <div class="b5-row"><button class="b5-btn" data-save-group>Save selection as group</button><button class="b5-btn" data-test-selection>Use selection in the Console</button></div>
      <div class="b5-row">${data.groups.map((g,i)=>`<span><button class="b5-btn" data-group="${i}">${esc(g.name)} · ${g.entryIds.length}</button><button class="b5-btn b5-btn--sm" data-delete-group="${i}" aria-label="Delete group ${esc(g.name)}">Delete</button></span>`).join('')}</div>
      <div class="b5-workspace-list">${data.entries.map(e=>`<div class="b5-workspace-item"><label><input type="checkbox" data-entry="${esc(e.id)}" ${selected.includes(e.id)?'checked':''}>${esc(e.name||e.id)} · U${UI.formatUser(e.universe)} / ${e.startAddress}</label><span class="b5-caption">${e.phaseCount} phase slots · ${esc(e.phaseSource)}</span></div>`).join('')}</div></details>
      <details><summary>Test presets · ${data.presets.length}</summary><button class="b5-btn" data-save-preset>Save current function tests</button><p class="b5-caption">Loads the tests and exact fixture selection. They are live at once and reach the rig only while output is Armed.</p>
      <div class="b5-row">${data.presets.map((p,i)=>`<span><button class="b5-btn" data-preset="${i}">Load ${esc(p.name)}</button><button class="b5-btn b5-btn--sm" data-delete-preset="${i}" aria-label="Delete preset ${esc(p.name)}">Delete</button></span>`).join('')}</div></details>
      <details><summary>Rig baselines · ${data.baselines.length}</summary><button class="b5-btn" data-snapshot>Record baseline…</button><p class="b5-caption">Records the patch, cached fixture configuration, and open issues.</p>
      <div class="b5-workspace-list">${data.baselines.map((b,i)=>`<div class="b5-workspace-item"><strong>${esc(b.name)}</strong> · ${esc(new Date(b.at).toLocaleString())} · ${b.issues} issues at capture <button class="b5-btn b5-btn--sm" data-report="${i}">Compare now</button><a class="b5-btn b5-btn--sm" href="/api/patch/workspace/report/${encodeURIComponent(b.id)}?format=txt">Export report</a><button class="b5-btn b5-btn--sm" data-delete-baseline="${i}" aria-label="Delete baseline ${esc(b.name)}">Delete</button></div>`).join('')}</div><div data-report-body></div></details>
      ${data.canRehearse ? `<details><summary>Offline rehearsal</summary><label>Scenario <select class="b5-select" data-fault><option value="none">As patched</option><option value="missing">Missing fixtures</option><option value="address">Wrong addresses</option><option value="slow">Delayed RDM responses</option></select></label><button class="b5-btn" data-rehearse>Build rehearsal…</button><p class="b5-caption">Disarms live output (blackout). Opens an in-memory rig on fake lighting transport. Replaces any earlier rehearsal.</p><div data-rehearsal-link></div></details>` : ''}
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
    // C7: the Console replaces the retired Function check. The selection
    // becomes the programmer selection (one per station), which the Console's
    // Tests panel acts on when its scope is "Selection".
    dialog.querySelector('[data-test-selection]').onclick=()=>action(async()=>{
      if(!selected.length)throw new Error('Select at least one fixture.');
      await ProgrammerSync.act('select',{action:'set',targets:selected.map(id=>({entryId:id,cell:''}))});
      dialog.close();window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'console'}));
    },'Selection ready');
    dialog.querySelector('[data-save-preset]').onclick=()=>named('save-preset','Test preset saved');
    dialog.querySelectorAll('[data-preset]').forEach(b=>b.onclick=()=>action(async()=>{
      const p=data.presets[Number(b.dataset.preset)];await Api.workspaceAction('load-preset',{id:p.id});selection(p.entryIds);
      dialog.close();window.dispatchEvent(new CustomEvent('b5-navigate',{detail:'console'}));
    },'Preset loaded'));
    dialog.querySelector('[data-snapshot]').onclick=()=>named('snapshot','Baseline recorded');
    const rehearsal=dialog.querySelector('[data-rehearse]');
    if(rehearsal) rehearsal.onclick=async()=>{
      if(!await ask('Disarm live output and build a separate simulated copy of this show?'))return;
      rehearsal.disabled=true;
      try {
        const result=await Api.workspaceAction('rehearse',{confirm:'REHEARSE',fault:dialog.querySelector('[data-fault]').value});
        const url=new URL(location.href);url.port=String(result.port);url.pathname='/';url.search='';url.hash='';
        dialog.querySelector('[data-rehearsal-link]').innerHTML=`<a class="b5-btn b5-btn--primary" href="${esc(url.href)}" target="_blank" rel="noopener">Open rehearsal</a>`;
        status('Rehearsal ready. Live output is disarmed.');await context();
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
