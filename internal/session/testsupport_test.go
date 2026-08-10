package session

import (
	"net/netip"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// --- addresses used across the session tests ---------------------------

func addrPort(s string, port uint16) netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr(s), port)
}

var (
	nodeIP   = netip.MustParseAddr("2.11.90.2")
	nodeAddr = netip.AddrPortFrom(nodeIP, ArtNetUDPPort)

	uidA = rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x0000A001}
	uidB = rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x0000B002}
	uidC = rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x0000C003}
)

func mustPortAddress(t *testing.T, net, sub, uni byte) artnet.PortAddress {
	t.Helper()
	pa, err := artnet.NewPortAddress(net, sub, uni)
	if err != nil {
		t.Fatalf("NewPortAddress(%d,%d,%d): %v", net, sub, uni, err)
	}
	return pa
}

// --- ArtPollReply construction ----------------------------------------

// pollReply builds a plausible 4-port node reply. Callers mutate the result
// before encoding to model whatever the test needs.
func pollReply(ip string, bindIndex byte, short, long string) artnet.PollReply {
	r := artnet.DefaultPollReply()
	r.IPAddress = netip.MustParseAddr(ip).As4()
	r.BindIndex = bindIndex
	r.ShortName = short
	r.LongName = long
	r.EstaManufacturer = 0x4F50
	r.Oem = 0x1234
	r.Style = byte(StyleNode)
	r.NumPorts = 4
	r.NetSwitch = 0
	r.SubSwitch = 0
	// All four ports are DMX outputs (bit 7), on universes 0-3.
	for i := 0; i < 4; i++ {
		r.PortTypes[i] = 0x80
		r.SwOut[i] = byte(i)
		r.SwIn[i] = byte(i)
	}
	r.Status1 = 0x02 // RDM capable
	r.MAC = [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	return r
}

func inboundPollReply(r artnet.PollReply, from netip.AddrPort) Inbound {
	return Inbound{
		Data: artnet.Encode(artnet.Packet{Kind: artnet.KindPollReply, PollReply: r}),
		From: from,
	}
}

// --- ArtTodData construction ------------------------------------------

func todDataInbound(port artnet.PortAddress, uidTotal uint16, blockCount byte, uids []rdm.UID, from netip.AddrPort) Inbound {
	td := artnet.TodData{
		ProtocolVersion: artnet.DefaultProtocolVersion,
		RdmVersion:      0x01,
		Port:            1,
		Net:             port.Net,
		CommandResponse: TodFull,
		Address:         port.SubUni(),
		UidTotal:        uidTotal,
		BlockCount:      blockCount,
		Tod:             uids,
	}
	return Inbound{Data: artnet.Encode(artnet.Packet{Kind: artnet.KindTodData, TodData: td}), From: from}
}

func todNakInbound(port artnet.PortAddress, from netip.AddrPort) Inbound {
	td := artnet.TodData{
		ProtocolVersion: artnet.DefaultProtocolVersion,
		RdmVersion:      0x01,
		Net:             port.Net,
		CommandResponse: TodNak,
		Address:         port.SubUni(),
	}
	return Inbound{Data: artnet.Encode(artnet.Packet{Kind: artnet.KindTodData, TodData: td}), From: from}
}

// --- scripted RDM responder -------------------------------------------

// reply describes how the fake responder answers one request. The zero value
// answers ACK with no data at the harness's default latency.
type reply struct {
	// Drop suppresses the response entirely (models a lost packet).
	Drop bool
	// Delay overrides the harness latency for this response.
	Delay time.Duration
	// Type is the RDM Response Type (ACK / ACK_TIMER / NACK / ACK_OVERFLOW).
	Type rdm.ResponseType
	// Data is the response Parameter Data.
	Data []byte
	// PID overrides the echoed Parameter ID (for mismatch tests).
	PID *rdm.ParameterID
	// Duplicates schedules N extra identical copies of the response, each
	// one Delay later than the previous (models UDP duplication).
	Duplicates int
	// TNOffset corrupts the echoed Transaction Number.
	TNOffset int
	// SourceUID overrides the responding UID (for mis-addressed replies).
	SourceUID *rdm.UID
	// MessageCount sets the responder's queued-message count.
	MessageCount byte
}

func ackTimerData(units uint16) []byte {
	return []byte{byte(units >> 8), byte(units)}
}

func nackData(reason rdm.NackReason) []byte {
	return []byte{byte(reason >> 8), byte(reason)}
}

// rdmHarness wires a FakeClock, a FakeTransport and an RDMController
// together with a scripted responder. Replies are consumed in order, one per
// outbound ArtRdm packet; an exhausted script means "no response", which is
// how timeout paths are driven.
//
// Responses are scheduled on the fake clock rather than delivered inline,
// because OnSend runs while the controller still holds its mutex. That is
// also what makes reply latency (and therefore delay/reorder scripting)
// natural to express.
type rdmHarness struct {
	t     *testing.T
	clock *FakeClock
	tr    *FakeTransport
	ctrl  *RDMController
	node  NodeRef

	mu       sync.Mutex
	scripts  []reply
	next     int
	requests []rdm.Message
	latency  time.Duration
}

func newRDMHarness(t *testing.T, cfg RDMConfig) *rdmHarness {
	t.Helper()
	clock := NewFakeClock(time.Time{})
	tr := NewFakeTransport()
	cfg.Clock = clock
	cfg.Transport = tr
	ctrl := NewRDMController(cfg)

	h := &rdmHarness{
		t: t, clock: clock, tr: tr, ctrl: ctrl,
		latency: 10 * time.Millisecond,
		node: NodeRef{
			Key:  NodeKey{IP: nodeIP, BindIndex: 1},
			Addr: nodeAddr,
			Port: artnet.PortAddress{Net: 0, SubNet: 0, Universe: 0},
		},
	}
	tr.OnSend = h.onSend
	t.Cleanup(func() { ctrl.Stop() })
	return h
}

// script appends replies to the responder's program.
func (h *rdmHarness) script(rs ...reply) {
	h.mu.Lock()
	h.scripts = append(h.scripts, rs...)
	h.mu.Unlock()
}

// requestCount reports how many ArtRdm requests the responder has seen.
func (h *rdmHarness) requestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

// requestAt returns the Nth request the responder saw.
func (h *rdmHarness) requestAt(i int) rdm.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i < 0 || i >= len(h.requests) {
		h.t.Fatalf("no request at index %d (have %d)", i, len(h.requests))
	}
	return h.requests[i]
}

