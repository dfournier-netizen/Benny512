package main

import (
	"context"
	"net/netip"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
	"benny512/internal/web"
)

// demoFixture is one pre-scripted fake RDM responder.
type demoFixture struct {
	uid      rdm.UID
	label    string
	model    string
	proxied  bool // answers via ACK_TIMER first, like a slow wireless proxy
	nodeIP   netip.Addr
	port     artnet.PortAddress
	startAdr uint16
}

// buildDemo wires every engine against session.FakeTransport/FakeClock (real
// wall-clock backed — see fakeRealtimeClock below), pre-scripts two fake
// Art-Net nodes (a 4-port "Netron EN4"-ish node and a 1-port wireless node)
// and six fake RDM fixtures (one of which answers via ACK_TIMER, modelling a
// LumenRadio-style proxy), and starts a periodic fake ArtDmx generator so
// the Analyzer screen has something to show. It lets Dom exercise the whole
// UI with zero hardware, per architecture rev 5 §7 (Phase 1c).
func buildDemo(ctx context.Context) *web.Server {
	clock := session.RealClock{}
	tport := session.NewFakeTransport()

	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock, PollInterval: 3 * time.Second})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: tport, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(capture.DefaultCapacity)

	en4IP := netip.MustParseAddr("2.11.90.2")
	wirelessIP := netip.MustParseAddr("2.11.90.9")

	nodes.HandlePollReply(artnet.PollReply{
		IPAddress: en4IP.As4(),
		ShortName: "EN4-Demo", LongName: "Netron EN4 (demo)",
		NumPorts:  4,
		PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80},
		Status1:   0x02, // RDM capable
	}, netip.AddrPortFrom(en4IP, session.ArtNetUDPPort))

	nodes.HandlePollReply(artnet.PollReply{
		IPAddress: wirelessIP.As4(),
		ShortName: "Aurora-Demo", LongName: "LumenRadio Aurora (demo)",
		NumPorts:  1,
		PortTypes: [4]byte{0x80, 0, 0, 0},
		Status1:   0x02,
	}, netip.AddrPortFrom(wirelessIP, session.ArtNetUDPPort))

	port0, _ := artnet.NewPortAddress(0, 0, 0)
	port1, _ := artnet.NewPortAddress(0, 0, 1)

	fixtures := []demoFixture{
		{uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}, label: "Wash 1", model: "Robe Wash", nodeIP: en4IP, port: port0, startAdr: 1},
		{uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}, label: "Wash 2", model: "Robe Wash", nodeIP: en4IP, port: port0, startAdr: 21},
		{uid: rdm.UID{ManufacturerID: 0xAAAA, DeviceID: 3}, label: "Spot 1", model: "Ayrton Spot", nodeIP: en4IP, port: port1, startAdr: 1},
		{uid: rdm.UID{ManufacturerID: 0x4D50, DeviceID: 4}, label: "Beam 1", model: "Martin Beam", nodeIP: en4IP, port: port1, startAdr: 41},
		{uid: rdm.UID{ManufacturerID: 0x454C, DeviceID: 5}, label: "Par 1", model: "Elation Par", nodeIP: en4IP, port: port1, startAdr: 81},
		{uid: rdm.UID{ManufacturerID: 0x6C74, DeviceID: 6}, label: "Wireless Mover", model: "via LumenRadio proxy", nodeIP: wirelessIP, port: port0, startAdr: 1, proxied: true},
	}

	for _, f := range fixtures {
		ref := session.NodeRef{
			Key:  session.NodeKey{IP: f.nodeIP, BindIndex: 1},
			Addr: netip.AddrPortFrom(f.nodeIP, session.ArtNetUDPPort),
			Port: f.port,
		}
		reg.NoteFixture(ref, f.uid)
	}

	installDemoResponder(tport, rdmc, fixtures)

	go reg.Run()
	go generateDemoTraffic(ctx, ring)

	return web.New(nodes, rdmc, dmx, reg, ring)
}

