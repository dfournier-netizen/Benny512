package session

import (
	"context"
	"net/netip"
	"sort"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// Default ArtNetSession timings. 3 s is Art-Net's recommended controller poll
// period; the spec's own liveness rule is "a node is considered gone if it
// has not replied to three polls", hence 3× by default.
const (
	DefaultPollInterval    = 3 * time.Second
	DefaultLivenessFactor  = 3
	DefaultEventBufferSize = 64
)

// NodeKey is a node's stable identity. Art-Net nodes with multiple bound
// port groups emit one ArtPollReply per group, all from the same IP,
// distinguished by BindIndex — so identity is (IP, BindIndex), not IP alone.
// BindIndex 0 (legacy nodes that don't implement it) and 1 both mean "the
// first/only bind", and are normalised to 1 so a node that starts reporting
// BindIndex mid-life doesn't duplicate itself in the table.
type NodeKey struct {
	IP        netip.Addr
	BindIndex byte
}

// NodeStyle is ArtPollReply's Style field (device category).
type NodeStyle byte

// Art-Net Style codes.
const (
	StyleNode       NodeStyle = 0x00
	StyleController NodeStyle = 0x01
	StyleMedia      NodeStyle = 0x02
	StyleRoute      NodeStyle = 0x03
	StyleBackup     NodeStyle = 0x04
	StyleConfig     NodeStyle = 0x05
	StyleVisual     NodeStyle = 0x06
)

// String renders the style for the UI.
func (s NodeStyle) String() string {
	switch s {
	case StyleNode:
		return "Node"
	case StyleController:
		return "Controller"
	case StyleMedia:
		return "Media Server"
	case StyleRoute:
		return "Router"
	case StyleBackup:
		return "Backup"
	case StyleConfig:
		return "Config Tool"
	case StyleVisual:
		return "Visualiser"
	default:
		return "Unknown"
	}
}

// NodePort is one of a node's up-to-four physical DMX ports, with the
// Port-Address it is patched to.
type NodePort struct {
	Index int // 0-3, as reported within this bind group
	// Type is PortTypes[i]: bit 7 = output (node→DMX), bit 6 = input
	// (DMX→node), bits 5-0 = protocol (0 = DMX512).
	Type        byte
	Input       bool
	Output      bool
	GoodInput   byte
	GoodOutputA byte
	GoodOutputB byte
	// InputAddress / OutputAddress are the full 15-bit Port-Addresses formed
	// from NetSwitch : SubSwitch : SwIn[i] / SwOut[i].
	InputAddress  artnet.PortAddress
	OutputAddress artnet.PortAddress
	// RDMEnabled decodes GoodOutputB bit 7 (1 = RDM disabled on this port).
	// Art-Net 4 only; nodes that predate GoodOutputB report zero, which
	// reads as "enabled" — the optimistic default, matching how nodes that
	// do support RDM but not the status bit behave in the field.
	RDMEnabled bool
}

// Node is one discovered Art-Net node (one ArtPollReply bind group).
type Node struct {
	Key  NodeKey
	Addr netip.AddrPort // where to unicast to this node

	ShortName        string
	LongName         string
	NodeReport       string
	EstaManufacturer uint16
	Oem              uint16
	Style            NodeStyle
	VersInfo         uint16
	MAC              [6]byte
	BindIP           [4]byte

	NetSwitch byte
	SubSwitch byte
	NumPorts  int
	Ports     []NodePort

	// DefaultRespUID is ArtPollReply's DefaultRespUID field: the UID the
	// node forwards RDM to when it has no better Port-Address routing for
	// it. In practice, many single-purpose gateways/nodes set this to their
	// own root RDM UID, which is what lets the UI offer "this node's own
	// RDM parameters" (report's feature-C node/RDM merge ask) — but that
	// reading is a field-observed convention, not something the Art-Net 4
	// spec text guarantees. WEAKLY CONFIRMED; treat a zero UID as "not
	// advertised" and confirm any non-zero value against a live DEVICE_INFO
	// GET before presenting it as authoritative (see registry.NodeSelfUID).
	DefaultRespUID rdm.UID

	Status1 byte
	Status2 byte
	Status3 byte

	// RDMCapable decodes Status1 bit 1 (1 = capable of RDM).
	RDMCapable bool
	// RDMDiscoveryRunning decodes Status3 bit 5 (1 = node's RDM discovery
	// is currently running; sending RDM at that moment is likely to NACK
	// with PROXY_REJECT or be dropped).
	RDMDiscoveryRunning bool

	FirstSeen time.Time
	LastSeen  time.Time
	Stale     bool
	Replies   uint64
}

// OutputUniverses returns the Port-Addresses this node transmits DMX on.
func (n Node) OutputUniverses() []artnet.PortAddress {
	out := make([]artnet.PortAddress, 0, len(n.Ports))
	for _, p := range n.Ports {
		if p.Output {
			out = append(out, p.OutputAddress)
		}
	}
	return out
}

// InputUniverses returns the Port-Addresses this node receives DMX on.
func (n Node) InputUniverses() []artnet.PortAddress {
	out := make([]artnet.PortAddress, 0, len(n.Ports))
	for _, p := range n.Ports {
		if p.Input {
			out = append(out, p.InputAddress)
		}
	}
	return out
}

// NodeEventKind classifies a NodeEvent.
type NodeEventKind int

// NodeEvent kinds.
const (
	// NodeAdded is emitted the first time a node is seen, and again when a
	// previously-lost node starts replying (the table entry is retained
	// across a loss so the UI can show "was here, gone now").
	NodeAdded NodeEventKind = iota
	// NodeUpdated is emitted when a reply changes any advertised field.
	// Plain refreshes that change nothing but LastSeen do NOT emit — this
	// keeps a 3 s poll cycle from spamming the WebSocket hub.
	NodeUpdated
	// NodeLost is emitted when a node misses the liveness window.
	NodeLost
)

// String renders the event kind.
func (k NodeEventKind) String() string {
	switch k {
	case NodeAdded:
		return "added"
	case NodeUpdated:
		return "updated"
	case NodeLost:
		return "lost"
	default:
		return "unknown"
	}
}

// NodeEvent is published on ArtNetSession.Events.
type NodeEvent struct {
	Kind NodeEventKind
	Node Node
	At   time.Time
}

// ArtNetConfig configures an ArtNetSession. The zero value of every field is
// replaced by a documented default.
type ArtNetConfig struct {
	Transport Transport
	Clock     Clock
	// PollInterval defaults to 3 s.
	PollInterval time.Duration
	// LivenessTimeout defaults to 3× PollInterval.
	LivenessTimeout time.Duration
	// ProtocolVersion defaults to artnet.DefaultProtocolVersion (14).
	ProtocolVersion uint16
	// PollFlags is ArtPoll's Flags byte; defaults to 0x02 (bit 1 = "send me
	// an ArtPollReply whenever your state changes"), which is what makes
	// node status changes visible between polls.
	PollFlags    byte
	DiagPriority byte
	// EventBuffer defaults to 64. Events are dropped (and counted) rather
	// than blocking the engine if a subscriber stalls.
	EventBuffer int
}

// ArtNetSession is the controller-side Art-Net node discovery and liveness
// engine: it broadcasts ArtPoll on a timer, folds ArtPollReply into a node
// table, and ages nodes out.
//
// State is mutex-guarded rather than goroutine-owned. The choice is
// deliberate: it keeps the inbound path a plain synchronous method
// (HandleInbound), which is what makes the state machine directly testable
// without a scheduler in the loop. Run is a thin pump over the transport
// channel for production use.
type ArtNetSession struct {
	cfg ArtNetConfig

	mu      sync.Mutex
	nodes   map[NodeKey]*Node
	started bool
	stopped bool
	timer   Timer
	polls   uint64
	dropped uint64

	// configWaiters holds pending SetPortAddresses/SetNodeNames/ProgramIP/
	// SetInputEnabled calls waiting for a node's follow-up ArtPollReply —
	// see nodeconfig.go.
	configWaiters map[NodeKey][]chan Node

	// ipProgTargets holds, for each NodeKey with an in-flight ProgramIP call
	// requesting a specific new static address, that requested address —
	// so handleIPProgReply can recognise a reply arriving FROM that new
	// address as confirmation of THIS waiter (registered under the node's
	// OLD key), not just a reply from the address the request was sent to.
	// Bench evidence (RDM-LOG20): a real EN4 programmed to a new IP replies
	// to ArtIpProg from its new address, since it has already moved — only
	// matching on the old key's IP silently discarded that reply. Populated/
	// cleared around ProgramIP's sendAndAwaitConfirm call (nodeconfig.go);
	// absent (or zero) for every other config call, which never move a
	// node's own address.
	ipProgTargets map[NodeKey]netip.Addr

	events chan NodeEvent
}

// NewArtNetSession builds a session. Transport and Clock are required.
func NewArtNetSession(cfg ArtNetConfig) *ArtNetSession {
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.LivenessTimeout <= 0 {
		cfg.LivenessTimeout = time.Duration(DefaultLivenessFactor) * cfg.PollInterval
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = artnet.DefaultProtocolVersion
	}
	if cfg.PollFlags == 0 {
		cfg.PollFlags = 0x02
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = DefaultEventBufferSize
	}
	return &ArtNetSession{
		cfg:           cfg,
		nodes:         make(map[NodeKey]*Node),
		configWaiters: make(map[NodeKey][]chan Node),
		ipProgTargets: make(map[NodeKey]netip.Addr),
		events:        make(chan NodeEvent, cfg.EventBuffer),
	}
}

// Events is the node add/update/loss stream consumed by the web layer.
func (s *ArtNetSession) Events() <-chan NodeEvent { return s.events }

// DroppedEvents counts events discarded because Events was full.
func (s *ArtNetSession) DroppedEvents() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// PollCount reports how many ArtPolls have been broadcast.
func (s *ArtNetSession) PollCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.polls
}

