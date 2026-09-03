package session

import (
	"context"
	"net/netip"
	"sort"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// ArtTodControl / ArtTodRequest / ArtTodData command codes.
const (
	// AtcNone is ArtTodControl's no-op command.
	AtcNone byte = 0x00
	// AtcFlush tells the node to flush its Table of Devices and run a full
	// RDM discovery on the addressed port (protocol reference §3.5 step 1).
	AtcFlush byte = 0x01

	// TodFull is ArtTodRequest's "send me your ToD" command and
	// ArtTodData's "this is the complete list" response.
	TodFull byte = 0x00
	// TodNak is ArtTodData's "discovery did not complete" response.
	TodNak byte = 0xFF
)

// todKey identifies a Table of Devices: one node port.
//
// It is keyed by source IP and Port-Address only, NOT by BindIndex: Art-Net 4
// added a BindIndex to ArtTodData but its byte offset is unresolved in the
// codec layer (see artnet.TodData's doc comment; the 7 reserved bytes are
// carried verbatim as Spare). Keying on Port-Address is unambiguous anyway,
// since a bind group's ports have distinct Port-Addresses. Revisit in Phase
// 1d once real EN4 captures settle the offset.
type todKey struct {
	ip   netip.Addr
	port uint16
}

type todEntry struct {
	uids     []rdm.UID
	complete bool
	updated  time.Time
}

// DiscoveryResult is the outcome of a Table of Devices assembly.
type DiscoveryResult struct {
	Node NodeRef
	// UIDs is the assembled table, sorted and de-duplicated. It is
	// populated even on a partial/failed result, so a timeout still yields
	// whatever was received.
	UIDs []rdm.UID
	// Complete is true when UidTotal UIDs were assembled.
	Complete bool
	// Blocks counts ArtTodData packets that contributed to this table.
	Blocks int
	// DistinctBlockIDs counts the distinct BlockCount values seen, which is
	// less than Blocks when a node duplicates or retransmits a block.
	DistinctBlockIDs int
	Elapsed          time.Duration
	Err              error
}

// Discovery is the handle returned by Discover / RequestToD.
type Discovery struct {
	node NodeRef
	key  todKey
	done chan DiscoveryResult

	// --- controller-owned state, guarded by RDMController.mu ---
	seen map[rdm.UID]struct{}
	// blockIDs records which BlockCount values have been seen. Nodes vary
	// in whether BlockCount is an index or a running total, so it is kept
	// for diagnostics only — completion is decided by the UID count.
	blockIDs  map[byte]struct{}
	blocks    int
	total     uint16
	haveTotal bool
	// forcedFlush records that this discovery began with an ArtTodControl
	// AtcFlush ("flush your ToD and run a full discovery") rather than a
	// plain ArtTodRequest ("send me the table you already have"). It is the
	// difference between the two meanings of an empty ToD — see the
	// completion rule in HandleTodData.
	forcedFlush bool
	// sawTodData records that the node answered at all, which is a different
	// fact from what it answered WITH. On the discovery timeout it separates
	// "the node reported an empty table and nothing more arrived" from "the
	// node never said anything", and RDM-LOG25 contains one of each: a flush
	// to universe 11 that was answered empty, and a flush to universe 3 that
	// drew no reply in the whole capture. Reporting both as the same timeout
	// would hide the more diagnostic of the two.
	sawTodData bool
	startedAt  time.Time
	timer      Timer
	gen        uint64
	finished   bool
}

// Done delivers the DiscoveryResult exactly once, then closes.
func (d *Discovery) Done() <-chan DiscoveryResult { return d.done }

// Await blocks until discovery completes or ctx is cancelled.
func (d *Discovery) Await(ctx context.Context) (DiscoveryResult, error) {
	select {
	case r := <-d.done:
		return r, nil
	case <-ctx.Done():
		return DiscoveryResult{}, ctx.Err()
	}
}

// Node returns the node port this discovery targets.
func (d *Discovery) Node() NodeRef { return d.node }

// Discover asks a node to flush its Table of Devices and run a fresh RDM
// discovery on the given port (ArtTodControl / AtcFlush), then assembles the
// resulting ArtTodData blocks.
func (c *RDMController) Discover(node NodeRef) *Discovery {
	pkt := artnet.Packet{Kind: artnet.KindTodControl, TodControl: artnet.TodControl{
		ProtocolVersion: c.cfg.ProtocolVersion,
		Net:             node.Port.Net,
		Command:         AtcFlush,
		Address:         node.Port.SubUni(),
	}}
	return c.startDiscovery(node, artnet.Encode(pkt), true)
}

// RequestToD asks a node for its cached Table of Devices without forcing a
// new discovery (ArtTodRequest).
func (c *RDMController) RequestToD(node NodeRef) *Discovery {
	pkt := artnet.Packet{Kind: artnet.KindTodRequest, TodRequest: artnet.TodRequest{
		ProtocolVersion: c.cfg.ProtocolVersion,
		Net:             node.Port.Net,
		Command:         TodFull,
		Address:         []byte{node.Port.SubUni()},
	}}
	return c.startDiscovery(node, artnet.Encode(pkt), false)
}

func (c *RDMController) startDiscovery(node NodeRef, wire []byte, forcedFlush bool) *Discovery {
	key := todKey{ip: node.Key.IP, port: node.Port.RawValue()}
	d := &Discovery{
		node:        node,
		key:         key,
		done:        make(chan DiscoveryResult, 1),
		seen:        make(map[rdm.UID]struct{}),
		blockIDs:    make(map[byte]struct{}),
		forcedFlush: forcedFlush,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		d.finished = true
		d.done <- DiscoveryResult{Node: node, Err: ErrControllerStopped}
		close(d.done)
		return d
	}
	// One discovery per node port: a second request supersedes the first,
	// which is what a user hammering "Discover" produces.
	if prev, ok := c.discoveries[key]; ok {
		c.finishDiscoveryLocked(prev, ErrTodTimeout)
	}
	d.startedAt = c.cfg.Clock.Now()
	c.discoveries[key] = d
	c.armDiscoveryTimerLocked(d)

	_ = c.cfg.Transport.Send(wire, node.Addr)
	return d
}

func (c *RDMController) armDiscoveryTimerLocked(d *Discovery) {
	d.gen++
	gen := d.gen
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = c.cfg.Clock.AfterFunc(c.cfg.DiscoveryTimeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if d.finished || d.gen != gen {
			return
		}
		// The node answering an empty table and then going quiet is a
		// RESULT, not a failure: the port really has nothing on it. The node
		// never answering at all is a genuine timeout, and usually means the
		// port is not carrying that universe — RDM-LOG25 contains one of
		// each, and collapsing them into the same error would throw away the
		// more diagnostic half.
		// The test is the node's OWN count, not merely "did it answer":
		// satisfied means we hold everything it said it had, which for an
		// empty table is trivially true and for a partial one is false. A
		// node that promised 3 UIDs and sent 1 has genuinely failed to
		// deliver and must still surface as ErrTodTimeout.
		if d.sawTodData && d.haveTotal && len(d.seen) >= int(d.total) {
			c.finishDiscoveryLocked(d, nil)
			return
		}
		c.finishDiscoveryLocked(d, ErrTodTimeout)
	})
}

