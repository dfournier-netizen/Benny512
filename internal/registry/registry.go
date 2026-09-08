// Package registry merges ArtNetSession node events and RDMController
// ToD/command events into one queryable state: nodes (with ports) and, per
// node port, the fixtures (RDM responders) discovered there.
//
// Layering rule: this package only consumes the two engines' Events()
// channels and read-only accessor methods (Nodes(), ToD()) — it never
// touches Transport or Clock, so it is testable by feeding synthetic events
// or by driving the real engines against session.FakeTransport.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// Fixture is one RDM responder discovered on a node port — despite the
// name (kept for compatibility with the existing Fixtures screen/API; see
// Registry.Devices), this represents ANY discovered RDM responder, fixture
// or not: a gateway/node, a splitter, a LumenRadio-style wireless radio and
// a moving light are all Fixture values, distinguished by Class. Non-
// fixture devices are first-class citizens of this table, not a special
// case (report §1.3/feature C).
type Fixture struct {
	UID              rdm.UID
	ManufacturerID   uint16
	ManufacturerName string
	Node             session.NodeKey
	Port             artnet.PortAddress
	FirstSeen        time.Time
	LastSeen         time.Time
	// Params caches the last-known value of any param the UI has fetched,
	// keyed by PID. Values are raw parameter data; internal/params decodes
	// them. Absent from this map means "not yet fetched".
	Params map[rdm.ParameterID][]byte

	// Class is this device's DeviceClass, derived from cached DEVICE_INFO
	// (product_category), PRODUCT_DETAIL_ID_LIST and PROXIED_DEVICE_COUNT
	// as they're observed — see reclassify. Zero value is ClassUnknown
	// until at least DEVICE_INFO has been fetched once (e.g. via
	// params.Client.Introspect or DeviceInfo).
	Class DeviceClass
	// ProductCategory/ProductDetails/DMXFootprint/IsWirelessProxy are the
	// raw signals reclassify derives Class from, cached here so the web
	// layer/UI can show them directly without a second RDM round-trip.
	ProductCategory rdm.ProductCategory
	ProductDetails  []rdm.ProductDetail
	DMXFootprint    uint16
	IsWirelessProxy bool
	// SubDeviceCount is DEVICE_INFO's reported number of sub-devices. It is
	// meaningful only once HasDeviceInfo is true; zero then means a root-only
	// responder, while a non-zero value can be used by a phase-aware client
	// without changing this registry's one-root-fixture identity model.
	SubDeviceCount uint16

	// DeviceModelID/HasDeviceInfo cache DEVICE_INFO's numeric model ID (the
	// Devices screen's Model column fallback when DEVICE_MODEL_DESCRIPTION
	// is unavailable — see ManufacturerLabel/ModelDescription below).
	// HasDeviceInfo is true once DEVICE_INFO has ACKed at least once,
	// distinguishing "model ID legitimately 0" from "never fetched".
	DeviceModelID uint16
	HasDeviceInfo bool

	// ManufacturerLabel/ModelDescription cache the device's OWN report of
	// MANUFACTURER_LABEL (0x0081) / DEVICE_MODEL_DESCRIPTION (0x0080) —
	// task ask: "prefer the device's own report" over the static ESTA
	// manufacturer-ID table (ManufacturerName, above) for the Devices
	// screen's Manufacturer/Model columns. The *Known flags are true once a
	// GET for the respective PID has completed at all (ACK -> the string;
	// NACK -> stays "" but Known still flips true) so callers can tell "not
	// yet attempted" (show a pending placeholder) apart from "device
	// doesn't report this" (show the fallback immediately) without retrying
	// forever. See handleRDMEvent/cacheParam/noteParamNack.
	ManufacturerLabel      string
	ManufacturerLabelKnown bool
	ModelDescription       string
	ModelDescriptionKnown  bool

	// ProxyUnreachable / ProxyUnreachableSince / ProxyRetryAt /
	// ProxyRefusals mirror session's per-device proxy circuit breaker (see
	// session.DeviceUnreachableError) so a device Benny512 has stopped
	// asking is a *stated* condition rather than a row that quietly stops
	// filling in.
	//
	// This is the fix for the round-4 bench symptom: with the far side of a
	// CRMX link refusing everything, the Devices screen simply showed less
	// and less information with nothing to explain why. These are raw facts
	// only — the sentence a lighting tech reads is composed in the web
	// layer, the same division of labour as ManufacturerLabel above and
	// effectiveManufacturer.
	//
	// ProxyUnreachable is cleared the moment the device answers anything at
	// all, ACK or NACK. ProxyRefusals is the consecutive refused-command
	// count behind the breaker, kept after recovery so a link that is only
	// marginal still leaves a trace.
	ProxyUnreachable      bool
	ProxyUnreachableSince time.Time
	ProxyRetryAt          time.Time
	ProxyRefusals         int

	// ProxiedDeviceCount/ProxiedDeviceCountKnown/ProxiedListChanged cache
	// this device's own PROXIED_DEVICE_COUNT (0x0011) report — Phase D task
	// 1's "expose proxy status as structured data" ask, replacing
	// PROXIED_DEVICE_COUNT's now-hidden (params.TierHidden) generic-editor
	// row with a first-class field the UI can render as a device-level
	// badge directly off the Devices/device-detail JSON, no extra fetch.
	// ProxiedDeviceCountKnown is true once a GET for this PID has ACKed at
	// least once (distinguishing "confirmed zero" from "never fetched",
	// same convention as ManufacturerLabelKnown above); ProxiedDeviceCount
	// is only meaningful when it's true. ProxiedListChanged mirrors the
	// PID's own "list changed" flag (E1.20 §8.4.1) — true means the device
	// is telling the controller its proxied-UID list has moved since last
	// asked, i.e. GET PROXIED_DEVICES would return something new; nothing
	// in this app currently acts on it (PROXIED_DEVICES is Hidden too, see
	// classification.go), so it's carried here only so a future pass has
	// it without another wire round-trip to relearn it.
	ProxiedDeviceCount      uint16
	ProxiedDeviceCountKnown bool
	ProxiedListChanged      bool
}