// Start broadcasts the first ArtPoll immediately and schedules the cycle.
func (s *ArtNetSession) Start() error {
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	err := s.pollLocked()
	s.scheduleLocked()
	s.mu.Unlock()
	return err
}

// Stop halts the poll cycle. The node table is retained.
func (s *ArtNetSession) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.started = false
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *ArtNetSession) scheduleLocked() {
	if !s.started {
		return
	}
	s.timer = s.cfg.Clock.AfterFunc(s.cfg.PollInterval, s.tick)
}

func (s *ArtNetSession) tick() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	_ = s.pollLocked()
	s.sweepLocked()
	s.scheduleLocked()
	s.mu.Unlock()
}

// PollNow broadcasts an ArtPoll immediately, outside the timer cycle (the
// UI's "refresh" button).
func (s *ArtNetSession) PollNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pollLocked()
}

func (s *ArtNetSession) pollLocked() error {
	poll := artnet.Poll{
		ProtocolVersion: s.cfg.ProtocolVersion,
		Flags:           s.cfg.PollFlags,
		DiagPriority:    s.cfg.DiagPriority,
	}
	s.polls++
	return s.cfg.Transport.Broadcast(artnet.Encode(artnet.Packet{Kind: artnet.KindPoll, Poll: poll}))
}

