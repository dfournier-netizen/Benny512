package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/autoread"
	"benny512/internal/capture"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// The four datagrams 2.11.90.6 sent at 15:22:10 in RDM-LOG36
// (docs/evidence/captures/RDM-LOG36.txt): Art-Net framed as ArtRdm (opcode
// 0x8300) whose body is ArtPollReply content ("Port 1".."Port 4", NodeReport
// "#0001 [2186] RcPowerOk"). The RDM decoder rejects every one with
// "rdm: checksum mismatch". These are the literal bytes off the wire.
var log36MalformedArtRdm = []string{
	"4172742d4e6574000083000e010000000000000000000000012b506f727420314d50001159305600000000210060130100009b0509000000f0002a010100010000010651000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000640000000000000000000000000000000001000000000000000000000000000000280000000000000000000000",
	"4172742d4e6574000083000e010000000000000000000000012b506f727420320000000000000000000000000060130100009b0509000000f0002a010100010000010651000000000000000000000000000000000000000000000000000000000000000000000000000000002330303031205b323138365d205263506f7765724f6b00000000000000000000000000000000000000000000000000000000000000000000000000000000000000018000000008000000000000000c0000000c000000640000000000000000000000000000000002004000000000000000000000000000280000000000000000000000",
	"4172742d4e6574000083000e010000000000000000000000012b506f727420330000000000000000000000000060130100009b0509000000f0002a010100010000010651000000000000000000000000000000000000000000000000000000000000000000000000000000002330303031205b323138375d205263506f7765724f6b00000000000000000000000000000000000000000000000000000000000000000000000000000000000000018000000008000000000000000d0000000d000000640000000000000000000000000000000003004000000000000000000000000000280000000000000000000000",
	"4172742d4e6574000083000e010000000000000000000000012b506f727420340000000000000000000000000060130100009b0509000000f0002a010100010000010651000000000000000000000000000000000000000000000000000000000000000000000000000000002330303031205b323138385d205263506f7765724f6b00000000000000000000000000000000000000000000000000000000000000000000000000000000000000018000000008000000000000000d0000000d000000640000000000000000000000000000000004004000000000000000000000000000280000000000000000000000",
}

var log36Node = netip.MustParseAddrPort("2.11.90.6:6454")

func feedLog36Malformed(t *testing.T, h *testHarness, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		raw, err := hex.DecodeString(log36MalformedArtRdm[i%len(log36MalformedArtRdm)])
		if err != nil {
			t.Fatal(err)
		}
		h.rdmc.HandleInbound(session.Inbound{Data: raw, From: log36Node})
	}
}

func rdmNotes(h *testHarness) []string {
	var out []string
	for _, e := range h.srv.RDMCapture.Snapshot(capture.Filter{Kind: capture.KindNote}, 0) {
		out = append(out, e.Key)
	}
	return out
}

func seedPollReply(h *testHarness, ip [4]byte, bind byte, name string) {
	h.nodes.HandlePollReply(artnet.PollReply{
		IPAddress: ip, BindIndex: bind, ShortName: name, LongName: name,
		NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0}, Status1: 0x02,
	}, netip.AddrPortFrom(netip.AddrFrom4(ip), 6454))
}