// installDemoResponder wires FakeTransport.OnSend to answer GET requests
// for the six demo fixtures with plausible canned parameter data, so the
// Fixtures screen's detail pane and Identify toggle work end-to-end in
// --demo mode without real hardware. The scripted proxied fixture answers
// its first request with ACK_TIMER before ACKing, modelling a wireless
// RDM proxy's normal path (architecture rev 5 §1.1).
func installDemoResponder(tport *session.FakeTransport, ctrl *session.RDMController, fixtures []demoFixture) {
	byUID := make(map[rdm.UID]demoFixture, len(fixtures))
	for _, f := range fixtures {
		byUID[f.uid] = f
	}
	seenOnce := make(map[rdm.UID]bool)

	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		f, ok := byUID[msg.DestinationUID]
		if !ok {
			return
		}

		if f.proxied && !seenOnce[f.uid] {
			seenOnce[f.uid] = true
			// ACK_TIMER: ask for ~200ms (20 units * 10ms), matching the
			// controller's default AckTimerUnit.
			resp := rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: f.uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACKTimer),
				SubDevice: msg.SubDevice, CommandClass: responseClassFor(msg.CommandClass),
				ParameterID: msg.ParameterID, ParameterData: []byte{0x00, 0x14},
			}
			time.AfterFunc(5*time.Millisecond, func() { ctrl.HandleRDMResponse(resp) })
			return
		}

		data := demoParamData(f, msg.ParameterID)
		respClass := responseClassFor(msg.CommandClass)
		if msg.CommandClass == rdm.SetCommand {
			data = nil
		}
		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: f.uid,
			TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACK),
			SubDevice: msg.SubDevice, CommandClass: respClass,
			ParameterID: msg.ParameterID, ParameterData: data,
		}
		delay := 3 * time.Millisecond
		if f.proxied {
			delay = 30 * time.Millisecond
		}
		time.AfterFunc(delay, func() { ctrl.HandleRDMResponse(resp) })
	}
}

func responseClassFor(cc rdm.CommandClass) rdm.CommandClass {
	switch cc {
	case rdm.GetCommand:
		return rdm.GetCommandResponse
	case rdm.SetCommand:
		return rdm.SetCommandResponse
	default:
		return cc
	}
}

func demoParamData(f demoFixture, pid rdm.ParameterID) []byte {
	switch pid {
	case rdm.PIDDeviceInfo:
		return params.EncodeDeviceInfo(params.DeviceInfo{
			ProtocolVersionMajor: 1, DeviceModelID: 1, ProductCategory: 0x0101,
			SoftwareVersionID: 0x01000000, DMXFootprint: 20,
			CurrentPersonality: 1, PersonalityCount: 2, DMXStartAddress: f.startAdr,
		})
	case rdm.PIDDeviceLabel:
		return []byte(f.label)
	case rdm.PIDDeviceModelDescription:
		return []byte(f.model)
	case rdm.PIDManufacturerLabel:
		return []byte(registry.ManufacturerName(f.uid.ManufacturerID))
	case rdm.PIDSoftwareVersionLabel:
		return []byte("1.0-demo")
	case rdm.PIDDMXStartAddress:
		return []byte{byte(f.startAdr >> 8), byte(f.startAdr)}
	case rdm.PIDDMXPersonality:
		return []byte{1, 2}
	case rdm.PIDIdentifyDevice:
		return []byte{0}
	default:
		return nil
	}
}

// generateDemoTraffic periodically drops synthetic ArtDmx-shaped capture
// entries so the Analyzer screen has live movement in --demo mode. It
// writes directly to the capture ring (skipping actual wire encoding,
// since nothing is listening on a real socket in demo mode).
func generateDemoTraffic(ctx context.Context, ring *capture.Ring) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	seq := byte(0)
	universes := []uint16{0, 1}
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			u := universes[i%len(universes)]
			i++
			ring.Add(capture.Entry{
				Dir: capture.DirOut, Kind: "ArtDmx", Universe: u, Size: 530,
				Key: "seq=" + itoa(int(seq)) + " len=512",
			})
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