// clone returns a deep-enough copy for safe hand-out across the mutex
// boundary (Params is a map and must not alias the registry's copy).
func (f Fixture) clone() Fixture {
	cp := f
	cp.Params = make(map[rdm.ParameterID][]byte, len(f.Params))
	for k, v := range f.Params {
		cp.Params[k] = append([]byte(nil), v...)
	}
	cp.ProductDetails = append([]rdm.ProductDetail(nil), f.ProductDetails...)
	return cp
}

// NodeView is a node plus a summary of what's been discovered on its ports,
// as consumed by the Nodes screen.
type NodeView struct {
	session.Node
	FixtureCount int
}

// Registry holds the merged view. All exported methods are safe for
// concurrent use.
//
// Registry is the sole true consumer of both engines' Events() channels.
// Go channels split values across concurrent readers rather than
// broadcasting them, so a second independent goroutine calling
// artnet.Events()/rdmc.Events() directly (as web.Server's WS live-view
// pumps originally did) silently steals roughly half of Registry's own
// events at random — which, for RDM events specifically, meant
// EventCommandComplete arrived at Registry.Run() unpredictably often
// (sometimes never, depending on goroutine scheduling), so
// PARAMETER_DESCRIPTION/DEVICE_INFO GETs issued through the web layer
// would classify a device's DeviceClass nondeterministically or not at
// all. Found via cmd/benny512's demo end-to-end test this session
// (TestDemoSensorWarningAndDeviceClass kept flaking until this was fixed).
// The fix: Registry re-publishes (fans out) every event it processes on
// NodeEvents()/RDMEvents(), and web.Server's pumps read from those instead
// of the engines directly — Registry stays the only direct engine
// consumer.
type Registry struct {
	artnet *session.ArtNetSession
	rdmc   *session.RDMController

	mu       sync.RWMutex
	fixtures map[fixtureKey]*Fixture

	// nodeOut/rdmOut are the fan-out republish channels described above.
	// Buffered and non-blocking-send (drop-if-full, mirroring
	// RDMController.emitLocked's own policy) so a slow or absent WS
	// consumer can never stall Registry's own event processing.
	nodeOut chan session.NodeEvent
	rdmOut  chan session.Event
}

