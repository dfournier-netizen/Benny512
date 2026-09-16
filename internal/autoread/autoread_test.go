package autoread_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/autoread"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
)

// TestMain fails the package if a test leaves goroutines behind — the reader
// owns exactly one, bound to the context Run was given, and the whole point
// of requirement 6 is that cancelling that context ends it. Same pattern as
// internal/sacn/sender_test.go.
func TestMain(m *testing.M) {
	before := runtime.NumGoroutine()
	code := m.Run()
	if code == 0 {
		var after int
		for i := 0; i < 50; i++ {
			after = runtime.NumGoroutine()
			if after <= before {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if after > before {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			fmt.Fprintf(os.Stderr, "goroutine leak: %d before, %d after\n%s", before, after, buf)
			code = 1
		}
	}
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// The evidence.
//
// Everything below is transcribed from $HOME/mnt/RDM App/Logs/RDM-LOG30.txt,
// captured on a live rig on 2026-09-16. The ArtTodData packets are the
// LITERAL hex the log recorded off the wire, not packets this test builds, so
// the decode under test is the real one and the UID lists below are checked
// against the bytes rather than against another copy of themselves.
// ---------------------------------------------------------------------------

// todEmpty31 is the ArtTodData that came back from 2.11.90.2 for
// Port-Address 31 at 14:04:38.092, 14:04:38.328 and 14:04:38.568 — byte for
// byte identical all three times. uidTotal=0: "this port is empty".
const todEmpty31 = "4172742d4e6574000081000e01010000000000000300001f00000000"

// todLate31A is 14:04:49.899 — uidTotal=11, 11.554 s after the flush that
// was already answered "empty" three times and given up on.
const todLate31A = "4172742d4e6574000081000e01010000000000000100001f000b000b" +
	"22a6846f307822a684b7309422a684b930b922a684b930c722a684bd30a4" +
	"22a6868a469322a6868a46a022a693ae9bb122a693b37ecb22a693b57e91" +
	"22a693b79c90"

// todLate31B is 14:04:50.913 — uidTotal=14, 12.568 s after the same flush.
const todLate31B = "4172742d4e6574000081000e01010000000000000200001f000e000e" +
	"22a67f9a43b922a6845f30a322a6845f30b722a6846f308822a684b8308a" +
	"22a684b7308c22a684b8309a22a6868046c222a68688468422a6868846b0" +
	"22a693ad98bc22a693ae9b8f22a693b09b8b22a6e2e4b3d1"

// wantLateA / wantLateB are the "uids:" lines the log printed for those two
// packets, transcribed as text. The test asserts the decoded packets match
// these, so a fat-fingered hex digit or a dropped UID fails loudly instead of
// quietly shrinking the expected set.
var wantLateA = []string{
	"22A6:846F3078", "22A6:84B73094", "22A6:84B930B9", "22A6:84B930C7",
	"22A6:84BD30A4", "22A6:868A4693", "22A6:868A46A0", "22A6:93AE9BB1",
	"22A6:93B37ECB", "22A6:93B57E91", "22A6:93B79C90",
}

var wantLateB = []string{
	"22A6:7F9A43B9", "22A6:845F30A3", "22A6:845F30B7", "22A6:846F3088",
	"22A6:84B8308A", "22A6:84B7308C", "22A6:84B8309A", "22A6:868046C2",
	"22A6:86884684", "22A6:868846B0", "22A6:93AD98BC", "22A6:93AE9B8F",
	"22A6:93B09B8B", "22A6:E2E4B3D1",
}

// The literal inter-packet gaps from the log, as the test advances its fake
// clock: flush at 14:04:37.881, empties at .092 / .328 / .568, then the two
// late tables at 14:04:49.899 and 14:04:50.913.
const (
	gapFlushToEmpty1 = 211 * time.Millisecond
	gapEmpty1To2     = 236 * time.Millisecond
	gapEmpty2To3     = 240 * time.Millisecond
	gapEmpty3ToLateA = 11331 * time.Millisecond
	gapLateAToLateB  = 1014 * time.Millisecond
)

// node2119002 is 2.11.90.2, the node in the log.
var node2119002 = netip.MustParseAddr("2.11.90.2")

// ---------------------------------------------------------------------------
// Rig: a real RDMController over a real FakeTransport, a real Registry, and
// the reader wired to it exactly as internal/web wires it. Nothing here
// short-circuits the path under test; the assertions count transactions at
// the responder, which is the only place "did we send it?" has an honest
// answer (the discipline internal/params/probecache_test.go set).
// ---------------------------------------------------------------------------

type reqKey struct {
	uid rdm.UID
	pid rdm.ParameterID
}

type fakeDevice struct {
	// silent devices never answer anything — the four responders in
	// RDM-LOG30 that were asked DEVICE_INFO 9-12 times apiece and answered
	// none of them.
	silent bool
	// advertised is this device's SUPPORTED_PARAMETERS answer.
	advertised []rdm.ParameterID
	model      string
	mfr        string
	startAddr  uint16
	footprint  uint16
}

type rig struct {
	t      *testing.T
	clock  *session.FakeClock
	tport  *session.FakeTransport
	ctrl   *session.RDMController
	reg    *registry.Registry
	reader *autoread.Reader
	port   artnet.PortAddress
	ref    session.NodeRef

	mu          sync.Mutex
	devices     map[rdm.UID]*fakeDevice
	reqs        map[reqKey]int
	order       []rdm.UID
	inflight    int
	maxInflight int
	passes      int
}

func newRig(t *testing.T, profile session.TimeoutProfile, maxAttempts int) *rig {
	t.Helper()
	// params keeps per-UID probe state for the life of the process.
	params.ClearAllDeviceState()
	params.ClearDescriptorCache()

	r := &rig{
		t:       t,
		clock:   session.NewFakeClock(time.Time{}),
		devices: map[rdm.UID]*fakeDevice{},
		reqs:    map[reqKey]int{},
	}
	r.tport = session.NewFakeTransport()
	r.ctrl = session.NewRDMController(session.RDMConfig{
		Transport: r.tport, Clock: r.clock, DefaultProfile: profile,
	})
	a := session.NewArtNetSession(session.ArtNetConfig{Transport: r.tport, Clock: r.clock})
	r.reg = registry.New(a, r.ctrl)
	r.reader = autoread.New(autoread.Config{
		Ctrl:        r.ctrl,
		MaxAttempts: maxAttempts,
		OnPass: func(autoread.Pass) {
			r.mu.Lock()
			r.passes++
			r.mu.Unlock()
		},
	})
	r.reg.SetOnDeviceSeen(r.reader.Note)

	r.port, _ = artnet.NewPortAddress(0, 1, 15) // Port-Address 31 = net 0, sub-net 1, universe 15
	r.ref = session.NodeRef{
		Key:  session.NodeKey{IP: node2119002, BindIndex: 1},
		Addr: netip.AddrPortFrom(node2119002, session.ArtNetUDPPort),
		Port: r.port,
	}

	r.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		r.mu.Lock()
		r.reqs[reqKey{msg.DestinationUID, msg.ParameterID}]++
		r.order = append(r.order, msg.DestinationUID)
		dev := r.devices[msg.DestinationUID]
		if dev == nil || dev.silent {
			r.mu.Unlock()
			return // no answer will come; nothing to hold "in flight"
		}
		r.inflight++
		if r.inflight > r.maxInflight {
			r.maxInflight = r.inflight
		}
		r.mu.Unlock()
		resp := dev.answer(msg)
		// OnSend runs with the controller's own mutex held, so the reply
		// must be scheduled, never delivered inline.
		r.clock.AfterFunc(time.Millisecond, func() {
			r.mu.Lock()
			r.inflight--
			r.mu.Unlock()
			r.ctrl.HandleRDMResponse(resp)
		})
	}
	return r
}

func (d *fakeDevice) answer(msg rdm.Message) rdm.Message {
	resp := rdm.Message{
		DestinationUID: msg.SourceUID, SourceUID: msg.DestinationUID,
		TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACK),
		SubDevice: msg.SubDevice, CommandClass: rdm.GetCommandResponse, ParameterID: msg.ParameterID,
	}
	nack := func(reason rdm.NackReason) rdm.Message {
		resp.PortIDOrResponseType = byte(rdm.ResponseNackReason)
		resp.ParameterData = []byte{byte(reason >> 8), byte(reason)}
		return resp
	}
	switch msg.ParameterID {
	case rdm.PIDDeviceInfo:
		resp.ParameterData = params.EncodeDeviceInfo(params.DeviceInfo{
			ProtocolVersionMajor: 1, DeviceModelID: 0x009B, ProductCategory: 0x0509,
			DMXFootprint: d.footprint, CurrentPersonality: 1, PersonalityCount: 1,
			DMXStartAddress: d.startAddr,
		})
	case rdm.PIDSupportedParameters:
		resp.ParameterData = rdm.EncodeSupportedParameters(d.advertised)
	case rdm.PIDDeviceModelDescription:
		resp.ParameterData = []byte(d.model)
	case rdm.PIDManufacturerLabel:
		resp.ParameterData = []byte(d.mfr)
	default:
		return nack(rdm.NackUnknownPID)
	}
	return resp
}

// start runs the registry, the reader and a fake-clock driver, and returns a
// stop func. Each is bound to the same context, so stop() must leave nothing
// behind (TestMain checks).
func (r *rig) start() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); r.reg.RunContext(ctx) }()
	go func() { defer wg.Done(); r.reader.Run(ctx) }()
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			r.clock.Advance(5 * time.Millisecond)
			time.Sleep(100 * time.Microsecond)
		}
	}()
	return ctx, func() {
		cancel()
		r.ctrl.Stop()
		wg.Wait()
	}
}

