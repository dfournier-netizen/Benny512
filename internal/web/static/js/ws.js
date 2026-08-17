// ws.js — WebSocket client with auto-reconnect and a tiny pub/sub so each
// screen module can subscribe to the message types it cares about without
// coupling to connection lifecycle.
const Live = (() => {
  let socket = null;
  let retryMs = 1000;
  const listeners = { node: [], rdm: [], capture: [], all: [] };

  function connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    socket = new WebSocket(`${proto}://${location.host}/ws`);

    socket.onopen = () => {
      retryMs = 1000;
      setStatus(true);
    };
    socket.onclose = () => {
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

  connect();
  return { on, send };
})();