// fanOutBufferSize is generous relative to realistic event bursts (a full
// SUPPORTED_PARAMETERS introspection run is, at most, a few hundred
// GET/PARAMETER_DESCRIPTION round-trips) so a WS pump that's briefly slow
// to drain doesn't lose events under normal load.
const fanOutBufferSize = 512

type fixtureKey struct {
	ip   netip.Addr
	bind byte
	port uint16
	uid  rdm.UID
}

// New builds a Registry over an already-constructed ArtNetSession and
// RDMController. It does not start either engine; call Run to begin
// consuming their event streams.
func New(a *session.ArtNetSession, r *session.RDMController) *Registry {
	return &Registry{
		artnet:   a,
		rdmc:     r,
		fixtures: make(map[fixtureKey]*Fixture),
		nodeOut:  make(chan session.NodeEvent, fanOutBufferSize),
		rdmOut:   make(chan session.Event, fanOutBufferSize),
	}
}

// NodeEvents is Registry's fan-out republish of every ArtNetSession node
// event it has processed — downstream live-view consumers (web.Server's WS
// pump) should read from here, not from the engine's own Events(); see the
// Registry doc comment for why.
func (reg *Registry) NodeEvents() <-chan session.NodeEvent { return reg.nodeOut }

// RDMEvents is NodeEvents' analogue for RDM controller events.
func (reg *Registry) RDMEvents() <-chan session.Event { return reg.rdmOut }

func publishNonBlocking[T any](ch chan T, ev T) {
	select {
	case ch <- ev:
	default:
		// Drop rather than block Registry's own processing loop; the WS
		// live view missing one event under a rare, hard burst is far
		// preferable to Registry itself falling behind on classification/
		// discovery.
	}
}

// Run consumes both engines' event channels until they close. It is meant
// to run on its own goroutine for the life of the process; node events
// require no action here (Nodes() reads straight through to the session),
// but ToD updates populate/prune the fixture table. Every event, of either
// kind, is also republished on NodeEvents()/RDMEvents() — see the Registry
// doc comment.
func (reg *Registry) Run() {
	reg.RunContext(context.Background())
}

// RunContext allows temporary rehearsal registries to shut down without
// leaving an event-pump goroutine behind.
func (reg *Registry) RunContext(ctx context.Context) {
	nodeEvents := reg.artnet.Events()
	rdmEvents := reg.rdmc.Events()
	for nodeEvents != nil || rdmEvents != nil {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-nodeEvents:
			if !ok {
				nodeEvents = nil
				continue
			}
			_ = ev // node table itself lives in ArtNetSession; nothing to merge
			publishNonBlocking(reg.nodeOut, ev)
		case ev, ok := <-rdmEvents:
			if !ok {
				rdmEvents = nil
				continue
			}
			reg.handleRDMEvent(ev)
			publishNonBlocking(reg.rdmOut, ev)
		}
	}
}

// cachePIDFor picks the parameter an ACK's data actually belongs to.
//
// For every PID but one that is simply the PID that was requested. GET
// QUEUED_MESSAGE is the exception: it has no value of its own, and its
// response carries some other parameter's deferred answer under that
// parameter's PID. Caching a drained payload under 0x0020 would file a
// status report or a sensor reading as "the value of QUEUED_MESSAGE", which
// is both wrong and, on a fixtures screen, visible. Filing it under the PID
// the responder actually answered with puts a deferred DEVICE_INFO in the
// DEVICE_INFO slot, which is what the device meant by sending it.
func cachePIDFor(res *session.Result) rdm.ParameterID {
	if res.Request.PID == rdm.PIDQueuedMessage && res.ResponsePID != 0 {
		return res.ResponsePID
	}
	return res.Request.PID
}