// TestNoResponseLogNoteAndRecoveryReRead covers the RDM-LOG36 fixture
// (4D50:00115938, 123 requests, zero replies) from the web layer's side:
//
//   - the RDM log NOTE for a no-response pause must say what actually
//     happened — it went unanswered while its neighbours answered — not the
//     wireless-proxy sentence, which would send a tech to a radio link the
//     rig does not have;
//   - when it answers again, the recovery NOTE must not claim a proxy either;
//   - and its identity must be re-read: while it was paused the background
//     reader's passes failed fast and left it in gaveUp, so without a fresh
//     read a fixture that came back would stay an empty row forever.
//
// A proxy-refusal recovery keeps its original wording and does NOT re-read.
func TestNoResponseLogNoteAndRecoveryReRead(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.srv.AutoRead.Run(ctx)

	node := session.NodeRef{Key: session.NodeKey{IP: log36Node.Addr(), BindIndex: 1}}
	silent := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115938}
	proxied := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x00000001}
	keySilent := autoread.KeyFor(node.Key.IP, node.Key.BindIndex, node.Port, silent)
	keyProxied := autoread.KeyFor(node.Key.IP, node.Key.BindIndex, node.Port, proxied)

	// Drive the real reader to gaveUp for both: nothing on the fake wire
	// answers, so every pass times out on the fake clock.
	h.srv.AutoRead.Note(node, silent)
	h.srv.AutoRead.Note(node, proxied)
	deadline := time.Now().Add(20 * time.Second)
	for {
		a, _ := h.srv.AutoRead.State(keySilent)
		b, _ := h.srv.AutoRead.State(keyProxied)
		if a == autoread.StateGaveUp && b == autoread.StateGaveUp {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reader never gave up: silent=%q proxied=%q", a, b)
		}
		h.clock.Advance(100 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}

	at := time.Date(2026, 10, 6, 15, 22, 33, 0, time.UTC)
	retry := at.Add(15 * time.Second)
	unreachable := func(uid rdm.UID, due *session.DeviceUnreachableError) session.Event {
		return session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid, At: at,
			Result: &session.Result{Kind: session.ResultDeviceUnreachable, Err: due,
				Request: session.Request{Node: node, UID: uid, PID: rdm.PIDDeviceInfo}}}
	}
	ack := func(uid rdm.UID) session.Event {
		return session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid, At: retry,
			Result: &session.Result{Kind: session.ResultAck,
				Request: session.Request{Node: node, UID: uid, PID: rdm.PIDDeviceInfo}}}
	}

	// --- no-response: open, then a second suppressed command (deduped) ---
	due := &session.DeviceUnreachableError{UID: silent, Cause: session.CauseNoResponse, Silences: 3, Opens: 1, RetryAt: retry}
	h.srv.noteUnreachableToLog(unreachable(silent, due))
	h.srv.noteUnreachableToLog(unreachable(silent, due))
	notes := rdmNotes(h)
	if len(notes) != 1 {
		t.Fatalf("NOTEs after one no-response open = %d %q, want exactly 1", len(notes), notes)
	}
	want := "4D50:00115938 not answering — 3 requests in a row went unanswered while other devices on the same port answered. Benny512 has paused it so the rest of the port keeps running; next check at 15:22:48."
	if notes[0] != want {
		t.Errorf("no-response NOTE =\n  %q\nwant\n  %q", notes[0], want)
	}

	h.srv.noteUnreachableToLog(ack(silent))
	notes = rdmNotes(h)
	if len(notes) != 2 || notes[1] != "4D50:00115938 is answering again; resuming normally." {
		t.Errorf("no-response recovery NOTE = %q, want \"4D50:00115938 is answering again; resuming normally.\"", notes)
	}
	if st, n := h.srv.AutoRead.State(keySilent); st != autoread.StatePending && st != autoread.StateReading {
		t.Errorf("after a no-response recovery the reader state = %q (attempts %d), want pending/reading — the fixture's identity is never re-read", st, n)
	}

	// --- proxy refusal: wording unchanged, no re-read ---
	pdue := &session.DeviceUnreachableError{UID: proxied, Cause: session.CauseProxyRefusal, Refusals: 3, Opens: 1, RetryAt: retry}
	h.srv.noteUnreachableToLog(unreachable(proxied, pdue))
	h.srv.noteUnreachableToLog(ack(proxied))
	notes = rdmNotes(h)
	if len(notes) != 4 {
		t.Fatalf("NOTEs = %q, want 4", notes)
	}
	if !strings.Contains(notes[2], "not answering through its wireless proxy — 3 commands refused in a row (NACK PROXY_BUFFER_FULL)") {
		t.Errorf("proxy NOTE changed: %q", notes[2])
	}
	if notes[3] != "4C55:00000001 is answering again through its wireless proxy; resuming normally." {
		t.Errorf("proxy recovery NOTE changed: %q", notes[3])
	}
	if st, _ := h.srv.AutoRead.State(keyProxied); st != autoread.StateGaveUp {
		t.Errorf("proxy-refusal recovery changed reader state to %q; only no-response recoveries re-read", st)
	}
}

