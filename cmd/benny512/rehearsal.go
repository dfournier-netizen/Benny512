package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/params"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
	"benny512/internal/web"
)

// makeRehearsalLauncher owns at most one temporary rehearsal. The HTTP UI is
// LAN-accessible like the parent, but the entire lighting side is FakeTransport.
// No runtime data paths or disk logger are installed on this child.
func makeRehearsalLauncher(parent context.Context) func(patch.Patch, string) (int, error) {
	var mu sync.Mutex
	var previous context.CancelFunc
	return func(p patch.Patch, fault string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		ctx, cancel := context.WithCancel(parent)
		srv, start, err := buildRehearsal(ctx, p, fault)
		if err != nil {
			cancel()
			return 0, err
		}
		listener, err := net.Listen("tcp", ":0")
		if err != nil {
			cancel()
			srv.Close()
			return 0, err
		}
		if previous != nil {
			previous()
		}
		previous = cancel
		srv.OnShutdownRequest = func(string) { cancel() }
		server := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			server.Close()
			srv.Close()
			srv.DMX.Stop()
			srv.Nodes.Stop()
			srv.RDM.Stop()
		}()
		start()
		go srv.Run(ctx)
		go func() { _ = server.Serve(listener); cancel() }()
		return listener.Addr().(*net.TCPAddr).Port, nil
	}
}

