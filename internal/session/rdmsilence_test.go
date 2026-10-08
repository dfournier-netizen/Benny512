package session

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// Silence-breaker, fair-requeue, automatic-recheck, foreign-response and
// node-fault tests, all modelled on bench capture RDM-LOG36
// (docs/evidence/captures/RDM-LOG36.txt): three Martin ERA 800s on one port
// of node 2.11.90.6, one of which (4D50:00115938) answered none of 123
// requests while the other two answered every one.

var (
	log36NodeIP = netip.MustParseAddr("2.11.90.6")
	uid38       = rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115938} // never answers
	uid08       = rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115908}
	uid30       = rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115930}
)

// ownerResponseTimeout is the owner-decided (2026-10-06) per-attempt wait on
// a directly-wired port: 6,754 matched request/reply pairs across LOG2-LOG36
// never took longer than 96 ms to answer a first attempt.
const ownerResponseTimeout = 500 * time.Millisecond

// directSilentBudget is what one unanswered command costs on ProfileDirect:
// one attempt plus every retry, each waiting a full ResponseTimeout.
func directSilentBudget() time.Duration {
	return ProfileDirect.ResponseTimeout * time.Duration(1+ProfileDirect.Retries)
}

type wireReq struct {
	uid rdm.UID
	pid rdm.ParameterID
	cc  rdm.CommandClass
	tn  byte
	at  time.Time
}

// uidResponder is a per-UID responder on one node port: every UID answers
// ACK after the harness latency unless it is marked silent (or the whole node
// is). It counts transactions at the responder, from the real Art-Net bytes
// the controller put on the wire.
type uidResponder struct {
	h *rdmHarness

	mu     sync.Mutex
	silent map[rdm.UID]bool
	all    bool
	log    []wireReq
}

func newLOG36Rig(t *testing.T) (*rdmHarness, *uidResponder) {
	t.Helper()
	// Zero config on purpose: ProfileDirect, ScopeNodePort and the default
	// drain policy are what every real rig runs (nothing calls
	// SetNodeProfile).
	h := newRDMHarness(t, RDMConfig{})
	h.node = NodeRef{
		Key:  NodeKey{IP: log36NodeIP, BindIndex: 1},
		Addr: netip.AddrPortFrom(log36NodeIP, ArtNetUDPPort),
		Port: artnet.PortAddress{},
	}
	r := &uidResponder{h: h, silent: make(map[rdm.UID]bool)}
	h.tr.OnSend = r.onSend
	return h, r
}

func (r *uidResponder) onSend(sp SentPacket) {
	h := r.h
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
	now := h.clock.Now()
	h.mu.Lock()
	h.requests = append(h.requests, msg)
	h.sentAt = append(h.sentAt, now)
	h.mu.Unlock()

	r.mu.Lock()
	r.log = append(r.log, wireReq{uid: msg.DestinationUID, pid: msg.ParameterID, cc: msg.CommandClass, tn: msg.TransactionNumber, at: now})
	quiet := r.all || r.silent[msg.DestinationUID]
	r.mu.Unlock()
	if quiet {
		return
	}
	resp := h.buildResponse(msg, reply{Type: rdm.ResponseACK, Data: []byte{0x01}})
	h.clock.AfterFunc(h.latency, func() { h.ctrl.HandleInbound(resp) })
}

func (r *uidResponder) setSilent(uid rdm.UID, on bool) {
	r.mu.Lock()
	r.silent[uid] = on
	r.mu.Unlock()
}

func (r *uidResponder) setAll(on bool) {
	r.mu.Lock()
	r.all = on
	r.mu.Unlock()
}

func (r *uidResponder) logLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.log)
}

func (r *uidResponder) since(i int) []wireReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wireReq(nil), r.log[i:]...)
}

func (r *uidResponder) sentTo(uid rdm.UID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, w := range r.log {
		if w.uid == uid {
			n++
		}
	}
	return n
}

// tod delivers a complete ArtTodData for the rig's port, as the node sends it.
func (r *uidResponder) tod(uids ...rdm.UID) {
	r.h.ctrl.HandleInbound(todDataInbound(r.h.node.Port, uint16(len(uids)), 0, uids, r.h.node.Addr))
}

// --- closed-loop pollers ---------------------------------------------------

type polled struct {
	res       Result
	submitted time.Time
	doneAt    time.Time
}