func (reg *Registry) handleRDMEvent(ev session.Event) {
	switch ev.Kind {
	case session.EventToDUpdate:
		reg.mergeToD(ev.Node, ev.UIDs)
	case session.EventQueuedMessageCollected:
		// A queued message the controller pulled back that answered no
		// command still waiting — stale, or one whose command has given up.
		// It is still a real reading from a real device, so file it under
		// the parameter it actually is, and take it as proof the device is
		// reachable. What must not happen is it being credited to whichever
		// command was in flight at the time; the controller has already
		// ruled that out (session.collectRoutesToLocked), and this event
		// carries exactly the messages that rule excluded.
		if ev.QueuedPID != 0 {
			reg.cacheParam(ev.Node, ev.UID, ev.QueuedPID, ev.QueuedData)
		}
		reg.noteReachable(ev.Node, ev.UID)
	case session.EventCommandComplete:
		if ev.Result == nil {
			return
		}
		switch ev.Result.Kind {
		case session.ResultAck:
			// Cache on every ACK, including a legitimate zero-length one
			// (an empty MANUFACTURER_LABEL/DEVICE_MODEL_DESCRIPTION string
			// is a valid ACK, not "nothing happened" — the previous
			// len(data)>0 gate would have left such a device stuck looking
			// "not yet attempted" forever).
			reg.cacheParam(ev.Node, ev.UID, cachePIDFor(ev.Result), ev.Result.Data)
			reg.noteReachable(ev.Node, ev.UID)
		case session.ResultNack:
			reg.noteParamNack(ev.Node, ev.UID, ev.Result.Request.PID)
			// A NACK is an answer: it came back through the proxy, so
			// whatever the breaker thought, this device is reachable.
			reg.noteReachable(ev.Node, ev.UID)
		case session.ResultDeviceUnreachable:
			// Deliberately NOT routed through noteParamNack: the device has
			// said nothing about this PID, so recording it as "asked and
			// answered" would repeat the round-2 cache-poisoning bug in a
			// new place.
			var due *session.DeviceUnreachableError
			if errors.As(ev.Result.Err, &due) {
				reg.noteUnreachable(ev.Node, ev.UID, due)
			}
		}
	}
}

// getOrCreateLocked returns the fixture for (node, uid), creating a fresh
// entry (ESTA-fallback ManufacturerName pre-computed from the UID alone,
// per ManufacturerName's own fallback chain) if this is the first time
// anything about this UID has been observed. Callers must hold reg.mu.
func (reg *Registry) getOrCreateLocked(node session.NodeKey, port artnet.PortAddress, uid rdm.UID, now time.Time) *Fixture {
	key := fixtureKey{ip: node.IP, bind: node.BindIndex, port: port.RawValue(), uid: uid}
	f, ok := reg.fixtures[key]
	if !ok {
		f = &Fixture{
			UID:              uid,
			ManufacturerID:   uid.ManufacturerID,
			ManufacturerName: ManufacturerName(uid.ManufacturerID),
			Node:             node,
			Port:             port,
			FirstSeen:        now,
			Params:           make(map[rdm.ParameterID][]byte),
		}
		reg.fixtures[key] = f
	}
	return f
}

func (reg *Registry) mergeToD(node session.NodeRef, uids []rdm.UID) {
	now := time.Now()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, uid := range uids {
		f := reg.getOrCreateLocked(node.Key, node.Port, uid, now)
		f.LastSeen = now
	}
}

func (reg *Registry) cacheParam(node session.NodeRef, uid rdm.UID, pid rdm.ParameterID, data []byte) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	f := reg.getOrCreateLocked(node.Key, node.Port, uid, time.Now())
	f.LastSeen = time.Now()
	f.Params[pid] = append([]byte(nil), data...)
	reclassify(f, pid, data)
}

