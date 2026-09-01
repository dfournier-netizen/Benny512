package web

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// runHTTPAsyncNoRDM is like runHTTPAsync but for handlers that only touch
// ArtNetSession (node config), not the RDM controller — the confirmation
// path there is driven by tport.OnSend directly re-entering
// h.nodes.HandlePollReply, which (per session/nodeconfig_test.go's
// TestSetNodeNamesConfirmedViaOnSend) is safe to do synchronously since
// ArtNetSession doesn't hold its own mutex across Transport.Send. No clock
// advance is needed for the confirmed path; the timeout path still needs
// one, handled per-test.
func (h *testHarness) runHTTPAsyncNoRDM(t *testing.T, method, path string, body any) *responseRecorderResult {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), method, path, body)
	return &responseRecorderResult{Code: rr.Code, Body: rr.Body.Bytes()}
}

type responseRecorderResult struct {
	Code int
	Body []byte
}

func TestNodeAddressEndpoint(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)

	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.Packet.Kind != artnet.KindAddress {
			return
		}
		addr := netip.MustParseAddr("10.0.0.5")
		h.nodes.HandlePollReply(artnet.PollReply{
			IPAddress: addr.As4(), ShortName: sp.Packet.Address.ShortName, LongName: sp.Packet.Address.LongName,
			NumPorts: 1, PortTypes: [4]byte{0x80}, Status1: 0x02,
		}, netip.AddrPortFrom(addr, session.ArtNetUDPPort))
	}

	body := map[string]any{
		"shortName": "New EN4",
		"netSwitch": 5,
		"swOut":     [4]any{2, nil, nil, nil},
		"command":   "merge_htp_0",
	}
	rr := h.runHTTPAsyncNoRDM(t, "POST", "/api/node/10.0.0.5/address", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
	}
	var got nodeConfigResultJSON
	if err := json.Unmarshal(rr.Body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Confirmed {
		t.Fatalf("expected Confirmed true: %+v", got)
	}
}

func TestNodeAddressUnknownCommand(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)
	rr := h.runHTTPAsyncNoRDM(t, "POST", "/api/node/10.0.0.5/address", map[string]any{"command": "not_a_real_command"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
	}
}

func TestNodeAddressUnknownNode(t *testing.T) {
	h := newHarness(t)
	rr := h.runHTTPAsyncNoRDM(t, "POST", "/api/node/10.0.0.99/address", map[string]any{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
	}
}

func TestNodeIPConfigEndpoint(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)
	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProgReply && sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		addr := netip.MustParseAddr("10.0.0.5")
		// Deliver an ArtIpProgReply directly via the inbound path.
		reply := artnet.Packet{Kind: artnet.KindIpProgReply, IpProgReply: artnet.IpProgReply{CurrentIP: [4]byte{10, 0, 0, 60}}}
		h.nodes.HandleInbound(session.Inbound{Data: artnet.Encode(reply), From: netip.AddrPortFrom(addr, session.ArtNetUDPPort)})
	}

	body := map[string]any{"ip": "10.0.0.60", "mask": "255.255.255.0", "gateway": "10.0.0.1", "dhcp": false}
	rr := h.runHTTPAsyncNoRDM(t, "POST", "/api/node/10.0.0.5/ipconfig", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
	}
	var got nodeConfigResultJSON
	if err := json.Unmarshal(rr.Body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Confirmed {
		t.Fatalf("expected Confirmed true: %+v", got)
	}
	if got.Warning == "" {
		t.Fatal("expected gateway-not-sent warning")
	}
}

func TestNodeInputEndpoint(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)
	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.Packet.Kind != artnet.KindInput {
			return
		}
		addr := netip.MustParseAddr("10.0.0.5")
		h.nodes.HandlePollReply(artnet.PollReply{IPAddress: addr.As4(), NumPorts: 1, PortTypes: [4]byte{0x80}}, netip.AddrPortFrom(addr, session.ArtNetUDPPort))
	}
	rr := h.runHTTPAsyncNoRDM(t, "POST", "/api/node/10.0.0.5/input", map[string]any{"enabled": [4]bool{true, true, false, false}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
	}
}

func TestNodeConfigTimeoutPath(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)
	// No OnSend confirmation hook: node never replies.
	done := make(chan *responseRecorderResult, 1)
	go func() {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/node/10.0.0.5/input", map[string]any{"enabled": [4]bool{true, true, true, true}})
		done <- &responseRecorderResult{Code: rr.Code, Body: rr.Body.Bytes()}
	}()
	// Driven by real, blocking synchronization, not a spin loop bounded by
	// an iteration count — see runHTTPAsync/pumpUntilDone's doc comment in
	// device_test.go for the full story: a fixed-count Gosched spin can
	// exhaust its whole budget in low-single-digit milliseconds of real
	// CPU time, starving the handler goroutine of any timeslice at all
	// under contention — a real-scheduler race exactly like racing a
	// wall-clock deadline is, just losing in the opposite direction.
	// h.tport.SentSignal() reacts the instant the handler's request hits
	// the wire; the 1ms real ticker is the fallback that keeps fake time
	// moving even between sends. Neither is a spin: both block for real
	// between events, so this goroutine holds no CPU while the handler
	// goroutine needs to run. 30s is a deadlock backstop only.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	backstop := time.NewTimer(30 * time.Second)
	defer backstop.Stop()
	for {
		select {
		case rr := <-done:
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, string(rr.Body))
			}
			var got nodeConfigResultJSON
			if err := json.Unmarshal(rr.Body, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Confirmed {
				t.Fatal("expected Confirmed false: node never replied")
			}
			return
		case <-h.tport.SentSignal():
			h.clock.Advance(200 * time.Millisecond)
		case <-ticker.C:
			h.clock.Advance(200 * time.Millisecond)
		case <-backstop.C:
			t.Fatal("timed out waiting for node config call to resolve (no progress for 30s — a real hang, not scheduling jitter)")
			return
		}
	}
}

func TestGetNICs(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/nics", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []nicJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The sandbox's loopback interface should show up at minimum.
	if len(out) == 0 {
		t.Log("no interfaces reported — acceptable in a maximally locked-down sandbox, but worth a manual check")
	}
}