// sweepLocked ages out nodes whose last reply predates the liveness window.
func (s *ArtNetSession) sweepLocked() {
	now := s.cfg.Clock.Now()
	for _, n := range s.nodes {
		if n.Stale {
			continue
		}
		if now.Sub(n.LastSeen) > s.cfg.LivenessTimeout {
			n.Stale = true
			s.emitLocked(NodeEvent{Kind: NodeLost, Node: *n, At: now})
		}
	}
}

// Sweep runs the liveness check on demand (it otherwise runs once per poll
// tick, so loss detection granularity is one poll interval).
func (s *ArtNetSession) Sweep() {
	s.mu.Lock()
	s.sweepLocked()
	s.mu.Unlock()
}

// Run pumps the transport's inbound channel into HandleInbound until ctx is
// cancelled or the transport closes.
func (s *ArtNetSession) Run(ctx context.Context) error {
	in := s.cfg.Transport.Inbound()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p, ok := <-in:
			if !ok {
				return nil
			}
			s.HandleInbound(p)
		}
	}
}

// HandleInbound decodes one datagram and folds any ArtPollReply into the
// node table (also notifying config-waiters — see nodeconfig.go); an
// ArtIpProgReply notifies config-waiters too (its own confirmation signal
// for ProgramIP) without otherwise touching the node table, since it
// carries IP/subnet/DHCP status, not the full PollReply shape a Node needs.
// Non-Art-Net bytes and other packet types are ignored.
func (s *ArtNetSession) HandleInbound(in Inbound) {
	pkt, err := artnet.Decode(in.Data)
	if err != nil {
		return
	}
	switch pkt.Kind {
	case artnet.KindPollReply:
		s.HandlePollReply(pkt.PollReply, in.From)
	case artnet.KindIpProgReply:
		s.handleIPProgReply(pkt.IpProgReply, in.From)
	}
}

