package web

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Console-lite C6c — live layout sync. Every successful layout mutation
// bumps the layout revision and broadcasts {"type":"layout","revision":N}
// on the existing WebSocket hub; GET /api/patch/layout carries the revision
// (JSON "revision" and the X-Benny-Layout header). Proven with a real
// WebSocket client on the wire (hand-rolled RFC 6455 handshake and frame
// reader, zero dependencies) against the real server over real HTTP.

// wsTestClient is a minimal RFC 6455 client: handshake, then unmasked
// server-to-client text frames. One goroutine reads frames with no read
// deadline and queues the text messages, so a wait that ends never cuts a
// frame in half (a read deadline expiring mid-frame would desynchronise
// the stream for every later read).
type wsTestClient struct {
	conn net.Conn
	r    *bufio.Reader
	msgs chan string
}

// hubClients is how many WebSocket connections the server's hub holds.
func hubClients(s *Server) int {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return len(s.hub.clients)
}

func dialTestWS(t *testing.T, ts *httptest.Server, srv *Server) *wsTestClient {
	t.Helper()
	before := hubClients(srv)
	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", addr, key)
	r := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("WebSocket handshake: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("WebSocket handshake: %d accept %q", resp.StatusCode, resp.Header.Get("Sec-WebSocket-Accept"))
	}
	conn.SetReadDeadline(time.Time{})
	c := &wsTestClient{conn: conn, r: r, msgs: make(chan string, 256)}
	go c.readLoop()
	// handleWS writes the 101 answer (ws.Upgrade) BEFORE it registers the
	// connection with the hub (s.hub.add), so a broadcast sent right after
	// the handshake can reach the hub first and miss this client — the
	// "WebSocket layout messages [], want exactly [...]" failure under
	// load. Wait for the registration itself.
	end := time.Now().Add(5 * time.Second)
	for hubClients(srv) <= before {
		if time.Now().After(end) {
			t.Fatalf("the server never registered the WebSocket connection with its hub")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return c
}

func (c *wsTestClient) readLoop() {
	defer close(c.msgs)
	for {
		m, ok := c.readFrame()
		if !ok {
			return
		}
		if m != "" {
			c.msgs <- m
		}
	}
}

// next returns the next text message, or "" after the deadline.
func (c *wsTestClient) next(deadline time.Time) string {
	select {
	case m, ok := <-c.msgs:
		if !ok {
			return ""
		}
		return m
	case <-time.After(time.Until(deadline)):
		return ""
	}
}

// readFrame reads one frame: its text, "" for a non-text frame, ok false
// when the connection is gone.
func (c *wsTestClient) readFrame() (string, bool) {
	{
		var hdr [2]byte
		if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
			return "", false
		}
		n := uint64(hdr[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return "", false
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return "", false
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return "", false
		}
		if hdr[0]&0x0f == 0x1 {
			return string(payload), true
		}
		return "", true
	}
}

// layoutMessages waits up to wsFirstWait for the first {"type":"layout"}
// message, then keeps collecting for settle, so "exactly one" still
// catches a second, duplicate broadcast (other message types — capture,
// node — are skipped).
func (c *wsTestClient) layoutMessages(settle time.Duration) []uint64 {
	return c.typedMessages("layout", settle, false)
}

// wsFirstWait bounds the wait for an expected broadcast. It is a deadline,
// not a delay: the wait ends as soon as the message arrives.
const wsFirstWait = 10 * time.Second

// typedMessages collects the revisions of every message of type typ: it
// waits up to wsFirstWait for the first, then collects for settle. With
// needRevision, a message without a revision is skipped; otherwise it is
// recorded as 0.
func (c *wsTestClient) typedMessages(typ string, settle time.Duration, needRevision bool) []uint64 {
	end := time.Now().Add(wsFirstWait)
	out := make([]uint64, 0)
	for time.Now().Before(end) {
		m := c.next(end)
		if m == "" {
			break
		}
		var msg struct {
			Type     string  `json:"type"`
			Revision *uint64 `json:"revision"`
		}
		if json.Unmarshal([]byte(m), &msg) == nil && msg.Type == typ {
			if msg.Revision != nil {
				out = append(out, *msg.Revision)
			} else if !needRevision {
				out = append(out, 0)
			} else {
				continue
			}
			if len(out) == 1 {
				end = time.Now().Add(settle)
			}
		}
	}
	return out
}

type c6cLayout struct {
	Revision *uint64 `json:"revision"`
	Layout   struct {
		Layers []struct{ ID, Name string }
	}
}

func c6cDo(t *testing.T, ts *httptest.Server, method, path, body string) (int, string, c6cLayout) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var v c6cLayout
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return res.StatusCode, res.Header.Get("X-Benny-Layout") + "|" + strings.TrimSpace(string(b)), v
}

func TestLayoutLiveSync_RevisionBroadcastOnEveryMutation(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		map[string]any{"name": "G1", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 1, "startAddress": 1},
		map[string]any{"name": "G2", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 1, "startAddress": 5},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	ws := dialTestWS(t, ts, h.srv)

	code, raw, g0 := c6cDo(t, ts, "GET", "/api/patch/layout", "")
	if code != http.StatusOK || g0.Revision == nil {
		t.Fatalf("GET /api/patch/layout carries no revision: %d %.300s", code, raw)
	}
	r0 := *g0.Revision
	if hdr := strings.SplitN(raw, "|", 2)[0]; hdr != strconv.FormatUint(r0, 10) {
		t.Fatalf("X-Benny-Layout header %q does not carry revision %d", hdr, r0)
	}

	// Three successful mutations: each bumps the revision by one, answers
	// with it, and broadcasts exactly it.
	steps := []struct{ action, body string }{
		{"layer-create", `{"name":"Truss 1"}`},
		{"layer-create", `{"name":"Floor"}`},
	}
	want := r0
	for _, s := range steps {
		code, raw, v := c6cDo(t, ts, "POST", "/api/patch/layout/"+s.action, s.body)
		if code != http.StatusOK || v.Revision == nil {
			t.Fatalf("POST %s: %d %.300s (no revision in the answer)", s.action, code, raw)
		}
		want++
		if *v.Revision != want {
			t.Errorf("POST %s answered revision %d, want %d (previous + 1)", s.action, *v.Revision, want)
		}
		got := ws.layoutMessages(500 * time.Millisecond)
		if len(got) != 1 || got[0] != want {
			t.Errorf("POST %s: WebSocket layout messages %v, want exactly [%d]", s.action, got, want)
		}
	}
	_, _, cur := c6cDo(t, ts, "GET", "/api/patch/layout", "")
	var floor string
	for _, l := range cur.Layout.Layers {
		if l.Name == "Floor" {
			floor = l.ID
		}
	}
	code, raw, v := c6cDo(t, ts, "POST", "/api/patch/layout/layer-rename", `{"id":"`+floor+`","name":"Deck"}`)
	want++
	if code != http.StatusOK || v.Revision == nil || *v.Revision != want {
		t.Errorf("layer-rename: %d %.200s, want revision %d", code, raw, want)
	}
	if got := ws.layoutMessages(500 * time.Millisecond); len(got) != 1 || got[0] != want {
		t.Errorf("layer-rename: WebSocket layout messages %v, want exactly [%d]", got, want)
	}

	// A refused mutation changes nothing: no bump, no broadcast.
	if code, raw, _ := c6cDo(t, ts, "POST", "/api/patch/layout/layer-rename", `{"id":"no-such-layer","name":"X"}`); code == http.StatusOK {
		t.Fatalf("renaming a missing layer was accepted: %.200s", raw)
	}
	code, raw, g1 := c6cDo(t, ts, "GET", "/api/patch/layout", "")
	if code != http.StatusOK || g1.Revision == nil || *g1.Revision != want {
		t.Errorf("GET after the refusal: %.200s, want revision %d", raw, want)
	}
	if hdr := strings.SplitN(raw, "|", 2)[0]; hdr != strconv.FormatUint(want, 10) {
		t.Errorf("X-Benny-Layout header %q, want %d", hdr, want)
	}
	// No time window can prove a broadcast never comes. The hub delivers
	// one connection's messages in order, so the next successful mutation
	// proves it: had the refusal broadcast anything, it would arrive
	// before that mutation's own message.
	code, raw, v = c6cDo(t, ts, "POST", "/api/patch/layout/layer-rename", `{"id":"`+floor+`","name":"Floor"}`)
	want++
	if code != http.StatusOK || v.Revision == nil || *v.Revision != want {
		t.Errorf("layer-rename after the refusal: %d %.200s, want revision %d", code, raw, want)
	}
	if got := ws.layoutMessages(500 * time.Millisecond); len(got) != 1 || got[0] != want {
		t.Errorf("after a refused layout action the WebSocket carried %v before the next mutation's [%d]; a refusal must broadcast nothing", got, want)
	}
}