// TestNodesJSONCarriesNodeFault feeds RDM-LOG36's four malformed datagrams
// to the real controller and asks the real /api/nodes handler. Every
// binding of 2.11.90.6 must carry the fault; 2.11.90.5 beside it must not
// carry the key at all (absent = nothing recorded, not a zero count).
func TestNodesJSONCarriesNodeFault(t *testing.T) {
	h := newHarness(t)
	seedPollReply(h, [4]byte{2, 11, 90, 6}, 1, "EN4 port 1")
	seedPollReply(h, [4]byte{2, 11, 90, 6}, 2, "EN4 port 2")
	seedPollReply(h, [4]byte{2, 11, 90, 5}, 1, "Healthy")
	feedLog36Malformed(t, h, 4)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/nodes", nil)
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rr.Body.String())
	}
	seen := 0
	for _, n := range out {
		fault, has := n["fault"].(map[string]any)
		switch n["ip"] {
		case "2.11.90.6":
			seen++
			if !has {
				t.Errorf("node 2.11.90.6 bind %v has no fault: %v", n["bindIndex"], n)
				continue
			}
			if fault["malformed"] != float64(4) {
				t.Errorf("fault.malformed = %v, want 4", fault["malformed"])
			}
			if !strings.Contains(fmtAny(fault["lastError"]), "checksum mismatch") {
				t.Errorf("fault.lastError = %v, want the decoder's checksum error", fault["lastError"])
			}
			note := fmtAny(fault["note"])
			if !strings.HasPrefix(note, "This node has sent 4 malformed RDM packets (last at ") ||
				!strings.HasSuffix(note, "). That is the node's firmware or its own RDM handling, not a fixture — power-cycling or updating the node is the next step.") {
				t.Errorf("fault.note = %q", note)
			}
			for _, k := range []string{"first", "last"} {
				if _, ok := fault[k]; !ok {
					t.Errorf("fault.%s missing", k)
				}
			}
		case "2.11.90.5":
			seen++
			if _, present := n["fault"]; present {
				t.Errorf("healthy node carries a fault key: %v", n["fault"])
			}
		}
	}
	if seen != 3 {
		t.Fatalf("saw %d of 3 seeded node entries: %s", seen, rr.Body.String())
	}
}

// TestNodeFaultLogNoteIsDeduplicated: a node that sends malformed ArtRdm
// gets ONE NOTE in the RDM log, then at most one summary per 60 s carrying
// the count since the last NOTE. RDM-LOG36's node sent four inside one
// millisecond; one line per packet would bury the packets the log is for.
func TestNodeFaultLogNoteIsDeduplicated(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.srv.pumpRDMEvents(ctx)

	waitNotes := func(n int) []string {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if got := rdmNotes(h); len(got) >= n {
				// Give any further (wrong) NOTE a chance to land too.
				time.Sleep(100 * time.Millisecond)
				return rdmNotes(h)
			}
			time.Sleep(5 * time.Millisecond)
		}
		return rdmNotes(h)
	}

	feedLog36Malformed(t, h, 3)
	notes := waitNotes(1)
	if len(notes) != 1 {
		t.Fatalf("NOTEs after 3 malformed packets within 60 s = %d %q, want exactly 1", len(notes), notes)
	}
	if !strings.HasPrefix(notes[0], "Node 2.11.90.6 sent a malformed RDM packet (rdm: checksum mismatch") ||
		!strings.HasSuffix(notes[0], "). That is the node's own fault, not a fixture.") {
		t.Errorf("first NOTE = %q", notes[0])
	}

	h.clock.Advance(59 * time.Second)
	feedLog36Malformed(t, h, 1)
	if notes = waitNotes(2); len(notes) != 1 {
		t.Fatalf("NOTEs 59 s later = %q, want still 1", notes)
	}

	h.clock.Advance(2 * time.Second)
	feedLog36Malformed(t, h, 1)
	notes = waitNotes(2)
	if len(notes) != 2 {
		t.Fatalf("NOTEs after 61 s = %d %q, want 2 (one summary)", len(notes), notes)
	}
	// Since the first NOTE: two more in the first burst, one at +59 s, one now.
	if !strings.Contains(notes[1], "Node 2.11.90.6") || !strings.Contains(notes[1], "4 more malformed RDM packets") {
		t.Errorf("summary NOTE = %q, want it to name the node and carry the count 4", notes[1])
	}
}