// noteUnreachable records that session's proxy circuit breaker is open for
// this device, so the UI can say so instead of showing a half-filled row.
//
// It does not touch Params or any *Known flag: the device has answered
// nothing, and nothing about it has been settled.
func (reg *Registry) noteUnreachable(node session.NodeRef, uid rdm.UID, due *session.DeviceUnreachableError) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	f := reg.getOrCreateLocked(node.Key, node.Port, uid, time.Now())
	if !f.ProxyUnreachable {
		f.ProxyUnreachable = true
		f.ProxyUnreachableSince = time.Now()
	}
	f.ProxyRetryAt = due.RetryAt
	f.ProxyRefusals = due.Refusals
	// LastSeen is deliberately NOT touched. The device has not been seen;
	// the node's ToD still lists it, which is what put the row on screen in
	// the first place.
}

// noteReachable clears the unreachable flag after the device answers.
// ProxyRefusals is kept as a record that this path has had trouble.
func (reg *Registry) noteReachable(node session.NodeRef, uid rdm.UID) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	f := reg.getOrCreateLocked(node.Key, node.Port, uid, time.Now())
	f.ProxyUnreachable = false
	f.ProxyUnreachableSince = time.Time{}
	f.ProxyRetryAt = time.Time{}
}

// noteParamNack records that a GET for pid completed with a NACK, for the
// handful of PIDs the Devices screen needs "attempted, device doesn't
// report this" tracking for (MANUFACTURER_LABEL, DEVICE_MODEL_DESCRIPTION —
// see Fixture's *Known field docs). Other PIDs' NACKs are intentionally not
// tracked here; nothing downstream needs them yet.
func (reg *Registry) noteParamNack(node session.NodeRef, uid rdm.UID, pid rdm.ParameterID) {
	if pid != rdm.PIDManufacturerLabel && pid != rdm.PIDDeviceModelDescription {
		return
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	f := reg.getOrCreateLocked(node.Key, node.Port, uid, time.Now())
	f.LastSeen = time.Now()
	switch pid {
	case rdm.PIDManufacturerLabel:
		f.ManufacturerLabelKnown = true
	case rdm.PIDDeviceModelDescription:
		f.ModelDescriptionKnown = true
	}
}

// reclassify updates f's DeviceClass and the raw signals it's derived from
// whenever a param cache write touches one of the three PIDs report §1.3
// keys classification on: DEVICE_INFO (product_category, dmx_footprint),
// PRODUCT_DETAIL_ID_LIST (fallback fine-grained signal) and
// PROXIED_DEVICE_COUNT (the strongest "this is a wireless proxy" signal).
// It also opportunistically caches DEVICE_INFO's model ID and the device's
// own MANUFACTURER_LABEL/DEVICE_MODEL_DESCRIPTION reports for the Devices
// screen's Manufacturer/Model columns (task ask) — those two don't feed
// ClassifyDevice, so they return early rather than falling into the
// re-classify call at the bottom. Decode failures are ignored — a malformed
// cached blob simply doesn't move the classification, rather than erroring
// the whole cache write.
func reclassify(f *Fixture, pid rdm.ParameterID, data []byte) {
	switch pid {
	case rdm.PIDDeviceInfo:
		if di, err := decodeDeviceInfoFields(data); err == nil {
			f.ProductCategory = di.category
			f.DMXFootprint = di.dmxFootprint
			f.DeviceModelID = di.deviceModelID
			f.SubDeviceCount = di.subDeviceCount
			f.HasDeviceInfo = true
		}
	case rdm.PIDProductDetailIDList:
		if details, err := rdm.DecodeProductDetailIDList(data); err == nil {
			f.ProductDetails = details
		}
	case rdm.PIDProxiedDeviceCount:
		// A non-empty proxied-device count is the report's strongest
		// "acting as an RDM proxy" signal. PROXIED_DEVICE_COUNT's parameter
		// data is the confirmed 3-byte shape (E1.20 §8.4.1: UINT16 count +
		// 1-byte List Change flag — internal/rdm/proxy.go); decode it fully
		// so the structured proxiedDeviceCount/proxiedDeviceCountKnown
		// fields (Fixture, above) have real data, not just the classifier
		// boolean this case used to stop at.
		if pdc, err := rdm.DecodeProxiedDeviceCount(data); err == nil {
			f.ProxiedDeviceCount = pdc.Count
			f.ProxiedDeviceCountKnown = true
			f.ProxiedListChanged = pdc.ListChanged
			if pdc.Count > 0 {
				f.IsWirelessProxy = true
			}
		}
	case rdm.PIDManufacturerLabel:
		f.ManufacturerLabel = string(data)
		f.ManufacturerLabelKnown = true
		return
	case rdm.PIDDeviceModelDescription:
		f.ModelDescription = string(data)
		f.ModelDescriptionKnown = true
		return
	default:
		return
	}
	f.Class = ClassifyDevice(f.ProductCategory, f.ProductDetails, f.DMXFootprint, f.IsWirelessProxy)
}

// deviceInfoFields is the subset of DEVICE_INFO reclassify needs; decoded
// locally rather than importing package params (which already imports
// package session — importing it back here from registry would be fine
// today, but keeping registry PID-decoding self-contained via package rdm
// directly avoids a needless params<->registry coupling for two fields).
type deviceInfoFields struct {
	category       rdm.ProductCategory
	dmxFootprint   uint16
	deviceModelID  uint16
	subDeviceCount uint16
}

func decodeDeviceInfoFields(data []byte) (deviceInfoFields, error) {
	if len(data) != 19 {
		return deviceInfoFields{}, fmt.Errorf("registry: DEVICE_INFO wants 19 bytes, got %d", len(data))
	}
	return deviceInfoFields{
		deviceModelID:  uint16(data[2])<<8 | uint16(data[3]),
		category:       rdm.ProductCategory(uint16(data[4])<<8 | uint16(data[5])),
		dmxFootprint:   uint16(data[10])<<8 | uint16(data[11]),
		subDeviceCount: uint16(data[16])<<8 | uint16(data[17]),
	}, nil
}

// NoteFixture is a direct-write path for callers (the demo driver, or a
// one-shot Discover result) that have UIDs in hand without going through the
// event stream.
func (reg *Registry) NoteFixture(node session.NodeRef, uid rdm.UID) {
	reg.mergeToD(node, []rdm.UID{uid})
}

// ClearDevices wipes every discovered device (fixture/RDM responder) entry
// from the registry — the "wipe all ports" granularity of the
// discovered-RDM-device cache clear (task ask: "keep your logging +
// responses for now" — meaning this touches only the fixture table, never
// the capture rings, the RDM disk logger, or params' process-wide
// PARAMETER_DESCRIPTION cache/per-UID introspection state, all of which
// outlive this call by design). Does NOT touch the Art-Net node table
// either (session.ArtNetSession owns that separately; see its own
// ClearNodes, used only by the destructive full-reset flow, never by this
// cache clear). Returns the number of entries removed.
func (reg *Registry) ClearDevices() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	n := len(reg.fixtures)
	reg.fixtures = make(map[fixtureKey]*Fixture)
	return n
}

