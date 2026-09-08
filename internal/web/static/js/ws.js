// ws.js — WebSocket client with auto-reconnect and a tiny pub/sub so each
// screen module can subscribe to the message types it cares about without
// coupling to connection lifecycle.
const Live = (() => {
  let socket = null;
  let retryMs = 1000;
  // shuttingDown suppresses the normal "connection lost, retrying…" churn
  // for exactly one case: a full reset (settings.js) intentionally ends the
  // server process right after its 200 response. Every other disconnect
  // (network blip, node restart, the tech closing their laptop lid) still
  // gets the ordinary reconnect-with-backoff behavior below — this flag is
  // set only by the one call site that knows the process is gone on
  // purpose (see enterShutdown/settings.js's doFullReset).
  let shuttingDown = false;
  const listeners = { node: [], rdm: [], capture: [], all: [] };

  function connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    socket = new WebSocket(`${proto}://${location.host}/ws`);

    socket.onopen = () => {
      retryMs = 1000;
      setStatus(true);
      (listeners.connected || []).forEach(fn => fn({type: 'connected'}));
    };
    socket.onclose = () => {
      if (shuttingDown) return;
      setStatus(false);
      setTimeout(connect, retryMs);
      retryMs = Math.min(retryMs * 1.5, 15000);
    };
    socket.onerror = () => { socket.close(); };
    socket.onmessage = (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      (listeners[msg.type] || []).forEach(fn => fn(msg));
      listeners.all.forEach(fn => fn(msg));
    };
  }

  // setStatus renders the b5-connection pill (dot + text) — status is never
  // color-alone: the dot's color is paired with the text label, and the
  // element's own class carries the state name too (design-spec "Do":
  // never rely on color alone).
  function setStatus(ok) {
    const el = document.getElementById('connStatus');
    if (!el) return;
    el.className = 'b5-connection' + (ok ? '' : ' b5-connection--offline');
    el.innerHTML = '<span class="b5-connection__dot"></span>' + (ok ? 'Connected' : 'Reconnecting…');
  }

  function on(type, fn) {
    (listeners[type] || (listeners[type] = [])).push(fn);
  }

  // send delivers a client->server control message (currently
  // subscribe_sensors/unsubscribe_sensors — see wsClientMessage in
  // internal/web/server.go). No-op while disconnected/reconnecting; the
  // sensors panel re-subscribes on next open via its own effect, so a
  // dropped subscribe during a reconnect window self-heals rather than
  // needing a queue here.
  function send(obj) {
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(obj));
    }
  }

  // enterShutdown is called once, right after a full-reset 200 response
  // (settings.js) — the server is exiting on purpose, so stop trying to
  // reconnect and stop touching #connStatus; the caller replaces the whole
  // UI with its own terminal end-state instead. Deliberately does NOT call
  // socket.close() itself: the server process is already tearing down
  // (resetShutdownDelay has all but elapsed by the time the 200 response
  // reaches the browser) and will drop the TCP connection on its own in a
  // few hundred ms, which fires the ordinary onclose path above (a no-op
  // once shuttingDown is set). Proactively closing here raced the server's
  // own close and made Chrome log a spurious "WebSocket ... Close received
  // after close" console error for a connection that was about to die
  // cleanly either way.
  function enterShutdown() {
    shuttingDown = true;
  }

  connect();
  return { on, send, enterShutdown };
})();