// TestRDMDiagnosticsCarriesMalformedAndForeign: the two new controller
// counters reach /api/diagnostics/rdm, and a zero stays on the wire.
func TestRDMDiagnosticsCarriesMalformedAndForeign(t *testing.T) {
	h := newHarness(t)
	read := func() map[string]any {
		rr := doJSON(t, h.srv.Handler(), "GET", "/api/diagnostics/rdm", nil)
		var m map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}
	m := read()
	for _, k := range []string{"malformedRdm", "foreignResponses"} {
		if v, ok := m[k]; !ok || v != float64(0) {
			t.Errorf("%s = %v (present %v), want an explicit 0", k, v, ok)
		}
	}
	feedLog36Malformed(t, h, 4)
	if v := read()["malformedRdm"]; v != float64(4) {
		t.Errorf("malformedRdm = %v after LOG36's four datagrams, want 4", v)
	}
}

// TestFixturesJSONStatesNoResponseCause drives the real controller with
// RDM-LOG36's shape — three fixtures on one node port, one of which never
// answers — until the controller stops asking the silent one, then reads the
// real /api/fixtures handler. The row must say "not answering" with the
// no-response cause, and must not send a tech looking for a wireless proxy.
func TestFixturesJSONStatesNoResponseCause(t *testing.T) {
	h := newHarness(t)
	node := session.NodeRef{Key: session.NodeKey{IP: log36Node.Addr(), BindIndex: 1}}
	silent := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115938}
	healthy := []rdm.UID{{ManufacturerID: 0x4D50, DeviceID: 0x00115908}, {ManufacturerID: 0x4D50, DeviceID: 0x00115930}}

	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID == silent {
			return
		}
		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: msg.DestinationUID,
			TransactionNumber: msg.TransactionNumber, SubDevice: msg.SubDevice,
			CommandClass: rdm.GetCommandResponse, ParameterID: msg.ParameterID,
			PortIDOrResponseType: byte(rdm.ResponseACK), ParameterData: []byte("ERA 800"),
		}
		h.clock.AfterFunc(time.Millisecond, func() { h.rdmc.HandleRDMResponse(resp) })
	}
	await := func(uid rdm.UID) session.Result {
		t.Helper()
		cmd := h.rdmc.Get(node, uid, rdm.PIDDeviceModelDescription, nil)
		done := make(chan session.Result, 1)
		go func() {
			res, _ := cmd.Await(context.Background())
			done <- res
		}()
		deadline := time.Now().Add(10 * time.Second)
		for {
			select {
			case res := <-done:
				return res
			case <-time.After(time.Millisecond):
			}
			h.clock.Advance(20 * time.Millisecond)
			if time.Now().After(deadline) {
				t.Fatalf("GET to %s never completed", uid)
			}
		}
	}

	opened := false
	for round := 0; round < 30 && !opened; round++ {
		if res := await(silent); res.Kind == session.ResultDeviceUnreachable {
			opened = true
			break
		}
		for _, u := range healthy {
			if res := await(u); res.Kind != session.ResultAck {
				t.Fatalf("healthy %s: %v (%v)", u, res.Kind, res.Err)
			}
		}
	}
	if !opened {
		t.Fatal("the controller never stopped asking the silent fixture")
	}

	var rows []map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for {
		rr := doJSON(t, h.srv.Handler(), "GET", "/api/fixtures", nil)
		rows = nil
		if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		ready := false
		for _, r := range rows {
			if r["uid"] == silent.String() && r["unreachable"] == true {
				ready = true
			}
		}
		if ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	const want = "Not answering RDM. Other fixtures on the same port are answering, so the line is working. Benny512 has paused this one so the rest of the rig keeps running, and will check it again automatically."
	found := false
	for _, r := range rows {
		switch r["uid"] {
		case silent.String():
			found = true
			if r["unreachable"] != true {
				t.Errorf("silent fixture unreachable = %v", r["unreachable"])
			}
			if r["unreachableCause"] != "no-response" {
				t.Errorf("unreachableCause = %v, want \"no-response\"", r["unreachableCause"])
			}
			if r["unreachableNote"] != want {
				t.Errorf("unreachableNote = %q, want %q", r["unreachableNote"], want)
			}
		case healthy[0].String(), healthy[1].String():
			if r["unreachable"] != false {
				t.Errorf("healthy %v unreachable = %v", r["uid"], r["unreachable"])
			}
			if _, has := r["unreachableCause"]; has {
				t.Errorf("healthy %v carries unreachableCause %v; it must be absent when reachable", r["uid"], r["unreachableCause"])
			}
		}
	}
	if !found {
		t.Fatalf("silent fixture missing from /api/fixtures: %v", rows)
	}
}

func fmtAny(v any) string {
	s, _ := v.(string)
	return s
}