// poller models a UI row that keeps one request outstanding per device and
// asks again interval after the previous one completed.
type poller struct {
	uid      rdm.UID
	interval time.Duration
	pids     []rdm.ParameterID

	n       int
	cmd     *Command
	sub     time.Time
	nextAt  time.Time
	results []polled
}

var pollPIDs = []rdm.ParameterID{rdm.PIDDeviceInfo, rdm.PIDDeviceLabel, rdm.PIDDMXStartAddress, rdm.PIDSoftwareVersionLabel}

func newPoller(uid rdm.UID, interval time.Duration) *poller {
	return &poller{uid: uid, interval: interval, pids: pollPIDs}
}

// runPollers advances simulated time in 1 ms steps until `until`, letting
// each poller observe completions and resubmit.
func runPollers(h *rdmHarness, ps []*poller, until time.Time) {
	for {
		now := h.clock.Now()
		for _, p := range ps {
			if p.cmd != nil {
				if res, ok := tryResult(p.cmd); ok {
					p.results = append(p.results, polled{res: res, submitted: p.sub, doneAt: now})
					p.cmd = nil
					p.nextAt = now.Add(p.interval)
				}
			}
			if p.cmd == nil && !now.Before(p.nextAt) && now.Before(until) {
				p.sub = now
				p.cmd = h.ctrl.Get(h.node, p.uid, p.pids[p.n%len(p.pids)], nil)
				p.n++
			}
		}
		if !now.Before(until) {
			return
		}
		h.clock.Advance(time.Millisecond)
	}
}

// --- 1. LOG36 replay ------------------------------------------------------

// TestLOG36DeadFixtureDoesNotStarveItsNeighbours replays the shape of
// RDM-LOG36: …38 is dead, …08 and …30 answer everything, all three share
// one node port, and a backlog of requests to …38 sits AHEAD of the healthy
// fixtures' polls. In the capture, from 15:22:33 to the end of the log the
// port carried nothing but …38, and the two healthy rows stalled.
func TestLOG36DeadFixtureDoesNotStarveItsNeighbours(t *testing.T) {
	h, r := newLOG36Rig(t)
	r.setSilent(uid38, true)
	r.tod(uid38, uid08, uid30) // LOG36 15:06:18.623: uidTotal=3

	start := h.clock.Now()
	backlog := make([]*Command, 0, 10)
	for i := 0; i < 10; i++ {
		backlog = append(backlog, h.ctrl.Get(h.node, uid38, pollPIDs[i%len(pollPIDs)], nil))
	}
	p08, p30, p38 := newPoller(uid08, time.Second), newPoller(uid30, time.Second), newPoller(uid38, time.Second)
	ps := []*poller{p08, p30, p38}
	const window = 60 * time.Second
	runPollers(h, ps, start.Add(window))

	// Bound for a healthy request: it can wait behind at most ONE silent
	// command to …38 — the one already on the wire when it was queued.
	// Every other …38 command queued ahead of it is moved behind it when
	// that silent command finishes, and once the breaker opens …38 costs
	// nothing at all. Add the replies of the (at most two) healthy requests
	// ahead of it at 10 ms each and the 1 ms poll granularity: 100 ms slack.
	bound := directSilentBudget() + 100*time.Millisecond
	for _, p := range []*poller{p08, p30} {
		if want := int(window / (p.interval + bound)); len(p.results) < want {
			t.Errorf("%s completed %d requests in %v, want at least %d — starved by its dead neighbour",
				p.uid, len(p.results), window, want)
		}
		worst := time.Duration(0)
		for _, pr := range p.results {
			if pr.res.Kind != ResultAck {
				t.Errorf("%s request at +%v finished %v, want ack", p.uid, pr.submitted.Sub(start), pr.res.Kind)
			}
			if d := pr.doneAt.Sub(pr.submitted); d > worst {
				worst = d
			}
		}
		if worst > bound {
			t.Errorf("%s worst request latency %v, want <= %v (one silent command to %s plus replies ahead)",
				p.uid, worst, bound, uid38)
		}
	}

	// …38's breaker opened on silence, after exactly SilenceBreakerTrip
	// counted strikes.
	var first *DeviceUnreachableError
	var all []Result
	for _, c := range backlog {
		if res, ok := tryResult(c); ok {
			all = append(all, res)
		}
	}
	for _, pr := range p38.results {
		all = append(all, pr.res)
	}
	for _, res := range all {
		var due *DeviceUnreachableError
		if res.Kind == ResultDeviceUnreachable && errors.As(res.Err, &due) {
			first = due
			break
		}
	}
	if first == nil {
		t.Fatalf("no command to %s ever finished device-unreachable; it was asked %d times",
			uid38, r.sentTo(uid38))
	}
	if first.Cause != CauseNoResponse || first.Silences != SilenceBreakerTrip {
		t.Errorf("breaker opened with cause=%v silences=%d, want %v / %d",
			first.Cause, first.Silences, CauseNoResponse, SilenceBreakerTrip)
	}
	for _, res := range all {
		if res.Kind == ResultDeviceUnreachable && res.Retransmissions != 0 {
			t.Errorf("a device-unreachable command was retransmitted %d times", res.Retransmissions)
		}
	}

	// While the breaker is open a command to …38 fails at once and costs
	// no transmission.
	reach := h.ctrl.Reachability()
	open := false
	for _, d := range reach {
		if d.UID == uid38 && d.Unreachable && d.Cause == CauseNoResponse {
			open = true
		}
		if d.UID != uid38 && d.Opens > 0 {
			t.Errorf("healthy %s has a breaker record %+v", d.UID, d)
		}
	}
	if !open {
		t.Errorf("Reachability() = %+v, want %s open with cause no-response", reach, uid38)
	} else {
		before := r.sentTo(uid38)
		c := h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil)
		res, done := tryResult(c)
		if !done || res.Kind != ResultDeviceUnreachable {
			t.Errorf("command to open-breaker %s: done=%v kind=%v, want immediate device-unreachable", uid38, done, res.Kind)
		}
		if got := r.sentTo(uid38); got != before {
			t.Errorf("transmissions to %s went %d -> %d for a suppressed command", uid38, before, got)
		}
	}

	// Transmission budget for …38 over the window: the commands before the
	// trip (SilenceBreakerTrip counted, plus the very first one, which went
	// out before anything on the port had answered and so carried no link
	// evidence) and at most one probe per initial cool-down, each one full
	// command of 1+Retries transmissions.
	probes := int(window/ProxyBreakerCooldownInitial) + 1
	maxTx := (SilenceBreakerTrip + 1 + probes) * (1 + ProfileDirect.Retries)
	if got := r.sentTo(uid38); got > maxTx {
		t.Errorf("transmissions to dead %s = %d over %v, want <= %d", uid38, got, window, maxTx)
	}
}