// HandleTodData folds one ArtTodData packet into the ToD cache and into any
// discovery in progress for that node port.
//
// Block handling is deliberately set-based rather than index-based: a
// multi-block reply is assembled by de-duplicating UIDs and comparing the
// count against UidTotal, so blocks arriving out of order, duplicated, or
// with an unhelpful BlockCount all converge on the same table.
func (c *RDMController) HandleTodData(td artnet.TodData, from netip.AddrPort) {
	ip := from.Addr()
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	port := artnet.MaskPortAddress(td.Net, (td.Address>>4)&0x0F, td.Address&0x0F)
	key := todKey{ip: ip, port: port.RawValue()}

	c.mu.Lock()
	defer c.mu.Unlock()

	node := NodeRef{Key: NodeKey{IP: ip, BindIndex: 1}, Addr: from, Port: port}
	d := c.discoveries[key]
	if d != nil {
		node = d.node
	}

	if td.CommandResponse == TodNak {
		if d != nil {
			c.finishDiscoveryLocked(d, ErrTodNak)
		} else {
			c.emitLocked(Event{Kind: EventToDUpdate, Node: node, Complete: false, At: c.cfg.Clock.Now()})
		}
		return
	}

	if d == nil {
		// Unsolicited ArtTodData (a node announcing a discovery it ran on
		// its own). Refresh the cache so the UI still learns about it.
		c.mergeUnsolicitedTodLocked(key, node, td)
		return
	}

	d.blocks++
	d.sawTodData = true
	d.blockIDs[td.BlockCount] = struct{}{}
	if !d.haveTotal || td.UidTotal > d.total {
		d.total = td.UidTotal
		d.haveTotal = true
	}
	for _, uid := range td.Tod {
		d.seen[uid] = struct{}{}
	}

	uids := sortedUIDs(d.seen)
	complete := d.haveTotal && len(uids) >= int(d.total)
	// An EMPTY table is not a completion signal for a flush-initiated
	// discovery, and this is the whole of bench capture RDM-LOG25's defect.
	//
	// AtcFlush means "flush your Table of Devices and run a full discovery".
	// The node therefore has an empty table the instant it obeys, and the
	// first thing it can say is "I have nothing" — LOG25 shows exactly that,
	// 225ms after the flush. A real RDM discovery walks a 48-bit UID space
	// with DISC_UNIQUE_BRANCH and takes SECONDS; nothing meaningful can have
	// happened in a quarter of a second. The real table follows.
	//
	// The old rule was `len(uids) >= int(d.total)`, and with total 0 and no
	// UIDs that is `0 >= 0` — true. So discovery closed as successfully
	// complete with zero fixtures, abandoning the 20-second window that
	// exists precisely to let the node finish looking. The owner could not
	// discover a rig of Elation Paladin Cubes on any port with any cable,
	// because neither the port nor the cable was the variable.
	//
	// A plain ArtTodRequest is the opposite case and must NOT be delayed:
	// nothing was flushed, the node is reporting the table it already holds,
	// and an empty answer there is real and final. Hence forcedFlush rather
	// than a blanket "never complete empty" — making the user wait out the
	// discovery window for a cached empty table would be its own regression.
	//
	// This is the tenth instance on this project of one defect class: a
	// plausible zero mistaken for a real answer. `uidTotal=0` carries two
	// entirely different meanings — "I have no devices" and "I have not
	// looked yet" — and immediately after a flush it is always the second.
	if complete && d.forcedFlush && len(uids) == 0 {
		complete = false
	}
	c.tod[key] = &todEntry{uids: uids, complete: complete, updated: c.cfg.Clock.Now()}
	c.emitLocked(Event{Kind: EventToDUpdate, Node: node, UIDs: uids, Complete: complete, At: c.cfg.Clock.Now()})

	if complete {
		c.finishDiscoveryLocked(d, nil)
		return
	}
	// Progress restarts the quiet-period timer.
	c.armDiscoveryTimerLocked(d)
}