// deliver hands the controller one literal ArtTodData packet from the log.
func (r *rig) deliver(hexPacket string) []rdm.UID {
	r.t.Helper()
	raw, err := hex.DecodeString(hexPacket)
	if err != nil {
		r.t.Fatalf("log hex will not decode: %v", err)
	}
	pkt, err := artnet.Decode(raw)
	if err != nil {
		r.t.Fatalf("artnet.Decode of the literal log bytes: %v", err)
	}
	if pkt.Kind != artnet.KindTodData {
		r.t.Fatalf("literal log bytes decoded as %v, want ArtTodData", pkt.Kind)
	}
	r.ctrl.HandleInbound(session.Inbound{
		Data: raw, From: netip.AddrPortFrom(node2119002, session.ArtNetUDPPort),
	})
	return pkt.TodData.Tod
}

func (r *rig) count(uid rdm.UID, pid rdm.ParameterID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[reqKey{uid, pid}]
}

func (r *rig) passCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.passes
}

func (r *rig) snapshotOrder() []rdm.UID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rdm.UID(nil), r.order...)
}

// waitFor polls cond until it holds or the real-time budget runs out.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func uidsFromStrings(t *testing.T, ss []string) []rdm.UID {
	t.Helper()
	out := make([]rdm.UID, 0, len(ss))
	for _, s := range ss {
		u, ok := rdm.ParseUID(s)
		if !ok {
			t.Fatalf("cannot parse UID %q transcribed from the log", s)
		}
		out = append(out, u)
	}
	return out
}