// --- 2. node-wide silence --------------------------------------------------

// TestNodeWideSilenceOpensNoBreakerAndResumesAtOnce is RDM-LOG4's reboot,
// on the LOG36 rig: every UID goes silent for 40 s, then everything answers.
// Silence that is not attributable to one device must open nothing.
func TestNodeWideSilenceOpensNoBreakerAndResumesAtOnce(t *testing.T) {
	h, r := newLOG36Rig(t)
	r.tod(uid38, uid08, uid30)
	ps := []*poller{newPoller(uid38, 500*time.Millisecond), newPoller(uid08, 500*time.Millisecond), newPoller(uid30, 500*time.Millisecond)}

	start := h.clock.Now()
	runPollers(h, ps, start.Add(5*time.Second))
	r.setAll(true)
	runPollers(h, ps, start.Add(45*time.Second))
	// The node comes back just after a request went out unanswered, so the
	// first chance to hear it is that request's next attempt (or, if that
	// was its last, the next command) one response timeout later.
	for n := r.logLen(); r.logLen() == n; {
		runPollers(h, ps, h.clock.Now().Add(time.Millisecond))
	}
	r.setAll(false)
	back := h.clock.Now()
	runPollers(h, ps, back.Add(10*time.Second))

	if got := h.ctrl.Stats().DevicesUnreachable; got != 0 {
		t.Errorf("Stats().DevicesUnreachable = %d after a node-wide outage, want 0", got)
	}
	for _, d := range h.ctrl.Reachability() {
		t.Errorf("Reachability() lists %s (%+v) after a node-wide outage", d.UID, d)
	}
	// Resumes at once: the port is answering again within one response
	// timeout of the node returning (the attempt that went out unheard, or
	// the next command if that was a last attempt; plus a reply at 10 ms and
	// poll granularity), and any request submitted after the node returned
	// waits at most for the attempt already on the wire — no cool-down to
	// sit out.
	bound := ownerResponseTimeout + 100*time.Millisecond
	var heard time.Time
	for _, p := range ps {
		after := 0
		for _, pr := range p.results {
			if pr.res.Kind == ResultDeviceUnreachable {
				t.Errorf("%s was written off during a node-wide outage (+%v)", p.uid, pr.submitted.Sub(start))
			}
			if pr.res.Kind == ResultAck && pr.doneAt.After(back) && (heard.IsZero() || pr.doneAt.Before(heard)) {
				heard = pr.doneAt
			}
			if pr.submitted.Before(back) {
				continue
			}
			after++
			if pr.res.Kind != ResultAck {
				t.Errorf("%s request after the node returned finished %v, want ack", p.uid, pr.res.Kind)
			}
			if d := pr.doneAt.Sub(pr.submitted); d > bound {
				t.Errorf("%s request after the node returned took %v, want <= %v", p.uid, d, bound)
			}
		}
		if after == 0 {
			t.Errorf("%s completed nothing after the node returned", p.uid)
		}
	}
	if heard.IsZero() || heard.Sub(back) > bound {
		t.Errorf("first answer on the port came %v after the node returned, want <= %v", heard.Sub(back), bound)
	}
}