func (c *RDMController) mergeUnsolicitedTodLocked(key todKey, node NodeRef, td artnet.TodData) {
	entry := c.tod[key]
	seen := make(map[rdm.UID]struct{})
	if entry != nil && !entry.complete {
		// Still mid-sequence from a previous unsolicited block: keep
		// accumulating. A completed table is replaced wholesale, since a
		// fresh announcement supersedes it.
		for _, u := range entry.uids {
			seen[u] = struct{}{}
		}
	}
	for _, u := range td.Tod {
		seen[u] = struct{}{}
	}
	uids := sortedUIDs(seen)
	complete := len(uids) >= int(td.UidTotal)
	c.tod[key] = &todEntry{uids: uids, complete: complete, updated: c.cfg.Clock.Now()}
	c.emitLocked(Event{Kind: EventToDUpdate, Node: node, UIDs: uids, Complete: complete, At: c.cfg.Clock.Now()})
}

func (c *RDMController) finishDiscoveryLocked(d *Discovery, err error) {
	if d.finished {
		return
	}
	d.finished = true
	d.gen++
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if cur, ok := c.discoveries[d.key]; ok && cur == d {
		delete(c.discoveries, d.key)
	}
	uids := sortedUIDs(d.seen)
	res := DiscoveryResult{
		Node:             d.node,
		UIDs:             uids,
		Complete:         err == nil && d.haveTotal && len(uids) >= int(d.total),
		Blocks:           d.blocks,
		DistinctBlockIDs: len(d.blockIDs),
		Elapsed:          c.cfg.Clock.Now().Sub(d.startedAt),
		Err:              err,
	}
	d.done <- res
	close(d.done)
}

// ToD returns the cached Table of Devices for a node port.
func (c *RDMController) ToD(ip netip.Addr, port artnet.PortAddress) ([]rdm.UID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.tod[todKey{ip: ip, port: port.RawValue()}]
	if !ok {
		return nil, false
	}
	return append([]rdm.UID(nil), e.uids...), true
}

// ClearToD drops every cached Table of Devices entry, for every node port —
// the "wipe all ports" granularity of the discovered-RDM-device cache clear
// (task ask). This is a bare cache wipe, not the destructive Stop(): any
// discovery currently in flight is left running (its own eventual result
// simply repopulates c.tod for its key), and nothing about the command
// queues is touched. The next Discover for any port starts from an empty
// table instead of merging into stale entries. Returns the number of
// (ip,port) tables dropped.
func (c *RDMController) ClearToD() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.tod)
	c.tod = make(map[todKey]*todEntry)
	return n
}

// ClearToDPort drops the cached Table of Devices for one node port only —
// the "wipe one port" granularity (task ask). Keyed on (ip, Port-Address)
// alone, matching todKey (see its doc comment: BindIndex is deliberately
// not part of the key). Returns 1 if a cached table existed and was
// dropped, 0 if there was nothing cached for that port.
func (c *RDMController) ClearToDPort(ip netip.Addr, port artnet.PortAddress) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := todKey{ip: ip, port: port.RawValue()}
	if _, ok := c.tod[key]; !ok {
		return 0
	}
	delete(c.tod, key)
	return 1
}

func sortedUIDs(set map[rdm.UID]struct{}) []rdm.UID {
	out := make([]rdm.UID, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}