func uidStrings(us []rdm.UID) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.String())
	}
	return out
}

// ---------------------------------------------------------------------------
// The defect.
// ---------------------------------------------------------------------------

// TestLateToDGetsReadAutomatically replays RDM-LOG30's 2.11.90.2
// Port-Address 31 sequence literally: flush, three empty ToDs at +211 / +236 /
// +240 ms, discovery accepts "the port is empty" and finishes, and then the
// real table arrives 11.3 s later in two blocks of 11 and 14 UIDs.
//
// Every one of those 25 devices must get a DEVICE_INFO read, with no browser
// anywhere in the picture. Before this change the ToD was merged into the
// registry and nothing was read at all: the rows appeared with null data and
// stayed null until an operator opened Inspect on each one.
func TestLateToDGetsReadAutomatically(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)

	wantA := uidsFromStrings(t, wantLateA)
	wantB := uidsFromStrings(t, wantLateB)
	all := append(append([]rdm.UID(nil), wantA...), wantB...)
	for i, u := range all {
		r.devices[u] = &fakeDevice{
			advertised: []rdm.ParameterID{rdm.PIDDMXStartAddress, rdm.PIDDeviceLabel},
			model:      "PROTEUS RAYZOR 1960",
			mfr:        "Elation",
			startAddr:  uint16(1 + i*10),
			footprint:  9,
		}
	}

	_, stop := r.start()
	defer stop()

	// The flush, and the three empties that followed it.
	disc := r.ctrl.Discover(r.ref)
	r.clock.Advance(gapFlushToEmpty1)
	if got := r.deliver(todEmpty31); len(got) != 0 {
		t.Fatalf("the first literal ToD carried %d UIDs, want 0 — this is the packet "+
			"the log shows arriving 211 ms after the flush with uidTotal=0", len(got))
	}
	select {
	case res := <-disc.Done():
		if len(res.UIDs) != 0 {
			t.Fatalf("discovery finished with %d UIDs, want 0 — the empty ToD is "+
				"accepted as-is, which is exactly the behaviour that makes the late "+
				"table a problem", len(res.UIDs))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not finish on the first empty ToD")
	}
	r.clock.Advance(gapEmpty1To2)
	r.deliver(todEmpty31)
	r.clock.Advance(gapEmpty2To3)
	r.deliver(todEmpty31)

	// ... discovery has moved on to other ports. 11.3 s later:
	r.clock.Advance(gapEmpty3ToLateA)
	gotA := r.deliver(todLate31A)
	if diff := strings.Join(uidStrings(gotA), " "); diff != strings.Join(wantLateA, " ") {
		t.Fatalf("the literal 14:04:49.899 packet decoded to\n  %s\nbut the log's own "+
			"uids: line says\n  %s", diff, strings.Join(wantLateA, " "))
	}
	r.clock.Advance(gapLateAToLateB)
	gotB := r.deliver(todLate31B)
	if diff := strings.Join(uidStrings(gotB), " "); diff != strings.Join(wantLateB, " ") {
		t.Fatalf("the literal 14:04:50.913 packet decoded to\n  %s\nbut the log's own "+
			"uids: line says\n  %s", diff, strings.Join(wantLateB, " "))
	}

	waitFor(t, "all 25 late-ToD devices to be read", func() bool {
		for _, u := range all {
			if r.count(u, rdm.PIDDeviceInfo) == 0 {
				return false
			}
		}
		return true
	})

	var missed []string
	for _, u := range all {
		if n := r.count(u, rdm.PIDDeviceInfo); n != 1 {
			missed = append(missed, fmt.Sprintf("%s=%d", u, n))
		}
	}
	if len(missed) > 0 {
		sort.Strings(missed)
		t.Errorf("DEVICE_INFO reads per UID (want exactly 1 each): %s", strings.Join(missed, " "))
	}

	// The registry is the thing the Devices screen reads, so check the data
	// actually landed there rather than only that a packet left.
	// MANUFACTURER_LABEL is the last PID of the pass, so waiting on it waits
	// for the whole pass to have landed in the registry.
	waitFor(t, "the registry to record every late UID's whole pass", func() bool {
		for _, u := range all {
			f, ok := r.reg.Fixture(u)
			if !ok || !f.HasDeviceInfo || !f.ManufacturerLabelKnown || !f.ModelDescriptionKnown {
				return false
			}
		}
		return true
	})
	for i, u := range all {
		f, _ := r.reg.Fixture(u)
		if want := uint16(1 + i*10); f.DMXStartAddress != want {
			t.Errorf("%s: registry DMXStartAddress = %d, want %d", u, f.DMXStartAddress, want)
		}
		if f.ModelDescription != "PROTEUS RAYZOR 1960" || f.ManufacturerLabel != "Elation" {
			t.Errorf("%s: registry has model %q / manufacturer %q, want the device's own report",
				u, f.ModelDescription, f.ManufacturerLabel)
		}
	}
}