// --- 3. link evidence between strikes --------------------------------------

// TestSilenceStrikesNeedAnotherDeviceAnsweringBetween: a device's silence
// only counts when something else on the same port answered since its last
// strike. Back to back with nothing else answering, only the first strike
// counts and nothing trips; with a neighbour answering in between, the
// device trips after exactly SilenceBreakerTrip strikes.
func TestSilenceStrikesNeedAnotherDeviceAnsweringBetween(t *testing.T) {
	h, r := newLOG36Rig(t)
	r.setSilent(uid38, true)
	r.tod(uid38, uid08)

	if res := h.awaitResult(h.ctrl.Get(h.node, uid08, rdm.PIDDeviceInfo, nil), time.Second); res.Kind != ResultAck {
		t.Fatalf("%s kind = %v, want ack", uid08, res.Kind)
	}
	const backToBack = 6
	for i := 0; i < backToBack; i++ {
		res := h.awaitResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil), 10*time.Second)
		if res.Kind != ResultTimeout {
			t.Fatalf("back-to-back command %d kind = %v, want timeout", i, res.Kind)
		}
	}
	if want := backToBack * (1 + ProfileDirect.Retries); r.sentTo(uid38) != want {
		t.Errorf("transmissions to %s = %d, want %d — back-to-back silence was held out", uid38, r.sentTo(uid38), want)
	}
	if got := h.ctrl.Stats().DevicesUnreachable; got != 0 {
		t.Errorf("DevicesUnreachable = %d after back-to-back silence with no other answers, want 0", got)
	}

	// Now a neighbour answers between each silent command. Phase one left one
	// counted strike, so two more rounds open the breaker.
	for i := 1; i < SilenceBreakerTrip; i++ {
		if res := h.awaitResult(h.ctrl.Get(h.node, uid08, rdm.PIDDeviceInfo, nil), time.Second); res.Kind != ResultAck {
			t.Fatalf("%s kind = %v, want ack", uid08, res.Kind)
		}
		if res := h.awaitResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil), 10*time.Second); res.Kind != ResultTimeout {
			t.Fatalf("interleaved command %d to %s kind = %v, want timeout", i, uid38, res.Kind)
		}
	}
	before := r.sentTo(uid38)
	res, done := tryResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil))
	if !done {
		t.Fatalf("after %d link-attributed silences a command to %s went to the wire, want an immediate device-unreachable", SilenceBreakerTrip, uid38)
	}
	var due *DeviceUnreachableError
	if !errors.As(res.Err, &due) {
		t.Fatalf("after %d link-attributed silences: kind=%v err=%v, want device-unreachable", SilenceBreakerTrip, res.Kind, res.Err)
	}
	if due.Cause != CauseNoResponse || due.Silences != SilenceBreakerTrip {
		t.Errorf("cause=%v silences=%d, want %v/%d", due.Cause, due.Silences, CauseNoResponse, SilenceBreakerTrip)
	}
	if r.sentTo(uid38) != before {
		t.Errorf("a suppressed command reached the wire")
	}
}

// --- 4. automatic recheck --------------------------------------------------