// handleIPProgReply notifies any pending ProgramIP waiter for the node this
// reply confirms, and — when the reply shows the node now living at a
// different address than its table entry's key — follows it there (see
// rekeyNodeLocked). It does not otherwise create or update a Node table
// entry — ArtIpProgReply doesn't carry a Node's full advertised shape — so
// the delivered Node is whatever this session already has on file for the
// (possibly just-moved) key, which is sufficient for ConfigResult's
// Confirmed:true signal even if Updated ends up mostly empty.
//
// Matching a reply to its waiter: a waiter registered under key matches a
// reply arriving from either key.IP (the address the request was unicast
// to — the common case: DHCP, or a node that hasn't moved yet) or
// ipProgTargets[key] (the specific new static address that request asked
// for, if any — RDM-LOG20's case: the node already moved and is answering
// from there). ArtIpProgReply carries no other correlator on the wire, so
// this is as precise as the protocol allows.
func (s *ArtNetSession) handleIPProgReply(reply artnet.IpProgReply, from netip.AddrPort) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The node's now-current IP as this reply itself reports it. CurrentIP
	// is authoritative when present; fall back to the reply's own UDP
	// source address otherwise (mirrors nodeFromPollReply's identical
	// fallback for ArtPollReply's IPAddress field).
	currentIP := netip.AddrFrom4(reply.CurrentIP)
	if !currentIP.IsValid() || currentIP.IsUnspecified() {
		currentIP = from.Addr()
	}

	for key, waiters := range s.configWaiters {
		target, hasTarget := s.ipProgTargets[key]
		if key.IP != from.Addr() && !(hasTarget && target == from.Addr()) {
			continue
		}
		s.rekeyNodeLocked(key, currentIP)
		lookupKey := NodeKey{IP: currentIP, BindIndex: key.BindIndex}
		node := Node{}
		if existing, ok := s.nodes[lookupKey]; ok {
			node = *existing
		} else if existing, ok := s.nodes[key]; ok {
			node = *existing
		}
		for _, ch := range waiters {
			select {
			case ch <- node:
			default:
			}
		}
	}
}

// rekeyNodeLocked moves every node table entry at oldKey.IP to the same
// BindIndex under newIP when the address has genuinely changed, preserving
// each entry's other fields (ports, names, MAC, …) rather than waiting for
// a fresh ArtPollReply to repopulate them. It moves ALL bind indices at
// that IP together, not just oldKey's own — ArtIpProg reprograms a whole
// physical device's IP, and BindIndex only distinguishes that one device's
// several logical port-groups (Art-Net nodes with multiple bound port
// groups emit one ArtPollReply per group, all from the same IP — see
// NodeKey's doc comment), so every bind index sharing the old IP moves with
// it even though only oldKey's own ProgramIP call had a pending waiter to
// confirm the change. (An earlier version of this rekeyed oldKey alone,
// which correctly moved bind 1 but left binds 2/3 of a 3-bind-index EN4
// stranded at the old IP — silently swallowing commands sent to them, and
// then duplicating once ArtPoll rediscovered them at the new address: both
// of the failure modes this function exists to avoid, just on the binds
// that weren't the one specific waiter's key.)
//
// Two failure modes this exists to avoid: leaving a stale entry in place,
// which would go on being the target of future commands sent into the void
// exactly as RDM-LOG20 bench-confirmed (two ArtIpProg retries addressed to
// the node's old, now-unreachable, IP got no reply at all); and ending up
// with a node listed twice — once stale at the old IP, once fresh at the
// new one once ArtPoll rediscovers it. Must be called with s.mu held.
func (s *ArtNetSession) rekeyNodeLocked(oldKey NodeKey, newIP netip.Addr) {
	oldIP := oldKey.IP
	if !newIP.IsValid() || newIP == oldIP {
		return
	}
	if _, ok := s.nodes[oldKey]; !ok {
		return
	}
	now := s.cfg.Clock.Now()
	for key, old := range s.nodes {
		if key.IP != oldIP {
			continue
		}
		newKey := NodeKey{IP: newIP, BindIndex: key.BindIndex}
		delete(s.nodes, key)
		if existing, exists := s.nodes[newKey]; exists {
			// A fresh ArtPollReply from the new address already beat this
			// confirmation in for this bind index — that data is more
			// current than what we'd copy from the old entry, so just drop
			// the now-deleted stale one and leave the newer one as-is.
			s.emitLocked(NodeEvent{Kind: NodeUpdated, Node: *existing, At: now})
			continue
		}
		moved := *old
		moved.Key = newKey
		moved.Addr = netip.AddrPortFrom(newIP, ArtNetUDPPort)
		moved.LastSeen = now
		s.nodes[newKey] = &moved
		s.emitLocked(NodeEvent{Kind: NodeUpdated, Node: moved, At: now})
	}
}