// TestReadsAreSerialized is the half of the fix that protects the rig rather
// than the screen. An RDM line is a shared half-duplex bus; a reader that
// fanned 25 devices out across goroutines would look faster in a unit test
// and be a real regression on a real rig.
//
// Two wire-level assertions, neither of which can be satisfied by a struct
// agreeing with itself: at most one request may be outstanding at any
// instant, and a device's requests must be contiguous — once the reader has
// moved on from a UID it must never come back to it mid-pass.
func TestReadsAreSerialized(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)
	all := append(uidsFromStrings(t, wantLateA), uidsFromStrings(t, wantLateB)...)
	for _, u := range all {
		r.devices[u] = &fakeDevice{advertised: []rdm.ParameterID{rdm.PIDDMXStartAddress}, model: "m", mfr: "f"}
	}
	_, stop := r.start()
	defer stop()

	r.deliver(todLate31A)
	r.deliver(todLate31B)

	waitFor(t, "every device to be read", func() bool {
		for _, u := range all {
			if r.count(u, rdm.PIDManufacturerLabel) == 0 {
				return false
			}
		}
		return true
	})

	r.mu.Lock()
	maxInflight := r.maxInflight
	r.mu.Unlock()
	if maxInflight > 1 {
		t.Errorf("%d RDM requests were outstanding at once, want at most 1 — the "+
			"background read must not put two devices on a shared half-duplex bus "+
			"at the same time", maxInflight)
	}

	order := r.snapshotOrder()
	seenDone := map[rdm.UID]bool{}
	var cur rdm.UID
	var have bool
	for i, u := range order {
		if have && u == cur {
			continue
		}
		if seenDone[u] {
			t.Fatalf("request %d went back to %s after the reader had moved on to "+
				"another device — reads are interleaving, not serialized.\nOrder: %s",
				i, u, strings.Join(uidStrings(order), " "))
		}
		if have {
			seenDone[cur] = true
		}
		cur, have = u, true
	}
}