// tripSilence opens uid38's silence breaker with uid08 answering between
// strikes, and returns the time it opened.
func tripSilence(t *testing.T, h *rdmHarness, r *uidResponder) time.Time {
	t.Helper()
	r.setSilent(uid38, true)
	for i := 0; i < SilenceBreakerTrip; i++ {
		if res := h.awaitResult(h.ctrl.Get(h.node, uid08, rdm.PIDDeviceInfo, nil), time.Second); res.Kind != ResultAck {
			t.Fatalf("%s kind = %v, want ack", uid08, res.Kind)
		}
		if res := h.awaitResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceLabel, nil), 10*time.Second); res.Kind != ResultTimeout {
			t.Fatalf("strike %d kind = %v, want timeout", i, res.Kind)
		}
	}
	opened := h.clock.Now()
	res, done := tryResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceLabel, nil))
	if !done {
		t.Fatalf("after %d link-attributed silences a command to %s went to the wire, want an immediate device-unreachable", SilenceBreakerTrip, uid38)
	}
	if res.Kind != ResultDeviceUnreachable {
		t.Fatalf("after %d strikes kind = %v (err %v), want device-unreachable", SilenceBreakerTrip, res.Kind, res.Err)
	}
	return opened
}

func onlyDeviceInfoTo(t *testing.T, reqs []wireReq, uid rdm.UID) int {
	t.Helper()
	n := 0
	for _, w := range reqs {
		if w.uid != uid {
			continue
		}
		if w.pid != rdm.PIDDeviceInfo || w.cc != rdm.GetCommand {
			t.Errorf("probe to %s was %v PID 0x%04X, want GET DEVICE_INFO", uid, w.cc, uint16(w.pid))
		}
		n++
	}
	return n
}

func retryIn(t *testing.T, h *rdmHarness, uid rdm.UID) time.Duration {
	t.Helper()
	for _, d := range h.ctrl.Reachability() {
		if d.UID == uid {
			if !d.Unreachable {
				t.Fatalf("%s is not unreachable: %+v", uid, d)
			}
			return d.RetryAt.Sub(h.clock.Now())
		}
	}
	t.Fatalf("%s not in Reachability()", uid)
	return 0
}

