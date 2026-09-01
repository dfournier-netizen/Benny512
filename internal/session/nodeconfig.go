package session

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"benny512/internal/artnet"
)

// This file wires the three remote node-configuration packets
// (internal/artnet's Address/Input/IpProg — see that package's nodeconfig.go
// for their wire-format confidence notes) into ArtNetSession as four typed,
// confirmable calls: SetPortAddresses, SetNodeNames, SetInputEnabled,
// ProgramIP. Confirmation model: send the config packet unicast to the
// node's known address, then wait for its next ArtPollReply (nodes are
// expected, by Art-Net convention, to send one unsolicited after processing
// a config-changing packet) up to a timeout — WEAKLY CONFIRMED as a
// universal behavior; not every implementation replies promptly, or for
// every field, so Confirmed:false means "no reply seen in time," not
// "definitely failed."

// DefaultConfigConfirmTimeout bounds how long the Set*/ProgramIP calls below
// wait for a node's follow-up ArtPollReply.
const DefaultConfigConfirmTimeout = 3 * time.Second

// ConfigResult is the outcome of one node-configuration call.
type ConfigResult struct {
	Node      NodeKey
	Confirmed bool
	// Updated is the node's table entry as of the confirming ArtPollReply;
	// zero value when Confirmed is false.
	Updated Node
	Elapsed time.Duration
	// Warning carries a non-fatal caveat about the request that the caller
	// should surface to the user (e.g. ProgramIP's gateway limitation)
	// without failing the call outright.
	Warning string
}

func (s *ArtNetSession) registerConfigWaiter(key NodeKey) chan Node {
	ch := make(chan Node, 1)
	s.mu.Lock()
	s.configWaiters[key] = append(s.configWaiters[key], ch)
	s.mu.Unlock()
	return ch
}

