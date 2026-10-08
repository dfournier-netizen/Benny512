// programmer.js — live-sync plumbing for the Console-lite programmer (C4a).
// No screen of its own (that is C6): it keeps this browser's copy of
// GET /api/programmer current and sends every mutation with the revision
// this browser last saw.
//
// One programmer serves every browser (owner decision 2026-10-07). Every
// mutation on the server broadcasts {"type":"programmer","revision":N}; a
// browser whose revision differs re-reads the state. A mutation sent with a
// revision another browser has already moved past is refused (409): the
// state is re-read and the error is passed on, so the caller decides whether
// to retry on the fresh view. Mutations from this browser are sent one at a
// time, so a fader drag never races its own previous step.
const ProgrammerSync = (() => {
  let state = null;     // last GET /api/programmer body
  let known = null;     // newest revision this browser has caught up to
  let running = null;   // the refresh in flight
  let again = false;    // a newer revision was announced during it
  let chain = Promise.resolve();
  const subs = [];

  function notify() {
    subs.forEach(fn => {
      try { fn(state); } catch (e) { console.error('programmer subscriber', e); }
    });
  }

  // refresh re-reads the state; calls arriving while one is in flight are
  // folded into one more read after it, so the last read is never older
  // than the last announcement.
  function refresh() {
    if (running) {
      again = true;
      return running;
    }
    running = (async () => {
      try {
        let reads = 0;
        do {
          again = false;
          const s = await Api.getProgrammer();
          reads++;
          // A read that left before one of this browser's own writes landed
          // carries an older revision than that write's answer: adopting it
          // would make the next write stale (409). Read again instead (the
          // server's revision only grows); give up waiting after 3 reads.
          if (known !== null && s.revision < known && reads < 3) { again = true; continue; }
          state = s;
          known = s.revision;
        } while (again);
        notify();
        return state;
      } finally {
        running = null;
      }
    })();
    return running;
  }

  // act posts one mutation ('select' | 'set' | 'clear' | 'raw').
  function act(action, body) {
    const run = async () => {
      try {
        const res = await Api.programmerAction(action, body, known);
        // Our write moved the revision; the server's answer is the new one,
        // so the next queued write carries it even before the re-read lands.
        if (res && typeof res.revision === 'number') known = res.revision;
        refresh();
        return res;
      } catch (e) {
        await refresh();
        throw e;
      }
    };
    const p = chain.then(run, run);
    chain = p.catch(() => {});
    return p;
  }

  function onChange(fn) { subs.push(fn); }

  Live.on('programmer', msg => {
    if (msg.revision !== known) refresh().catch(() => {});
  });
  // A reconnect may have missed broadcasts: catch up.
  Live.on('connected', () => { refresh().catch(() => {}); });

  return { refresh, act, onChange, state: () => state, revision: () => known };
})();