// HandlePollReply folds an already-decoded ArtPollReply into the table.
func (s *ArtNetSession) HandlePollReply(reply artnet.PollReply, from netip.AddrPort) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock.Now()
	node := nodeFromPollReply(reply, from)
	node.FirstSeen = now
	node.LastSeen = now
	node.Replies = 1

	existing, ok := s.nodes[node.Key]
	if !ok {
		copyNode := node
		s.nodes[node.Key] = &copyNode
		s.emitLocked(NodeEvent{Kind: NodeAdded, Node: copyNode, At: now})
		s.notifyConfigWaitersLocked(copyNode.Key, copyNode)
		return
	}

	wasStale := existing.Stale
	changed := nodeAdvertisedFieldsDiffer(*existing, node)

	node.FirstSeen = existing.FirstSeen
	node.Replies = existing.Replies + 1
	node.Stale = false
	*existing = node

	switch {
	case wasStale:
		s.emitLocked(NodeEvent{Kind: NodeAdded, Node: *existing, At: now})
	case changed:
		s.emitLocked(NodeEvent{Kind: NodeUpdated, Node: *existing, At: now})
	}
	// Any ArtPollReply following a config-change send is treated as
	// confirmation (report task ask: "confirms by observing the follow-up
	// ArtPollReply") — including one that reports no visible field change,
	// since some nodes echo back identical values for fields the app didn't
	// ask to change, and the app's own request may not touch anything
	// nodeAdvertisedFieldsDiffer compares (e.g. per-port universe changes
	// not yet reflected because the node debounces its reply).
	s.notifyConfigWaitersLocked(existing.Key, *existing)
}

// advertised is the comparable projection of a Node: everything the node
// itself puts on the wire, with the local bookkeeping fields (timestamps,
// counters, staleness) left out.
type advertised struct {
	Key                 NodeKey
	Addr                netip.AddrPort
	ShortName           string
	LongName            string
	NodeReport          string
	EstaManufacturer    uint16
	Oem                 uint16
	Style               NodeStyle
	VersInfo            uint16
	MAC                 [6]byte
	BindIP              [4]byte
	NetSwitch           byte
	SubSwitch           byte
	NumPorts            int
	DefaultRespUID      rdm.UID
	Status1             byte
	Status2             byte
	Status3             byte
	RDMCapable          bool
	RDMDiscoveryRunning bool
}

func advertisedOf(n Node) advertised {
	return advertised{
		Key: n.Key, Addr: n.Addr, ShortName: n.ShortName, LongName: n.LongName,
		NodeReport: n.NodeReport, EstaManufacturer: n.EstaManufacturer, Oem: n.Oem,
		Style: n.Style, VersInfo: n.VersInfo, MAC: n.MAC, BindIP: n.BindIP,
		NetSwitch: n.NetSwitch, SubSwitch: n.SubSwitch, NumPorts: n.NumPorts,
		DefaultRespUID: n.DefaultRespUID,
		Status1:        n.Status1, Status2: n.Status2, Status3: n.Status3,
		RDMCapable: n.RDMCapable, RDMDiscoveryRunning: n.RDMDiscoveryRunning,
	}
}

// nodeAdvertisedFieldsDiffer reports whether anything the node advertises
// has changed since the last reply.
func nodeAdvertisedFieldsDiffer(a, b Node) bool {
	if len(a.Ports) != len(b.Ports) {
		return true
	}
	for i := range a.Ports {
		if a.Ports[i] != b.Ports[i] {
			return true
		}
	}
	return advertisedOf(a) != advertisedOf(b)
}