func (s *ArtNetSession) unregisterConfigWaiter(key NodeKey, ch chan Node) {
	s.mu.Lock()
	waiters := s.configWaiters[key]
	for i, w := range waiters {
		if w == ch {
			s.configWaiters[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(s.configWaiters[key]) == 0 {
		delete(s.configWaiters, key)
	}
	s.mu.Unlock()
}

// registerIPProgTarget/unregisterIPProgTarget record, for the duration of
// one ProgramIP call, the specific new static address (if any) that call
// asked the node to move to — see ArtNetSession.ipProgTargets' doc comment
// and handleIPProgReply.
func (s *ArtNetSession) registerIPProgTarget(key NodeKey, target netip.Addr) {
	if !target.IsValid() {
		return
	}
	s.mu.Lock()
	s.ipProgTargets[key] = target
	s.mu.Unlock()
}

func (s *ArtNetSession) unregisterIPProgTarget(key NodeKey) {
	s.mu.Lock()
	delete(s.ipProgTargets, key)
	s.mu.Unlock()
}

// notifyConfigWaitersLocked delivers node to every pending waiter for key.
// Must be called with s.mu held (it's invoked from within HandlePollReply's
// locked section).
func (s *ArtNetSession) notifyConfigWaitersLocked(key NodeKey, node Node) {
	for _, ch := range s.configWaiters[key] {
		select {
		case ch <- node:
		default:
		}
	}
}

// sendAndAwaitConfirm unicasts wire to the node identified by key, then
// blocks until either its next ArtPollReply arrives (Confirmed:true), the
// timeout elapses (Confirmed:false, not an error — see file doc comment),
// or ctx is cancelled (returns ctx.Err()).
func (s *ArtNetSession) sendAndAwaitConfirm(ctx context.Context, key NodeKey, wire []byte, timeout time.Duration) (ConfigResult, error) {
	if timeout <= 0 {
		timeout = DefaultConfigConfirmTimeout
	}
	node, ok := s.Node(key)
	if !ok {
		return ConfigResult{}, fmt.Errorf("session: unknown node %v", key)
	}
	start := s.cfg.Clock.Now()
	ch := s.registerConfigWaiter(key)
	defer s.unregisterConfigWaiter(key, ch)

	if err := s.cfg.Transport.Send(wire, node.Addr); err != nil {
		return ConfigResult{}, err
	}

	timedOut := make(chan struct{})
	timer := s.cfg.Clock.AfterFunc(timeout, func() { close(timedOut) })
	defer timer.Stop()

	select {
	case updated := <-ch:
		return ConfigResult{Node: key, Confirmed: true, Updated: updated, Elapsed: s.cfg.Clock.Now().Sub(start)}, nil
	case <-timedOut:
		return ConfigResult{Node: key, Confirmed: false, Elapsed: s.cfg.Clock.Now().Sub(start)}, nil
	case <-ctx.Done():
		return ConfigResult{}, ctx.Err()
	}
}

func normalizedBindIndex(b byte) byte {
	if b == 0 {
		return 1
	}
	return b
}

func switchEntryFor(v *byte) artnet.SwitchEntry {
	if v == nil {
		return artnet.NoChangeSwitch
	}
	return artnet.ProgramSwitch(*v)
}

// PortAddressUpdate describes one ArtAddress programming request for
// SetPortAddresses. A nil pointer field means "leave this value unchanged"
// (SwitchEntry's bit-7 write-enable convention — see internal/artnet's
// nodeconfig.go). SwIn/SwOut entries are per-port Universe nibbles (0-15).
type PortAddressUpdate struct {
	NetSwitch *byte // 0-127
	SubSwitch *byte // 0-15
	SwIn      [4]*byte
	SwOut     [4]*byte
	// Command carries a single extra action alongside the programming
	// (merge LTP/HTP select, cancel merge, clear output buffer, protocol
	// select — see artnet.AcCommand). artnet.AcNone for "no extra action."
	Command artnet.AcCommand
}

// SetPortAddresses issues ArtAddress to program net/sub-net/per-port
// universe assignment (and optionally one AcCommand action) on the node
// identified by key, and waits for its follow-up ArtPollReply. Names are
// preserved from the node's currently-advertised ShortName/LongName — use
// SetPortAddressesAndNames to change names in the same packet.
func (s *ArtNetSession) SetPortAddresses(ctx context.Context, key NodeKey, update PortAddressUpdate) (ConfigResult, error) {
	node, ok := s.Node(key)
	if !ok {
		return ConfigResult{}, fmt.Errorf("session: unknown node %v", key)
	}
	return s.SetPortAddressesAndNames(ctx, key, update, node.ShortName, node.LongName)
}

// SetPortAddressesAndNames is SetPortAddresses plus explicit control over
// the same ArtAddress packet's ShortName/LongName fields — ArtAddress
// carries names and addressing together in one packet always (report/
// nodeconfig.go's WEAKLY CONFIRMED reading: there is no "leave name
// unchanged" sentinel, so a caller that wants to touch only addressing
// must still supply the names it wants preserved, which SetPortAddresses
// does automatically by reading them off the node table).
func (s *ArtNetSession) SetPortAddressesAndNames(ctx context.Context, key NodeKey, update PortAddressUpdate, shortName, longName string) (ConfigResult, error) {
	if _, ok := s.Node(key); !ok {
		return ConfigResult{}, fmt.Errorf("session: unknown node %v", key)
	}
	p := artnet.Address{
		ProtocolVersion: s.cfg.ProtocolVersion,
		BindIndex:       normalizedBindIndex(key.BindIndex),
		NetSwitch:       switchEntryFor(update.NetSwitch),
		SubSwitch:       switchEntryFor(update.SubSwitch),
		Command:         update.Command,
		ShortName:       shortName,
		LongName:        longName,
		// AcnPriority (wire offset 105) is sACN priority, not a spare byte
		// — leaving this at Go's zero value (0) would ask a real node to
		// reprogram its sACN priority to 0 on every call. 255 means "no
		// change" per the Art-Net 4 spec.
		AcnPriority: artnet.AcnPriorityNoChange,
	}
	for i := 0; i < 4; i++ {
		p.SwIn[i] = switchEntryFor(update.SwIn[i])
		p.SwOut[i] = switchEntryFor(update.SwOut[i])
	}
	wire := artnet.Encode(artnet.Packet{Kind: artnet.KindAddress, Address: p})
	return s.sendAndAwaitConfirm(ctx, key, wire, 0)
}

// SetNodeNames issues ArtAddress to program a node's ShortName/LongName,
// leaving Net/Sub-Net/per-port addressing untouched, and waits for its
// follow-up ArtPollReply.
func (s *ArtNetSession) SetNodeNames(ctx context.Context, key NodeKey, shortName, longName string) (ConfigResult, error) {
	p := artnet.Address{
		ProtocolVersion: s.cfg.ProtocolVersion,
		BindIndex:       normalizedBindIndex(key.BindIndex),
		NetSwitch:       artnet.NoChangeSwitch,
		SubSwitch:       artnet.NoChangeSwitch,
		ShortName:       shortName,
		LongName:        longName,
		Command:         artnet.AcNone,
		AcnPriority:     artnet.AcnPriorityNoChange,
	}
	for i := range p.SwIn {
		p.SwIn[i] = artnet.NoChangeSwitch
		p.SwOut[i] = artnet.NoChangeSwitch
	}
	wire := artnet.Encode(artnet.Packet{Kind: artnet.KindAddress, Address: p})
	return s.sendAndAwaitConfirm(ctx, key, wire, 0)
}

// SetInputEnabled issues ArtInput to enable/disable DMX input per port
// (index 0-3; enabled[i]==false disables that port's input) and waits for
// the node's follow-up ArtPollReply.
func (s *ArtNetSession) SetInputEnabled(ctx context.Context, key NodeKey, enabled [4]bool) (ConfigResult, error) {
	p := artnet.Input{ProtocolVersion: s.cfg.ProtocolVersion, BindIndex: normalizedBindIndex(key.BindIndex)}
	for i := 0; i < 4; i++ {
		if !enabled[i] {
			p.InputStates[i] = 0x01
		}
	}
	wire := artnet.Encode(artnet.Packet{Kind: artnet.KindInput, Input: p})
	return s.sendAndAwaitConfirm(ctx, key, wire, 0)
}

// ProgramIP issues ArtIpProg to remotely reconfigure a node's IPv4 address,
// subnet mask, default gateway, and DHCP mode, and waits for its
// ArtIpProgReply (which, like ArtPollReply, is treated as confirmation via
// the same waiter mechanism — a node is expected to also emit a fresh
// ArtPollReply after an IP change since its address just moved).
//
// Command-bit construction here was the site of a real bench-confirmed bug
// (RDM-LOG19, 2026-09-01): the previous artnet.IpProgProgramIP/
// IpProgSetDefault constants were on the wrong bits (a spec-transcription
// error, not a bug in this function), so every call here built a Command
// byte that told a real Netron EN4 to "program subnet mask + program UDP
// port to 0" while never actually asking it to change its IP. That is now
// fixed at the constant definitions (internal/artnet/nodeconfig.go) — this
// function's logic (set the bit, fill the field, only for values the caller
// actually supplied) was already correct and needed no change beyond adding
// gateway support below.
//
// Never programs the deprecated UDP-port field (bit 0) — this build does
// not offer port programming at all, per the Art-Net 4 spec marking it
// deprecated.
func (s *ArtNetSession) ProgramIP(ctx context.Context, key NodeKey, ip, mask, gateway netip.Addr, dhcp bool) (ConfigResult, error) {
	cmd := artnet.IpProgEnable
	var progIP, progSM, progGW [4]byte
	if dhcp {
		cmd |= artnet.IpProgEnableDHCP
	} else {
		if ip.Is4() {
			progIP = ip.As4()
			cmd |= artnet.IpProgProgramIP
		}
		if mask.Is4() {
			progSM = mask.As4()
			cmd |= artnet.IpProgProgramSubnetMask
		}
		if gateway.Is4() {
			progGW = gateway.As4()
			cmd |= artnet.IpProgProgramGateway
		}
	}
	p := artnet.IpProg{
		ProtocolVersion: s.cfg.ProtocolVersion, Command: cmd,
		ProgIP: progIP, ProgSubnetMask: progSM, ProgGateway: progGW,
	}
	wire := artnet.Encode(artnet.Packet{Kind: artnet.KindIpProg, IpProg: p})

	// A static-IP request that supplies a new address is the one case where
	// the node's confirming reply may legitimately arrive from somewhere
	// other than key.IP (see handleIPProgReply) — record the target so that
	// reply is still recognised as confirmation, not silence.
	if !dhcp && ip.Is4() {
		s.registerIPProgTarget(key, ip)
		defer s.unregisterIPProgTarget(key)
	}
	return s.sendAndAwaitConfirm(ctx, key, wire, 0)
}