// TestSecondToDDoesNotReReadKnownDevices is requirement 2. RDM-LOG30's
// browser sweep asked DEVICE_INFO 173 times across one session because it
// re-ran on every refresh of the Devices screen; a node re-announcing its
// table must cost nothing.
func TestSecondToDDoesNotReReadKnownDevices(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)
	all := uidsFromStrings(t, wantLateA)
	for _, u := range all {
		r.devices[u] = &fakeDevice{advertised: []rdm.ParameterID{rdm.PIDDMXStartAddress}, model: "m", mfr: "f"}
	}
	_, stop := r.start()
	defer stop()

	r.deliver(todLate31A)
	waitFor(t, "the first pass to finish", func() bool { return r.passCount() >= len(all) })

	// The same node announcing the same table again, twice more — which is
	// exactly what a rediscovery of a port already read looks like.
	r.deliver(todLate31A)
	r.deliver(todLate31A)
	// Give the reader every chance to do the wrong thing.
	time.Sleep(150 * time.Millisecond)

	for _, u := range all {
		if n := r.count(u, rdm.PIDDeviceInfo); n != 1 {
			t.Errorf("%s: DEVICE_INFO asked %d times across three identical ToDs, want 1", u, n)
		}
	}
	if got := r.passCount(); got != len(all) {
		t.Errorf("%d read passes for %d devices across three identical ToDs, want %d",
			got, len(all), len(all))
	}
}