func (h *rdmHarness) allRequests() []rdm.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]rdm.Message(nil), h.requests...)
}

func (h *rdmHarness) onSend(sp SentPacket) {
	if sp.DecodeErr != nil {
		h.t.Errorf("controller sent undecodable Art-Net bytes: %v", sp.DecodeErr)
		return
	}
	if sp.Packet.Kind != artnet.KindRdm {
		return
	}
	msg, err := sp.Packet.Rdm.DecodedRDMMessage()
	if err != nil {
		h.t.Errorf("controller sent undecodable RDM bytes: %v", err)
		return
	}

	h.mu.Lock()
	h.requests = append(h.requests, msg)
	var r reply
	have := false
	if h.next < len(h.scripts) {
		r = h.scripts[h.next]
		h.next++
		have = true
	}
	latency := h.latency
	h.mu.Unlock()

	if !have || r.Drop {
		return
	}
	delay := r.Delay
	if delay <= 0 {
		delay = latency
	}
	for i := 0; i <= r.Duplicates; i++ {
		when := time.Duration(i+1) * delay
		resp := h.buildResponse(msg, r)
		h.clock.AfterFunc(when, func() { h.ctrl.HandleInbound(resp) })
	}
}

func (h *rdmHarness) buildResponse(req rdm.Message, r reply) Inbound {
	pid := req.ParameterID
	if r.PID != nil {
		pid = *r.PID
	}
	src := req.DestinationUID
	if r.SourceUID != nil {
		src = *r.SourceUID
	}
	resp := rdm.Message{
		DestinationUID:       req.SourceUID,
		SourceUID:            src,
		TransactionNumber:    byte(int(req.TransactionNumber) + r.TNOffset),
		PortIDOrResponseType: byte(r.Type),
		MessageCount:         r.MessageCount,
		SubDevice:            req.SubDevice,
		CommandClass:         responseClassFor(req.CommandClass),
		ParameterID:          pid,
		ParameterData:        r.Data,
	}
	pkt := artnet.EncodeRdmPacket(resp, artnet.DefaultProtocolVersion, h.node.Port.Net, h.node.Port.SubUni())
	return Inbound{
		Data: artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: pkt}),
		From: h.node.Addr,
	}
}

// awaitResult advances the clock in small steps until the command completes,
// failing if it has not completed within limit of simulated time.
func (h *rdmHarness) awaitResult(cmd *Command, limit time.Duration) Result {
	h.t.Helper()
	const step = time.Millisecond
	for elapsed := time.Duration(0); elapsed <= limit; elapsed += step {
		select {
		case res := <-cmd.Done():
			return res
		default:
		}
		h.clock.Advance(step)
	}
	select {
	case res := <-cmd.Done():
		return res
	default:
	}
	h.t.Fatalf("command did not complete within %v of simulated time", limit)
	return Result{}
}

// tryResult returns the result if the command has already completed.
func tryResult(cmd *Command) (Result, bool) {
	select {
	case res := <-cmd.Done():
		return res, true
	default:
		return Result{}, false
	}
}

// rdmRequestsSent extracts every ArtRdm request from a batch of captured
// datagrams.
func rdmRequestsSent(t *testing.T, sent []SentPacket) []rdm.Message {
	t.Helper()
	var out []rdm.Message
	for _, sp := range sent {
		if sp.Packet.Kind != artnet.KindRdm {
			continue
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			t.Fatalf("undecodable RDM in captured packet: %v", err)
		}
		out = append(out, msg)
	}
	return out
}

// drainEvents collects every event currently buffered.
func drainEvents(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func drainNodeEvents(ch <-chan NodeEvent) []NodeEvent {
	var out []NodeEvent
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}