func TestSilenceBreakerRechecksOnItsOwn(t *testing.T) {
	t.Run("probe answers and closes the breaker", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		cooldown := retryIn(t, h, uid38)
		mark := r.logLen()
		drainEvents(h.ctrl.Events())

		h.clock.Advance(cooldown - time.Millisecond)
		if n := len(r.since(mark)); n != 0 {
			t.Fatalf("%d transmissions before the cool-down expired", n)
		}
		r.setSilent(uid38, false) // the tech power-cycled it
		h.clock.Advance(time.Millisecond)
		h.clock.Advance(50 * time.Millisecond)
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1 {
			t.Fatalf("automatic probes to %s = %d, want exactly 1 with no caller submitting", uid38, n)
		}
		for _, d := range h.ctrl.Reachability() {
			if d.UID == uid38 && d.Unreachable {
				t.Errorf("Reachability() still reports %s unreachable after it answered the probe", uid38)
			}
		}
		sawProbe := false
		for _, ev := range drainEvents(h.ctrl.Events()) {
			if ev.Kind == EventCommandComplete && ev.UID == uid38 && ev.Result != nil &&
				ev.Result.Kind == ResultAck && ev.Result.Request.PID == rdm.PIDDeviceInfo {
				sawProbe = true
			}
		}
		if !sawProbe {
			t.Error("the probe's ACK did not reach the event stream")
		}
		before := r.sentTo(uid38)
		c := h.ctrl.Get(h.node, uid38, rdm.PIDDeviceLabel, nil)
		if r.sentTo(uid38) != before+1 {
			t.Error("a caller command after recovery did not go straight to the wire")
		}
		if res := h.awaitResult(c, time.Second); res.Kind != ResultAck {
			t.Errorf("post-recovery kind = %v, want ack", res.Kind)
		}
	})

	t.Run("silent probe reopens longer when the link proved alive", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		openUntil := h.clock.Now().Add(retryIn(t, h, uid38))
		h.clock.Advance(time.Second)
		// A neighbour answers during the cool-down: the link is alive.
		if res := h.awaitResult(h.ctrl.Get(h.node, uid08, rdm.PIDDeviceInfo, nil), time.Second); res.Kind != ResultAck {
			t.Fatalf("%s kind = %v", uid08, res.Kind)
		}
		mark := r.logLen()
		h.clock.Set(openUntil.Add(directSilentBudget()))
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1+ProfileDirect.Retries {
			t.Fatalf("probe transmissions = %d, want one command's worth (%d)", n, 1+ProfileDirect.Retries)
		}
		if got := retryIn(t, h, uid38); got != ProxyBreakerCooldownInitial*ProxyBreakerCooldownFactor {
			t.Errorf("reopened for %v, want %v (grown: the link answered during the cool-down)",
				got, ProxyBreakerCooldownInitial*ProxyBreakerCooldownFactor)
		}
	})

	t.Run("silent probe with no link evidence reopens at the same cool-down", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		cooldown := retryIn(t, h, uid38)
		mark := r.logLen()
		h.clock.Advance(cooldown + directSilentBudget())
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1+ProfileDirect.Retries {
			t.Fatalf("probe transmissions = %d, want %d", n, 1+ProfileDirect.Retries)
		}
		if got := retryIn(t, h, uid38); got != cooldown {
			t.Errorf("reopened for %v, want the same %v (nothing proved the link alive)", got, cooldown)
		}
		// And it keeps rechecking on its own.
		mark = r.logLen()
		h.clock.Advance(cooldown + directSilentBudget())
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1+ProfileDirect.Retries {
			t.Errorf("second automatic probe transmissions = %d, want %d", n, 1+ProfileDirect.Retries)
		}
	})

	t.Run("a ToD listing the device makes the probe immediate", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		h.clock.Advance(2 * time.Second)
		mark := r.logLen()
		r.tod(uid38, uid08, uid30) // fixture power-cycled; node re-announces it
		h.clock.Advance(time.Millisecond)
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1 {
			t.Errorf("transmissions to %s right after a ToD listing it = %d, want 1 immediate probe", uid38, n)
		}
	})

	t.Run("no probe while the device is absent from the ToD", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		r.tod(uid08, uid30) // …38 has left the table
		mark := r.logLen()
		h.clock.Advance(3 * ProxyBreakerCooldownMax)
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 0 {
			t.Errorf("probes to %s while it is not in the ToD = %d, want 0", uid38, n)
		}
		r.tod(uid38, uid08, uid30)
		h.clock.Advance(time.Millisecond)
		if n := onlyDeviceInfoTo(t, r.since(mark), uid38); n != 1 {
			t.Errorf("probes once %s reappeared in the ToD = %d, want 1", uid38, n)
		}
	})

	t.Run("Stop leaves no live probe", func(t *testing.T) {
		h, r := newLOG36Rig(t)
		r.tod(uid38, uid08, uid30)
		tripSilence(t, h, r)
		mark := r.logLen()
		h.ctrl.Stop()
		h.clock.Advance(3 * ProxyBreakerCooldownMax)
		if n := len(r.since(mark)); n != 0 {
			t.Errorf("%d transmissions after Stop", n)
		}
		if n := h.clock.PendingTimers(); n != 0 {
			t.Errorf("%d timers still pending after Stop and %v", n, 3*ProxyBreakerCooldownMax)
		}
	})
}

// --- 5. fair requeue -------------------------------------------------------

type uidPID struct {
	uid rdm.UID
	pid rdm.ParameterID
}

func (u uidPID) String() string { return fmt.Sprintf("%s/0x%04X", u.uid, uint16(u.pid)) }

// TestSilentDeviceMovesToTheBackOfTheQueue: when a command to D finishes
// silent, D's other pending commands go behind everyone else's, keeping
// each device's own order.
func TestSilentDeviceMovesToTheBackOfTheQueue(t *testing.T) {
	h, r := newLOG36Rig(t)
	r.setSilent(uid38, true)
	order := []uidPID{
		{uid38, rdm.PIDDeviceInfo},           // D1 — goes on the wire at once
		{uid38, rdm.PIDDeviceLabel},          // D2
		{uid08, rdm.PIDDeviceInfo},           // X1
		{uid38, rdm.PIDDMXStartAddress},      // D3
		{uid30, rdm.PIDDeviceInfo},           // Y1
		{uid08, rdm.PIDDeviceLabel},          // X2
		{uid38, rdm.PIDSoftwareVersionLabel}, // D4
	}
	cmds := make([]*Command, 0, len(order))
	for _, s := range order {
		cmds = append(cmds, h.ctrl.Get(h.node, s.uid, s.pid, nil))
	}
	for _, c := range cmds {
		h.awaitResult(c, 30*time.Second)
	}
	var got []uidPID
	for _, w := range r.since(0) {
		s := uidPID{w.uid, w.pid}
		if len(got) == 0 || got[len(got)-1] != s {
			got = append(got, s)
		}
	}
	want := []uidPID{order[0], order[2], order[4], order[5], order[1], order[3], order[6]}
	if len(got) != len(want) {
		t.Fatalf("commands on the wire = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wire order = %v\nwant         %v", got, want)
		}
	}
}