// TestSilentDeviceIsCappedAndLeftUnread is requirement 3, and it is the one
// with a number behind it. RDM-LOG30's four silent responders — 4D50:001158FE
// and 4D50:0011597E at 63 requests each, 4D50:001159BE and 4D50:001159FE at
// 48 each, none of which ever ACKed DEVICE_INFO — are what "do not make that
// worse" means.
//
// The assertions: a bounded number of attempts, no follow-on PIDs wasted on a
// device that answered nothing, a state that SAYS it gave up, and a registry
// row that is explicitly unread rather than filled with plausible zeroes.
func TestSilentDeviceIsCappedAndLeftUnread(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 2)
	silent := uidsFromStrings(t, []string{"4D50:001158FE", "4D50:0011597E"})
	for _, u := range silent {
		r.devices[u] = &fakeDevice{silent: true}
	}
	_, stop := r.start()
	defer stop()

	for _, u := range silent {
		r.reg.NoteFixture(r.ref, u)
	}

	waitFor(t, "the reader to give up on both silent devices", func() bool {
		for _, u := range silent {
			st, _ := r.reader.State(autoread.KeyFor(node2119002, 1, r.port, u))
			if st != autoread.StateGaveUp {
				return false
			}
		}
		return true
	})
	// And it must STAY given up: nothing re-queues it behind our back.
	time.Sleep(200 * time.Millisecond)

	// ProfileDirect is 1 transmission + 2 retries per command, so a capped
	// device costs at most 2 passes x 3 = 6 DEVICE_INFO packets. The log's
	// worst case was 12, spread over four unbounded browser sweeps.
	const maxDeviceInfo = 2 * (1 + 2)
	for _, u := range silent {
		st, attempts := r.reader.State(autoread.KeyFor(node2119002, 1, r.port, u))
		if st != autoread.StateGaveUp {
			t.Errorf("%s: state %q, want %q", u, st, autoread.StateGaveUp)
		}
		if attempts != 2 {
			t.Errorf("%s: %d attempts, want exactly 2", u, attempts)
		}
		if n := r.count(u, rdm.PIDDeviceInfo); n > maxDeviceInfo {
			t.Errorf("%s: %d DEVICE_INFO requests, want at most %d — RDM-LOG30 spent "+
				"9-12 on this exact device and the point of the cap is that that "+
				"cannot happen again", u, n, maxDeviceInfo)
		}
		// A device that said nothing to DEVICE_INFO is not asked five more
		// questions it is not going to answer either.
		for _, pid := range []rdm.ParameterID{
			rdm.PIDSupportedParameters, rdm.PIDDeviceModelDescription, rdm.PIDManufacturerLabel,
		} {
			if n := r.count(u, pid); n != 0 {
				t.Errorf("%s: PID 0x%04X asked %d times of a device that answered no "+
					"DEVICE_INFO at all, want 0", u, uint16(pid), n)
			}
		}
		// Nothing invented. The row is unread and says so.
		f, ok := r.reg.Fixture(u)
		if !ok {
			t.Fatalf("%s: vanished from the registry; a device we gave up reading must "+
				"still be listed", u)
		}
		if f.HasDeviceInfo || f.ModelDescriptionKnown || f.ManufacturerLabelKnown {
			t.Errorf("%s: registry claims something is known (deviceInfo=%v model=%v mfr=%v) "+
				"about a device that answered nothing", u, f.HasDeviceInfo,
				f.ModelDescriptionKnown, f.ManufacturerLabelKnown)
		}
		if f.DMXStartAddress != 0 || f.DMXFootprint != 0 || f.Class != 0 {
			t.Errorf("%s: registry holds addressing/class data for a silent device: "+
				"addr=%d footprint=%d class=%v", u, f.DMXStartAddress, f.DMXFootprint, f.Class)
		}
	}
}

// TestForgetReopensTheCap covers the other half of requirement 3: the cap is
// the reader's own budget, and an operator asking for the device by name
// (POST /api/device/{uid}/introspect) or clearing the device cache outranks
// it.
func TestForgetReopensTheCap(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 1)
	uid := uidsFromStrings(t, []string{"4D50:001159BE"})[0]
	dev := &fakeDevice{silent: true}
	r.devices[uid] = dev
	_, stop := r.start()
	defer stop()

	key := autoread.KeyFor(node2119002, 1, r.port, uid)
	r.reg.NoteFixture(r.ref, uid)
	waitFor(t, "the reader to give up", func() bool {
		st, _ := r.reader.State(key)
		return st == autoread.StateGaveUp
	})

	// The operator opens Inspect; the device is now answering.
	r.mu.Lock()
	dev.silent = false
	dev.advertised = []rdm.ParameterID{rdm.PIDDMXStartAddress}
	dev.model, dev.mfr, dev.startAddr = "KL Core IP", "Elation", 101
	r.mu.Unlock()
	if n := r.reader.Forget(uid); n != 1 {
		t.Fatalf("Forget dropped %d ledger entries, want 1", n)
	}
	if st, _ := r.reader.State(key); st != autoread.StateUnknown {
		t.Fatalf("after Forget the state is %q, want %q", st, autoread.StateUnknown)
	}

	r.reg.NoteFixture(r.ref, uid)
	waitFor(t, "the re-opened device to be read", func() bool {
		st, _ := r.reader.State(key)
		return st == autoread.StateRead
	})
	f, _ := r.reg.Fixture(uid)
	if !f.HasDeviceInfo || f.DMXStartAddress != 101 {
		t.Errorf("after the cap was reopened the registry has deviceInfo=%v addr=%d, want true/101",
			f.HasDeviceInfo, f.DMXStartAddress)
	}
}