// ClearDevicesOnPort wipes every discovered device entry on one node port —
// the "wipe one port" granularity (task ask). Matched on (ip, Port-Address)
// alone, NOT BindIndex, mirroring session.RDMController's todKey (see its
// doc comment in internal/session/rdmdiscovery.go): a Port-Address is
// globally unambiguous on its own, and the /api/devices/clear request shape
// carries only ip+portAddress, no bind index. Returns the number of entries
// removed.
func (reg *Registry) ClearDevicesOnPort(ip netip.Addr, port artnet.PortAddress) int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	n := 0
	for k := range reg.fixtures {
		if k.ip == ip && k.port == port.RawValue() {
			delete(reg.fixtures, k)
			n++
		}
	}
	return n
}

// Nodes returns every known Art-Net node with its fixture count.
func (reg *Registry) Nodes() []NodeView {
	nodes := reg.artnet.Nodes()
	out := make([]NodeView, 0, len(nodes))
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	for _, n := range nodes {
		count := 0
		for k := range reg.fixtures {
			if k.ip == n.Key.IP && k.bind == n.Key.BindIndex {
				count++
			}
		}
		out = append(out, NodeView{Node: n, FixtureCount: count})
	}
	return out
}

// Fixtures returns every known fixture, optionally filtered to one node
// port. A zero NodeKey/PortAddress (IP invalid) means "all".
func (reg *Registry) Fixtures(node session.NodeKey, port artnet.PortAddress, filterByPort bool) []Fixture {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]Fixture, 0, len(reg.fixtures))
	for k, f := range reg.fixtures {
		if filterByPort {
			if k.ip != node.IP || k.bind != node.BindIndex || k.port != port.RawValue() {
				continue
			}
		}
		out = append(out, f.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID.Less(out[j].UID) })
	return out
}