// --- 6. foreign response ---------------------------------------------------

// log36Lines returns the hex payload printed after every capture line that
// match accepts, decoded to the literal datagram bytes.
func log36Lines(t *testing.T, match func(header string) bool) [][]byte {
	t.Helper()
	f, err := os.Open("../../docs/evidence/captures/RDM-LOG36.txt")
	if err != nil {
		t.Fatalf("open LOG36: %v", err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	armed := false
	for sc.Scan() {
		line := sc.Text()
		if match(line) {
			armed = true
			continue
		}
		if armed && strings.HasPrefix(strings.TrimSpace(line), "hex: ") {
			b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(line), "hex: "))
			if err != nil {
				t.Fatalf("bad hex in LOG36: %v", err)
			}
			out = append(out, b)
			armed = false
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan LOG36: %v", err)
	}
	return out
}

// TestForeignResponseDoesNotSatisfyOurCommand feeds the literal LOG36
// datagram from 15:07:12.003: a second controller's traffic, src=…38
// dst=…38 TN=9, which carries the TN, source UID and command class of our
// own TN=9 GET DEVICE_INFO to …38. It is not addressed to us.
func TestForeignResponseDoesNotSatisfyOurCommand(t *testing.T) {
	foreign := log36Lines(t, func(l string) bool { return strings.HasPrefix(l, "[2026-10-06 15:07:12.003] IN   ArtRdm") })
	if len(foreign) != 1 {
		t.Fatalf("found %d foreign datagrams at 15:07:12.003, want 1", len(foreign))
	}
	h, r := newLOG36Rig(t)
	r.setSilent(uid38, true)
	// TN 0..8 go to a healthy fixture so that our command to …38 is TN 9.
	for i := 0; i < 9; i++ {
		if res := h.awaitResult(h.ctrl.Get(h.node, uid08, rdm.PIDDeviceInfo, nil), time.Second); res.Kind != ResultAck {
			t.Fatalf("warm-up %d kind = %v", i, res.Kind)
		}
	}
	mark := r.logLen()
	cmd := h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil)
	ours := r.since(mark)
	if len(ours) != 1 || ours[0].tn != 9 {
		t.Fatalf("our request = %+v, want one GET with TN 9", ours)
	}

	h.clock.Advance(10 * time.Millisecond)
	h.ctrl.HandleInbound(Inbound{Data: foreign[0], From: netip.MustParseAddrPort("2.11.52.172:6454")})
	h.clock.Advance(10 * time.Millisecond)
	if res, done := tryResult(cmd); done {
		t.Fatalf("a response addressed to %s completed our command as %v", uid38, res.Kind)
	}
	if st := h.ctrl.Stats(); st.ForeignResponses != 1 || st.StrayResponses != 0 {
		t.Errorf("ForeignResponses=%d StrayResponses=%d, want 1/0", st.ForeignResponses, st.StrayResponses)
	}

	// The genuine answer, addressed to us, still completes it.
	req := h.allRequests()[len(h.allRequests())-1]
	h.ctrl.HandleInbound(h.buildResponse(req, reply{Type: rdm.ResponseACK, Data: []byte("ERA800")}))
	res, done := tryResult(cmd)
	if !done {
		t.Fatalf("the genuine response addressed to us did not complete the command")
	}
	if res.Kind != ResultAck || string(res.Data) != "ERA800" {
		t.Fatalf("genuine response: kind=%v data=%q, want ack/ERA800", res.Kind, res.Data)
	}
	if res.AckTimers != 0 {
		t.Errorf("AckTimers = %d — the foreign ACK_TIMER was folded into our command", res.AckTimers)
	}
	if got := len(r.since(mark)); got != 1 {
		t.Errorf("transmissions for the command = %d, want 1 (the foreign packet must not trigger any)", got)
	}
}