// TestResetClearsTheLedger pins the other reset path: POST /api/devices/clear
// and the full reset wipe the device table, and a ledger that outlived it
// would leave a rig that never fills in again.
func TestResetClearsTheLedger(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)
	all := uidsFromStrings(t, wantLateA)
	for _, u := range all {
		r.devices[u] = &fakeDevice{advertised: []rdm.ParameterID{rdm.PIDDMXStartAddress}, model: "m", mfr: "f"}
	}
	_, stop := r.start()
	defer stop()

	r.deliver(todLate31A)
	waitFor(t, "the first pass", func() bool { return r.passCount() >= len(all) })

	r.reader.Reset()
	r.reg.ClearDevices()
	r.deliver(todLate31A)
	waitFor(t, "every device to be read a second time", func() bool {
		for _, u := range all {
			if r.count(u, rdm.PIDDeviceInfo) < 2 {
				return false
			}
		}
		return true
	})
}

// TestRunStopsOnCancel is requirement 6, asserted directly rather than left
// to TestMain: Run must return promptly when its context is cancelled, even
// with work still queued and a device mid-pass.
func TestRunStopsOnCancel(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)
	all := uidsFromStrings(t, wantLateB)
	for _, u := range all {
		r.devices[u] = &fakeDevice{silent: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.reader.Run(ctx); close(done) }()
	go func() {
		for ctx.Err() == nil {
			r.clock.Advance(5 * time.Millisecond)
			time.Sleep(100 * time.Microsecond)
		}
	}()
	for _, u := range all {
		r.reader.Note(r.ref, u)
	}
	waitFor(t, "a read to start", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.order) > 0
	})
	cancel()
	r.ctrl.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5 s of its context being cancelled")
	}
	if st, _ := r.reader.State(autoread.KeyFor(node2119002, 1, r.port, all[0])); st == autoread.StateGaveUp {
		t.Error("a device was marked gaveUp because the process shut down — a cancelled " +
			"pass is this app's fault, not the device's, and must not spend the cap")
	}
}

// TestSpeculativePIDsStayGated is requirement 4, at the wire.
// PRODUCT_DETAIL_ID_LIST (0x0070) and PROXIED_DEVICE_COUNT (0x0011) are in
// the core-identity pass, and both are speculative: a device that does not
// advertise them must be asked zero times, and a device that does must still
// be asked. The asymmetry probecache_test.go documents is not to be "fixed"
// here by dropping them from the pass — that would be a silent classification
// loss with no NACK to reveal it.
func TestSpeculativePIDsStayGated(t *testing.T) {
	r := newRig(t, session.ProfileDirect, 0)
	quiet := uidsFromStrings(t, []string{"22A6:846F3078"})[0]
	proxy := uidsFromStrings(t, []string{"22A6:93B79C90"})[0]
	r.devices[quiet] = &fakeDevice{
		advertised: []rdm.ParameterID{rdm.PIDDMXStartAddress}, model: "m", mfr: "f",
	}
	r.devices[proxy] = &fakeDevice{
		advertised: []rdm.ParameterID{rdm.PIDProductDetailIDList, rdm.PIDProxiedDeviceCount},
		model:      "m", mfr: "f",
	}
	_, stop := r.start()
	defer stop()

	r.reader.Note(r.ref, quiet)
	r.reader.Note(r.ref, proxy)
	waitFor(t, "both devices to be read", func() bool {
		return r.count(quiet, rdm.PIDManufacturerLabel) > 0 && r.count(proxy, rdm.PIDManufacturerLabel) > 0
	})

	for _, pid := range []rdm.ParameterID{rdm.PIDProductDetailIDList, rdm.PIDProxiedDeviceCount} {
		if n := r.count(quiet, pid); n != 0 {
			t.Errorf("PID 0x%04X reached a device that does not advertise it %d times, want 0",
				uint16(pid), n)
		}
		if n := r.count(proxy, pid); n != 1 {
			t.Errorf("PID 0x%04X reached the device that DOES advertise it %d times, want 1 — "+
				"gating a PID the device reported is a silent feature loss",
				uint16(pid), n)
		}
	}
	// SUPPORTED_PARAMETERS is what makes the gate possible, so it is on the
	// wire — once per device, not once per gated PID.
	for _, u := range []rdm.UID{quiet, proxy} {
		if n := r.count(u, rdm.PIDSupportedParameters); n != 1 {
			t.Errorf("%s: SUPPORTED_PARAMETERS fetched %d times in one pass, want 1", u, n)
		}
	}
}
