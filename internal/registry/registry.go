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
	"net/netip"
	"sort"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// Fixture is one RDM responder discovered on a node port.
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
}

// clone returns a deep-enough copy for safe hand-out across the mutex
// boundary (Params is a map and must not alias the registry's copy).
func (f Fixture) clone() Fixture {
	cp := f
	cp.Params = make(map[rdm.ParameterID][]byte, len(f.Params))
	for k, v := range f.Params {
		cp.Params[k] = append([]byte(nil), v...)
	}
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
type Registry struct {
	artnet *session.ArtNetSession
	rdmc   *session.RDMController

	mu       sync.RWMutex
	fixtures map[fixtureKey]*Fixture
}

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
	}
}

// Run consumes both engines' event channels until they close. It is meant
// to run on its own goroutine for the life of the process; node events
// require no action here (Nodes() reads straight through to the session),
// but ToD updates populate/prune the fixture table.
func (reg *Registry) Run() {
	nodeEvents := reg.artnet.Events()
	rdmEvents := reg.rdmc.Events()
	for nodeEvents != nil || rdmEvents != nil {
		select {
		case ev, ok := <-nodeEvents:
			if !ok {
				nodeEvents = nil
				continue
			}
			_ = ev // node table itself lives in ArtNetSession; nothing to merge
		case ev, ok := <-rdmEvents:
			if !ok {
				rdmEvents = nil
				continue
			}
			reg.handleRDMEvent(ev)
		}
	}
}

func (reg *Registry) handleRDMEvent(ev session.Event) {
	switch ev.Kind {
	case session.EventToDUpdate:
		reg.mergeToD(ev.Node, ev.UIDs)
	case session.EventCommandComplete:
		if ev.Result != nil && ev.Result.Kind == session.ResultAck && len(ev.Result.Data) > 0 {
			reg.cacheParam(ev.Node, ev.UID, ev.Result.Request.PID, ev.Result.Data)
		}
	}
}

func (reg *Registry) mergeToD(node session.NodeRef, uids []rdm.UID) {
	now := time.Now()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, uid := range uids {
		key := fixtureKey{ip: node.Key.IP, bind: node.Key.BindIndex, port: node.Port.RawValue(), uid: uid}
		f, ok := reg.fixtures[key]
		if !ok {
			f = &Fixture{
				UID:              uid,
				ManufacturerID:   uid.ManufacturerID,
				ManufacturerName: ManufacturerName(uid.ManufacturerID),
				Node:             node.Key,
				Port:             node.Port,
				FirstSeen:        now,
				Params:           make(map[rdm.ParameterID][]byte),
			}
			reg.fixtures[key] = f
		}
		f.LastSeen = now
	}
}

func (reg *Registry) cacheParam(node session.NodeRef, uid rdm.UID, pid rdm.ParameterID, data []byte) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	key := fixtureKey{ip: node.Key.IP, bind: node.Key.BindIndex, port: node.Port.RawValue(), uid: uid}
	f, ok := reg.fixtures[key]
	if !ok {
		f = &Fixture{
			UID:              uid,
			ManufacturerID:   uid.ManufacturerID,
			ManufacturerName: ManufacturerName(uid.ManufacturerID),
			Node:             node.Key,
			Port:             node.Port,
			FirstSeen:        time.Now(),
			Params:           make(map[rdm.ParameterID][]byte),
		}
		reg.fixtures[key] = f
	}
	f.LastSeen = time.Now()
	f.Params[pid] = append([]byte(nil), data...)
}

// NoteFixture is a direct-write path for callers (the demo driver, or a
// one-shot Discover result) that have UIDs in hand without going through the
// event stream.
func (reg *Registry) NoteFixture(node session.NodeRef, uid rdm.UID) {
	reg.mergeToD(node, []rdm.UID{uid})
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
