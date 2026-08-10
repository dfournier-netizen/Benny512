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
	startedAt time.Time
	timer     Timer
	gen       uint64
	finished  bool
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
	return c.startDiscovery(node, artnet.Encode(pkt))
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
	return c.startDiscovery(node, artnet.Encode(pkt))
}

func (c *RDMController) startDiscovery(node NodeRef, wire []byte) *Discovery {
	key := todKey{ip: node.Key.IP, port: node.Port.RawValue()}
	d := &Discovery{
		node:     node,
		key:      key,
		done:     make(chan DiscoveryResult, 1),
		seen:     make(map[rdm.UID]struct{}),
		blockIDs: make(map[byte]struct{}),
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

func sortedUIDs(set map[rdm.UID]struct{}) []rdm.UID {
	out := make([]rdm.UID, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}
