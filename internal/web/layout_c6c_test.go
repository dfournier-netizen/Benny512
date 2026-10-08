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
// server-to-client text frames.
type wsTestClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialTestWS(t *testing.T, ts *httptest.Server) *wsTestClient {
	t.Helper()
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
	return &wsTestClient{conn: conn, r: r}
}

// next returns the next text message, or "" after the deadline.
func (c *wsTestClient) next(deadline time.Time) string {
	c.conn.SetReadDeadline(deadline)
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
			return ""
		}
		n := uint64(hdr[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return ""
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return ""
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return ""
		}
		if hdr[0]&0x0f == 0x1 {
			return string(payload)
		}
	}
}

// layoutMessages collects every {"type":"layout"} revision that arrives
// within d (other message types — capture, node — are skipped).
func (c *wsTestClient) layoutMessages(d time.Duration) []uint64 {
	end := time.Now().Add(d)
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
		if json.Unmarshal([]byte(m), &msg) == nil && msg.Type == "layout" {
			if msg.Revision == nil {
				out = append(out, 0)
			} else {
				out = append(out, *msg.Revision)
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
	ws := dialTestWS(t, ts)

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
	if got := ws.layoutMessages(300 * time.Millisecond); len(got) != 0 {
		t.Errorf("a refused layout action broadcast %v; it must broadcast nothing", got)
	}
	code, raw, g1 := c6cDo(t, ts, "GET", "/api/patch/layout", "")
	if code != http.StatusOK || g1.Revision == nil || *g1.Revision != want {
		t.Errorf("GET after the refusal: %.200s, want revision %d", raw, want)
	}
	if hdr := strings.SplitN(raw, "|", 2)[0]; hdr != strconv.FormatUint(want, 10) {
		t.Errorf("X-Benny-Layout header %q, want %d", hdr, want)
	}
}