func nodeFromPollReply(r artnet.PollReply, from netip.AddrPort) Node {
	ip := netip.AddrFrom4(r.IPAddress)
	if !ip.IsValid() || ip.IsUnspecified() {
		// Nodes that leave the IP field zero are identified by the datagram
		// source address instead.
		ip = from.Addr()
	}
	if ip.Is4In6() {
		ip = ip.Unmap()
	}

	bind := r.BindIndex
	if bind == 0 {
		bind = 1
	}

	numPorts := int(r.NumPorts)
	if numPorts > 4 {
		numPorts = 4
	}
	if numPorts < 0 {
		numPorts = 0
	}

	ports := make([]NodePort, 0, numPorts)
	for i := 0; i < numPorts; i++ {
		p := NodePort{
			Index:         i,
			Type:          r.PortTypes[i],
			Input:         r.PortTypes[i]&0x40 != 0,
			Output:        r.PortTypes[i]&0x80 != 0,
			GoodInput:     r.GoodInput[i],
			GoodOutputA:   r.GoodOutputA[i],
			GoodOutputB:   r.GoodOutputB[i],
			InputAddress:  artnet.MaskPortAddress(r.NetSwitch, r.SubSwitch, r.SwIn[i]),
			OutputAddress: artnet.MaskPortAddress(r.NetSwitch, r.SubSwitch, r.SwOut[i]),
			RDMEnabled:    r.GoodOutputB[i]&0x80 == 0,
		}
		ports = append(ports, p)
	}

	defaultRespUID, _ := rdm.UIDFromBytes(r.DefaultRespUID[:])

	return Node{
		Key:                 NodeKey{IP: ip, BindIndex: bind},
		Addr:                netip.AddrPortFrom(ip, ArtNetUDPPort),
		ShortName:           r.ShortName,
		LongName:            r.LongName,
		NodeReport:          r.NodeReport,
		EstaManufacturer:    r.EstaManufacturer,
		Oem:                 r.Oem,
		Style:               NodeStyle(r.Style),
		VersInfo:            uint16(r.VersInfoHi)<<8 | uint16(r.VersInfoLo),
		MAC:                 r.MAC,
		BindIP:              r.BindIP,
		NetSwitch:           r.NetSwitch & 0x7F,
		SubSwitch:           r.SubSwitch & 0x0F,
		NumPorts:            numPorts,
		Ports:               ports,
		DefaultRespUID:      defaultRespUID,
		Status1:             r.Status1,
		Status2:             r.Status2,
		Status3:             r.Status3,
		RDMCapable:          r.Status1&0x02 != 0,
		RDMDiscoveryRunning: r.Status3&0x20 != 0,
	}
}

func (s *ArtNetSession) emitLocked(ev NodeEvent) {
	select {
	case s.events <- ev:
	default:
		s.dropped++
	}
}

// Nodes returns a snapshot of the node table, sorted by IP then BindIndex.
func (s *ArtNetSession) Nodes() []Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if c := out[i].Key.IP.Compare(out[j].Key.IP); c != 0 {
			return c < 0
		}
		return out[i].Key.BindIndex < out[j].Key.BindIndex
	})
	return out
}

// LiveNodes returns only the non-stale nodes.
func (s *ArtNetSession) LiveNodes() []Node {
	all := s.Nodes()
	out := all[:0]
	for _, n := range all {
		if !n.Stale {
			out = append(out, n)
		}
	}
	return out
}

// ClearNodes wipes the entire Art-Net node table (full-reset flow, task
// ask: "everything" — NOT the discovered-RDM-device cache clear, which
// deliberately leaves the node table alone; see registry.Registry.
// ClearDevices' doc comment for that distinction). The poll cycle/liveness
// timer, if running, keeps ticking and will simply repopulate the table
// from scratch as fresh ArtPollReply packets arrive — nothing here touches
// started/stopped. Returns the number of nodes removed.
func (s *ArtNetSession) ClearNodes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.nodes)
	s.nodes = make(map[NodeKey]*Node)
	return n
}

// Node looks up a single node by key.
func (s *ArtNetSession) Node(key NodeKey) (Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[key]
	if !ok {
		return Node{}, false
	}
	return *n, true
}