func buildRehearsal(ctx context.Context, original patch.Patch, fault string) (*web.Server, func(), error) {
	if len(original.Entries) == 0 || len(original.Entries) > 1024 {
		return nil, nil, fmt.Errorf("rehearsal requires 1–1024 patch entries")
	}
	if fault != "none" && fault != "missing" && fault != "address" && fault != "slow" {
		return nil, nil, fmt.Errorf("unknown rehearsal fault")
	}
	// Deep copy: synthetic identities/configuration must not mutate the real show.
	encoded, err := json.Marshal(original)
	if err != nil {
		return nil, nil, err
	}
	var p patch.Patch
	if err = json.Unmarshal(encoded, &p); err != nil {
		return nil, nil, err
	}
	p.Name = "REHEARSAL · " + p.Name
	tport := session.NewFakeTransport()
	clock := session.RealClock{}
	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: tport, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(capture.DefaultCapacity)
	rdmRing := capture.New(capture.DefaultRDMCapacity)
	srv := web.New(nodes, rdmc, dmx, reg, ring, rdmRing)
	srv.Simulation = true
	srv.NIC = "REHEARSAL — fake lighting transport"
	devices := make([]*demoDevice, 0, len(p.Entries))
	replies := map[uint16]artnet.PollReply{}
	usedUIDs := map[rdm.UID]bool{}
	for i, e := range p.Entries {
		pa, err := artnet.PortAddressFromRaw(e.Universe)
		if err != nil {
			return nil, nil, err
		}
		if e.StartAddress < 1 || e.StartAddress > 512 || e.Footprint > 512 {
			return nil, nil, fmt.Errorf("entry %s has invalid addressing; repair before rehearsal", e.ID)
		}
		ip := netip.AddrFrom4([4]byte{10, 254, byte(e.Universe >> 8), byte(e.Universe)})
		uid, valid := rdm.ParseUID(e.ConfirmedUID)
		if !valid || usedUIDs[uid] {
			uid = rdm.UID{ManufacturerID: 0x7ff0, DeviceID: uint32(i + 1)}
			for usedUIDs[uid] {
				uid.DeviceID++
			}
		}
		usedUIDs[uid] = true
		// Preserve uncommitted entries so their normal matching workflow remains.
		if e.ConfirmedUID != "" {
			p.Entries[i].ConfirmedUID = uid.String()
		}
		replies[e.Universe] = artnet.PollReply{IPAddress: ip.As4(), BindIndex: 1, ShortName: "Rehearsal", LongName: "Simulated patch universe", NumPorts: 1, PortTypes: [4]byte{0x80}, Status1: 2, NetSwitch: byte(e.Universe >> 8), SubSwitch: byte((e.Universe >> 4) & 15), SwOut: [4]byte{byte(e.Universe & 15)}}
		if fault == "missing" && i%5 == 0 {
			continue
		}
		address := e.StartAddress
		if fault == "address" && i%5 == 0 {
			address = address%512 + 1
		}
		addr := []byte{0, 0}
		binary.BigEndian.PutUint16(addr, address)
		count, _ := patch.PhaseCountFor(e, 0)
		if count == 1 {
			count = 0
		} // A root-only fixture has no RDM sub-devices.
		d := &demoDevice{uid: uid, label: e.Name, model: e.FixtureType, nodeIP: ip, port: pa, startAdr: address, proxied: fault == "slow" && i%5 == 0,
			deviceInfo:       params.DeviceInfo{ProtocolVersionMajor: 1, DMXFootprint: e.Footprint, DMXStartAddress: address, CurrentPersonality: 1, PersonalityCount: 1, SubDeviceCount: count},
			paramValues:      map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: addr, rdm.PIDDMXPersonality: {1, 1}, rdm.PIDIdentifyDevice: {0}},
			personalityDescs: map[byte]params.PersonalityDescription{1: {Index: 1, DMXFootprint: e.Footprint, Description: e.Mode}},
		}
		devices = append(devices, d)
		reg.NoteFixture(session.NodeRef{Key: session.NodeKey{IP: ip, BindIndex: 1}, Addr: netip.AddrPortFrom(ip, session.ArtNetUDPPort), Port: pa}, uid)
	}
	tap := func(dir capture.Direction, peer netip.AddrPort, b []byte) capture.Entry {
		e := ring.Add(capture.DecodeEntry(dir, peer, b))
		if capture.IsRDMLoggable(e) {
			rdmRing.Add(e)
		}
		return e
	}
	installDemoResponder(tport, rdmc, devices, tap, false)
	previous := tport.OnSend
	tport.OnSend = func(sp session.SentPacket) {
		previous(sp)
		if sp.DecodeErr != nil {
			return
		}
		var addresses []uint16
		switch sp.Packet.Kind {
		case artnet.KindPoll:
			for _, reply := range replies {
				reply := reply
				time.AfterFunc(time.Millisecond, func() {
					deliverPollReply(nodes, tap, reply, netip.AddrPortFrom(netip.AddrFrom4(reply.IPAddress), session.ArtNetUDPPort))
				})
			}
			return
		case artnet.KindTodRequest:
			for _, a := range sp.Packet.TodRequest.Address {
				addresses = append(addresses, uint16(sp.Packet.TodRequest.Net)<<8|uint16(a))
			}
		case artnet.KindTodControl:
			addresses = []uint16{uint16(sp.Packet.TodControl.Net)<<8 | uint16(sp.Packet.TodControl.Address)}
		default:
			return
		}
		for _, u := range addresses {
			reply, ok := replies[u]
			if !ok {
				continue
			}
			peer := netip.AddrPortFrom(netip.AddrFrom4(reply.IPAddress), session.ArtNetUDPPort)
			ids := []rdm.UID{}
			for _, d := range devices {
				if d.port.RawValue() == u {
					ids = append(ids, d.uid)
				}
			}
			for offset := 0; offset < len(ids) || offset == 0; offset += 200 {
				end := offset + 200
				if end > len(ids) {
					end = len(ids)
				}
				tod := artnet.TodData{ProtocolVersion: 14, RdmVersion: 1, Port: 1, Net: byte(u >> 8), Address: byte(u), UidTotal: uint16(len(ids)), BlockCount: byte(offset / 200), Tod: append([]rdm.UID{}, ids[offset:end]...)}
				time.AfterFunc(time.Millisecond, func() {
					tap(capture.DirIn, peer, artnet.Encode(artnet.Packet{Kind: artnet.KindTodData, TodData: tod}))
					rdmc.HandleTodData(tod, peer)
				})
			}
		}
	}
	srv.PatchStore.Replace(p)
	start := func() {
		go reg.RunContext(ctx)
		for _, reply := range replies {
			deliverPollReply(nodes, tap, reply, netip.AddrPortFrom(netip.AddrFrom4(reply.IPAddress), session.ArtNetUDPPort))
		}
		warmDemoDeviceCaches(ctx, rdmc, devices)
	}
	return srv, start, nil
}