// --- 7. node faults --------------------------------------------------------

// TestMalformedArtRdmIsRecordedAsANodeFault feeds the four literal 239-byte
// datagrams 2.11.90.6 sent at 15:22:10: ArtRdm framing around ArtPollReply
// content, which the RDM layer rejects with a checksum mismatch.
func TestMalformedArtRdmIsRecordedAsANodeFault(t *testing.T) {
	bad := log36Lines(t, func(l string) bool { return strings.Contains(l, "DECODE ERROR") })
	if len(bad) != 4 {
		t.Fatalf("found %d DECODE ERROR datagrams in LOG36, want 4", len(bad))
	}
	h, _ := newLOG36Rig(t)
	drainEvents(h.ctrl.Events())
	first := h.clock.Now()
	for i, b := range bad {
		if len(b) != 239 {
			t.Fatalf("datagram %d is %d bytes, want 239", i, len(b))
		}
		from := netip.AddrPortFrom(log36NodeIP, ArtNetUDPPort)
		if i == 3 {
			// The same node seen through a dual-stack socket.
			from = netip.AddrPortFrom(netip.AddrFrom16(log36NodeIP.As16()), ArtNetUDPPort)
		}
		h.ctrl.HandleInbound(Inbound{Data: b, From: from})
		if i < 3 {
			h.clock.Advance(time.Millisecond)
		}
	}
	last := h.clock.Now()
	// Not an Art-Net packet at all: not a node fault.
	h.ctrl.HandleInbound(Inbound{Data: []byte("not art-net"), From: netip.AddrPortFrom(log36NodeIP, ArtNetUDPPort)})

	faults := h.ctrl.NodeFaults()
	if faults == nil {
		t.Fatal("NodeFaults() returned nil, want a non-nil slice")
	}
	if len(faults) != 1 {
		t.Fatalf("NodeFaults() = %+v, want one entry for %s", faults, log36NodeIP)
	}
	f := faults[0]
	if f.IP != log36NodeIP || f.Malformed != 4 {
		t.Errorf("fault = %+v, want IP %s Malformed 4", f, log36NodeIP)
	}
	if !f.First.Equal(first) || !f.Last.Equal(last) {
		t.Errorf("First/Last = %v/%v, want %v/%v", f.First, f.Last, first, last)
	}
	if !strings.Contains(f.LastError, "checksum") {
		t.Errorf("LastError = %q, want it to name the checksum mismatch", f.LastError)
	}
	if got := h.ctrl.Stats().MalformedRDM; got != 4 {
		t.Errorf("Stats().MalformedRDM = %d, want 4", got)
	}
	n := 0
	for _, ev := range drainEvents(h.ctrl.Events()) {
		if ev.Kind != EventNodeFault {
			continue
		}
		n++
		if ev.From != log36NodeIP || !strings.Contains(ev.Detail, "checksum") || ev.At.IsZero() {
			t.Errorf("EventNodeFault = From %v Detail %q At %v", ev.From, ev.Detail, ev.At)
		}
	}
	if n != 4 {
		t.Errorf("EventNodeFault count = %d, want 4", n)
	}
}

// --- 8. silent command cost -------------------------------------------------

// TestSilentDirectCommandFinishesAfterThreeHalfSecondAttempts: on the
// default profile an unanswered command costs 3 × 500 ms, not 3 × 1.5 s.
func TestSilentDirectCommandFinishesAfterThreeHalfSecondAttempts(t *testing.T) {
	h, r := newLOG36Rig(t)
	r.setSilent(uid38, true)
	start := h.clock.Now()
	res := h.awaitResult(h.ctrl.Get(h.node, uid38, rdm.PIDDeviceInfo, nil), 10*time.Second)
	if res.Kind != ResultTimeout {
		t.Fatalf("kind = %v, want timeout", res.Kind)
	}
	if want := 3 * ownerResponseTimeout; res.Elapsed != want {
		t.Errorf("silent command took %v, want %v", res.Elapsed, want)
	}
	sent := r.since(0)
	if len(sent) != 3 {
		t.Fatalf("transmissions = %d, want 3", len(sent))
	}
	for i, w := range sent {
		if want := start.Add(time.Duration(i) * ownerResponseTimeout); !w.at.Equal(want) {
			t.Errorf("attempt %d at +%v, want +%v", i, w.at.Sub(start), want.Sub(start))
		}
	}
}