// Fixture looks up a single fixture by UID (first match across any node
// port — RDM UIDs are globally unique in practice, and the UI addresses
// fixtures by UID alone on the Fixtures screen).
func (reg *Registry) Fixture(uid rdm.UID) (Fixture, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	for _, f := range reg.fixtures {
		if f.UID == uid {
			return f.clone(), true
		}
	}
	return Fixture{}, false
}

// FixtureNode resolves a UID to the NodeRef needed to send it RDM commands.
func (reg *Registry) FixtureNode(uid rdm.UID) (session.NodeRef, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	for _, f := range reg.fixtures {
		if f.UID == uid {
			node, ok := reg.artnet.Node(f.Node)
			addr := netip.AddrPortFrom(f.Node.IP, session.ArtNetUDPPort)
			if ok {
				addr = node.Addr
			}
			return session.NodeRef{Key: f.Node, Addr: addr, Port: f.Port}, true
		}
	}
	return session.NodeRef{}, false
}

// Devices is an alias for Fixtures using the vocabulary the report/feature
// C ask for: any discovered RDM responder, not just literal fixtures. New
// callers (the device-centric /api/device/... endpoints) should prefer
// this name; Fixtures is retained for the existing Fixtures-screen API.
func (reg *Registry) Devices() []Fixture {
	return reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)
}

// FixturesOnNode returns every device discovered on any port of the node
// identified by key (unlike Fixtures, which filters to one specific port
// when filterByPort is set) — the shape a node detail panel needs to list
// everything behind one gateway/node regardless of which of its ports each
// device answered on.
func (reg *Registry) FixturesOnNode(key session.NodeKey) []Fixture {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]Fixture, 0)
	for k, f := range reg.fixtures {
		if k.ip != key.IP || k.bind != key.BindIndex {
			continue
		}
		out = append(out, f.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID.Less(out[j].UID) })
	return out
}

// NodeSelfUID reports the RDM UID an Art-Net node advertises as its own
// (report feature C: "a node view should be able to show this node's own
// RDM parameters"), from ArtPollReply's DefaultRespUID field. See
// session.Node.DefaultRespUID's doc comment for the confirmation caveat —
// this is a best-effort hint, not a guarantee; callers should confirm by
// issuing DEVICE_INFO against the returned UID and checking for a
// DMXFootprint of 0 plus a Data-family product category before labeling a
// panel "this node's own RDM parameters" outright.
func (reg *Registry) NodeSelfUID(key session.NodeKey) (rdm.UID, bool) {
	node, ok := reg.artnet.Node(key)
	if !ok || node.DefaultRespUID == (rdm.UID{}) {
		return rdm.UID{}, false
	}
	return node.DefaultRespUID, true
}
