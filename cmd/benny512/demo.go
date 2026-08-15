package main

import (
	"context"
	"encoding/binary"
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

// uidToRespUID converts a UID to the fixed [6]byte form ArtPollReply's
// DefaultRespUID field wants.
func uidToRespUID(u rdm.UID) [6]byte {
	var out [6]byte
	copy(out[:], u.Bytes())
	return out
}

// demoDevice is one pre-scripted fake RDM responder for --demo mode. It
// generalizes the original six plain-fixture demo devices into a shape
// that can also describe manufacturer PIDs, sensors, and non-fixture
// device types, so the Phase 1c+ UI (generic PID editor, sensor gauges,
// device-class grouping, node config) is fully exercisable without any of
// Dom's actual hardware.
type demoDevice struct {
	uid      rdm.UID
	label    string
	model    string
	nodeIP   netip.Addr
	port     artnet.PortAddress
	startAdr uint16
	proxied  bool // answers via ACK_TIMER first, like a slow wireless proxy

	deviceInfo        params.DeviceInfo
	productDetails    []rdm.ProductDetail
	supportedExtra    []rdm.ParameterID // manufacturer/optional PIDs beyond the always-answered base set
	paramDescriptions map[rdm.ParameterID]rdm.ParameterDescription
	// noDescribe lists PIDs present in supportedExtra whose
	// PARAMETER_DESCRIPTION request should NACK — the "device that NACKs
	// PARAMETER_DESCRIPTION" fallback-path demo device (report §1.1 item 4).
	noDescribe  map[rdm.ParameterID]bool
	paramValues map[rdm.ParameterID][]byte // mutable GET/SET store
	sensors     []rdm.SensorDefinition
	sensorVals  []rdm.SensorValue // mutable, index-aligned with sensors
	// proxiedDeviceCount > 0 marks this device as acting as an RDM proxy
	// (report §1.3's PROXIED_DEVICES/PROXIED_DEVICE_COUNT signal).
	proxiedDeviceCount uint16
}

// baseSupportedParams are the PIDs every demo device advertises regardless
// of its specific profile (labels, personality, product detail) — mirrors
// what a real fixture's SUPPORTED_PARAMETERS would include beyond the
// mandatory-excluded set (report §2.1).
var baseSupportedParams = []rdm.ParameterID{
	rdm.PIDDeviceLabel, rdm.PIDManufacturerLabel, rdm.PIDDeviceModelDescription,
	rdm.PIDDMXPersonality, rdm.PIDProductDetailIDList,
}

func (d *demoDevice) supportedParameters() []rdm.ParameterID {
	out := append([]rdm.ParameterID(nil), baseSupportedParams...)
	if len(d.sensors) > 0 {
		out = append(out, rdm.PIDSensorDefinition, rdm.PIDSensorValue, rdm.PIDRecordSensors)
	}
	if d.proxiedDeviceCount > 0 {
		out = append(out, rdm.PIDProxiedDeviceCount, rdm.PIDProxiedDevices)
	}
	return append(out, d.supportedExtra...)
}

// handle computes this device's response to one decoded RDM request,
// mutating paramValues/sensorVals in place for SET commands.
func (d *demoDevice) handle(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason) {
	switch msg.ParameterID {
	case rdm.PIDDeviceInfo:
		return params.EncodeDeviceInfo(d.deviceInfo), false, 0
	case rdm.PIDDeviceLabel:
		return []byte(d.label), false, 0
	case rdm.PIDDeviceModelDescription:
		return []byte(d.model), false, 0
	case rdm.PIDManufacturerLabel:
		return []byte(registry.ManufacturerName(d.uid.ManufacturerID)), false, 0
	case rdm.PIDSoftwareVersionLabel:
		return []byte("1.0-demo"), false, 0
	case rdm.PIDProductDetailIDList:
		return rdm.EncodeProductDetailIDList(d.productDetails), false, 0
	case rdm.PIDSupportedParameters:
		return rdm.EncodeSupportedParameters(d.supportedParameters()), false, 0
	case rdm.PIDParameterDescription:
		if len(msg.ParameterData) < 2 {
			return nil, true, rdm.NackFormatError
		}
		pid := rdm.ParameterID(binary.BigEndian.Uint16(msg.ParameterData))
		if d.noDescribe[pid] {
			return nil, true, rdm.NackUnknownPID
		}
		pd, ok := d.paramDescriptions[pid]
		if !ok {
			return nil, true, rdm.NackUnknownPID
		}
		return rdm.EncodeParameterDescription(pd), false, 0
	case rdm.PIDSensorDefinition:
		if len(msg.ParameterData) < 1 || int(msg.ParameterData[0]) >= len(d.sensors) {
			return nil, true, rdm.NackDataOutOfRange
		}
		return rdm.EncodeSensorDefinition(d.sensors[msg.ParameterData[0]]), false, 0
	case rdm.PIDSensorValue:
		if len(msg.ParameterData) < 1 || int(msg.ParameterData[0]) >= len(d.sensorVals) {
			return nil, true, rdm.NackDataOutOfRange
		}
		idx := msg.ParameterData[0]
		if msg.CommandClass == rdm.SetCommand {
			// Per report §3.2, SET SENSOR_VALUE resets lowest/highest/
			// recorded to the current present_value.
			v := d.sensorVals[idx]
			v.Lowest, v.Highest, v.Recorded = v.Present, v.Present, v.Present
			d.sensorVals[idx] = v
			return nil, false, 0
		}
		return rdm.EncodeSensorValue(d.sensorVals[idx]), false, 0
	case rdm.PIDRecordSensors:
		if len(msg.ParameterData) < 1 {
			return nil, true, rdm.NackFormatError
		}
		idx := msg.ParameterData[0]
		if idx == rdm.AllSensors {
			for i, v := range d.sensorVals {
				v.Recorded = v.Present
				d.sensorVals[i] = v
			}
			return nil, false, 0
		}
		if int(idx) >= len(d.sensorVals) {
			return nil, true, rdm.NackDataOutOfRange
		}
		v := d.sensorVals[idx]
		v.Recorded = v.Present
		d.sensorVals[idx] = v
		return nil, false, 0
	case rdm.PIDProxiedDeviceCount:
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, d.proxiedDeviceCount)
		return b, false, 0
	default:
		v, ok := d.paramValues[msg.ParameterID]
		if !ok {
			return nil, true, rdm.NackUnknownPID
		}
		if msg.CommandClass == rdm.SetCommand {
			d.paramValues[msg.ParameterID] = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0
		}
		return v, false, 0
	}
}

// buildDemo wires every engine against session.FakeTransport/RealClock,
// pre-scripts two fake Art-Net nodes (a 4-port "Netron EN4"-ish gateway and
// a 1-port "LumenRadio Aurora"-ish wireless node) and ten fake RDM
// responders covering every device class/feature Dom's kit and the Phase
// 1c+ research report call out, and starts a periodic fake ArtDmx
// generator so the Analyzer screen has something to show. See each
// demoDevice literal below for which report finding it demonstrates.
func buildDemo(ctx context.Context) *web.Server {
	clock := session.RealClock{}
	tport := session.NewFakeTransport()

	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock, PollInterval: 3 * time.Second})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: tport, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(capture.DefaultCapacity)
	rdmRing := capture.New(capture.DefaultRDMCapacity)

	en4IP := netip.MustParseAddr("2.11.90.2")
	wirelessIP := netip.MustParseAddr("2.11.90.9")

	// EN4 root UID (0x1900 = ADJ Products LLC, report §7.3's leading
	// candidate for Obsidian's registered manufacturer ID — UNVERIFIED
	// which of 0x1900/0x22A6 a real EN4 reports; see registry/esta.go).
	en4RootUID := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x00000001}
	// Aurora root UID (0x4C55 = LumenRadio AB, report-CONFIRMED).
	auroraRootUID := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x00000001}

	nodes.HandlePollReply(artnet.PollReply{
		IPAddress: en4IP.As4(),
		ShortName: "EN4-Demo", LongName: "Netron EN4 (demo)",
		NumPorts:       4,
		PortTypes:      [4]byte{0x80, 0x80, 0x80, 0x80},
		Status1:        0x02, // RDM capable
		DefaultRespUID: uidToRespUID(en4RootUID),
	}, netip.AddrPortFrom(en4IP, session.ArtNetUDPPort))

	nodes.HandlePollReply(artnet.PollReply{
		IPAddress: wirelessIP.As4(),
		ShortName: "Aurora-Demo", LongName: "LumenRadio Aurora (demo)",
		NumPorts:       1,
		PortTypes:      [4]byte{0x80, 0, 0, 0},
		Status1:        0x02,
		DefaultRespUID: uidToRespUID(auroraRootUID),
	}, netip.AddrPortFrom(wirelessIP, session.ArtNetUDPPort))

	port0, _ := artnet.NewPortAddress(0, 0, 0)
	port1, _ := artnet.NewPortAddress(0, 0, 1)
	port2, _ := artnet.NewPortAddress(0, 0, 2)

	devices := buildDemoDevices(en4IP, wirelessIP, port0, port1, port2, en4RootUID, auroraRootUID)

	for _, d := range devices {
		ref := session.NodeRef{
			Key:  session.NodeKey{IP: d.nodeIP, BindIndex: 1},
			Addr: netip.AddrPortFrom(d.nodeIP, session.ArtNetUDPPort),
			Port: d.port,
		}
		reg.NoteFixture(ref, d.uid)
	}

	// Tap every outbound demo RDM request and its synthetic inbound reply
	// into the capture rings, exactly like buildReal's Demux does for real
	// traffic — so --demo mode genuinely exercises the Analyzer's RDM view
	// and the export endpoints, not just the synthetic ArtDmx feed below.
	srv := web.New(nodes, rdmc, dmx, reg, ring, rdmRing)
	srv.NIC = "demo (fake transport)"
	tap := func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry {
		e := capture.DecodeEntry(dir, peer, data)
		// Add stamps Time/Seq on its own returned copy — use that stamped
		// copy for the RDM ring and disk logger (see main.go's buildReal
		// for the same fix and fuller explanation of the bug this avoids).
		e = ring.Add(e)
		if capture.IsRDMKind(e.Kind) {
			e = rdmRing.Add(e)
			srv.LogRDMEntry(e)
		}
		return e
	}

	installDemoResponder(tport, rdmc, devices, tap)
	installDemoNodeConfigResponder(tport, nodes, en4IP, wirelessIP)

	go reg.Run()
	go generateDemoTraffic(ctx, ring)

	return srv
}

// buildDemoDevices returns the ten pre-scripted demoDevices. Each literal's
// comment names the report finding / task requirement it demonstrates.
func buildDemoDevices(en4IP, wirelessIP netip.Addr, port0, port1, port2 artnet.PortAddress, en4RootUID, auroraRootUID rdm.UID) []*demoDevice {
	basePV := func(model rdm.ProductCategory, footprint uint16, personalities, sensorCount byte) params.DeviceInfo {
		return params.DeviceInfo{
			ProtocolVersionMajor: 1, DeviceModelID: 1, ProductCategory: uint16(model),
			SoftwareVersionID: 0x01000000, DMXFootprint: footprint,
			CurrentPersonality: 1, PersonalityCount: personalities, SensorCount: sensorCount,
		}
	}
	dmxAddr := func(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

	// --- five plain fixtures (kept from the original demo set) ---
	wash1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}, label: "Wash 1", model: "Robe Wash",
		nodeIP: en4IP, port: port0, startAdr: 1,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}
	wash2 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}, label: "Wash 2", model: "Robe Wash",
		nodeIP: en4IP, port: port0, startAdr: 21,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(21), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}
	// Spot 1 demonstrates report §1.1 item 4: a device that lists a
	// manufacturer PID in SUPPORTED_PARAMETERS but NACKs
	// PARAMETER_DESCRIPTION for it — the UI's raw-hex-editor fallback path.
	spot1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0xAAAA, DeviceID: 3}, label: "Spot 1", model: "Ayrton Spot",
		nodeIP: en4IP, port: port1, startAdr: 1,
		deviceInfo:     basePV(rdm.CategoryFixtureMovingMirr, 24, 3, 0),
		supportedExtra: []rdm.ParameterID{0x8500},
		noDescribe:     map[rdm.ParameterID]bool{0x8500: true},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 3}, rdm.PIDIdentifyDevice: {0},
			0x8500: {0xDE, 0xAD}, // opaque 2-byte value, only ever editable as raw hex in the UI
		},
	}
	beam1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x4D50, DeviceID: 4}, label: "Beam 1", model: "Martin Beam",
		nodeIP: en4IP, port: port1, startAdr: 41,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 16, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(41), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}
	par1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x454C, DeviceID: 5}, label: "Par 1", model: "Elation Par",
		nodeIP: en4IP, port: port1, startAdr: 81,
		deviceInfo:  basePV(rdm.CategoryFixtureFixed, 8, 1, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(81), rdm.PIDDMXPersonality: {1, 1}, rdm.PIDIdentifyDevice: {0}},
	}
	wirelessMover := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x6C74, DeviceID: 6}, label: "Wireless Mover", model: "via LumenRadio proxy",
		nodeIP: wirelessIP, port: port0, startAdr: 1, proxied: true,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}

	// --- Chroma-Q Color Force II-ish fixture: manufacturer PIDs + sensors,
	// one sensor reading outside its normal band (report §7.1, §1.2). ---
	chromaQ := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}, label: "CF2 48", model: "Chroma-Q Color Force II 48",
		nodeIP: en4IP, port: port2, startAdr: 1,
		deviceInfo:     basePV(rdm.CategoryFixtureFixed, 20, 1, 2),
		supportedExtra: []rdm.ParameterID{0x8010, 0x8011, 0x8012},
		paramDescriptions: map[rdm.ParameterID]rdm.ParameterDescription{
			0x8010: {PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16, Description: "PIXEL COUNT"},
			0x8011: {PID: 0x8011, PDLSize: 2, DataType: rdm.DSUnsignedWord, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitHertz, Prefix: rdm.PrefixNone, MinValue: 750, MaxValue: 24000, DefaultValue: 1500, Description: "REFRESH RATE"},
			0x8012: {PID: 0x8012, PDLSize: 1, DataType: rdm.DSBoolean, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 1, DefaultValue: 0, Description: "LED FLIP"},
		},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 1}, rdm.PIDIdentifyDevice: {0},
			0x8010: {16}, 0x8011: {0x05, 0xDC} /* 1500 */, 0x8012: {0},
		},
		sensors: []rdm.SensorDefinition{
			{SensorNumber: 0, Type: rdm.SensorTemperature, Unit: rdm.UnitCentigrade, Prefix: rdm.PrefixNone, RangeMin: -20, RangeMax: 100, NormalMin: 0, NormalMax: 60, SupportsRecording: 0x03, Description: "PSU TEMP"},
			{SensorNumber: 1, Type: rdm.SensorVoltage, Unit: rdm.UnitVoltsDC, Prefix: rdm.PrefixDeci, RangeMin: 0, RangeMax: 300, NormalMin: 100, NormalMax: 250, SupportsRecording: 0x03, Description: "PSU VOLTAGE"},
		},
		sensorVals: []rdm.SensorValue{
			{SensorNumber: 0, Present: 85, Lowest: 20, Highest: 90, Recorded: 85},     // OUTSIDE [0,60] — warning-state demo
			{SensorNumber: 1, Present: 235, Lowest: 200, Highest: 240, Recorded: 235}, // inside [100,250]
		},
	}

	// --- Splitter: DATA_DISTRIBUTION category + SPLITTER product detail,
	// footprint 0, no sensors (report §1.3/§4.1-4.2). ---
	splitter := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x1900, DeviceID: 2}, label: "Splitter 1x4", model: "Generic 5-pin Splitter",
		nodeIP: en4IP, port: port2, startAdr: 0,
		deviceInfo:     basePV(rdm.CategoryDataDistribution, 0, 1, 0),
		productDetails: []rdm.ProductDetail{rdm.DetailSplitter},
		paramValues:    map[rdm.ParameterID][]byte{rdm.PIDIdentifyDevice: {0}},
	}

	// --- EN4 root: the gateway's own RDM identity (report feature C's
	// "node view should show this node's own RDM parameters"), footprint
	// 0, DATA_DISTRIBUTION + ETHERNET_NODE, plus a couple of E1.37-2 IP
	// PIDs so /api/device/{uid}/param/0705 etc. have something to show. ---
	en4Root := &demoDevice{
		uid: en4RootUID, label: "EN4-Demo", model: "Obsidian Netron EN4 (demo)",
		nodeIP: en4IP, port: port0, startAdr: 0,
		deviceInfo:     basePV(rdm.CategoryDataDistribution, 0, 1, 0),
		productDetails: []rdm.ProductDetail{rdm.DetailEthernetNode},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDIdentifyDevice:     {0},
			rdm.PIDListInterfaces:     {0, 0, 0, 1},
			rdm.PIDIPv4CurrentAddress: append([]byte{0, 0, 0, 1}, append(en4IP.AsSlice(), 255, 255, 0, 0)...),
		},
	}

	// --- Aurora root: LumenRadio-style RDM proxy with a signal-quality
	// sensor OUTSIDE its normal band (poor signal — a second warning-state
	// demo distinct from the Chroma-Q's) and a non-empty
	// PROXIED_DEVICE_COUNT marking it as a proxy (report §1.3, §7.2). ---
	auroraRoot := &demoDevice{
		uid: auroraRootUID, label: "Aurora-Demo", model: "LumenRadio Aurora (demo)",
		nodeIP: wirelessIP, port: port0, startAdr: 0,
		deviceInfo:         basePV(rdm.CategoryNotDeclared, 0, 2, 1),
		productDetails:     []rdm.ProductDetail{rdm.DetailWirelessLink},
		proxiedDeviceCount: 1,
		paramValues:        map[rdm.ParameterID][]byte{rdm.PIDIdentifyDevice: {0}, rdm.PIDDMXPersonality: {1, 2}},
		sensors: []rdm.SensorDefinition{
			// type=SENS_OTHER, unit=Percent: report §7.2 flags LumenRadio's
			// actual type/unit choice for "signal quality" as UNVERIFIED —
			// this is a plausible placeholder, not a confirmed value.
			{SensorNumber: 0, Type: rdm.SensorOther, Unit: rdm.UnitPercent, Prefix: rdm.PrefixNone, RangeMin: 0, RangeMax: 100, NormalMin: 40, NormalMax: 100, SupportsRecording: 0x01, Description: "Signal Quality"},
		},
		sensorVals: []rdm.SensorValue{
			{SensorNumber: 0, Present: 15, Lowest: 10, Highest: 90, Recorded: 15}, // OUTSIDE [40,100] — poor signal warning
		},
	}

	return []*demoDevice{wash1, wash2, spot1, beam1, par1, wirelessMover, chromaQ, splitter, en4Root, auroraRoot}
}

// installDemoResponder wires FakeTransport.OnSend to answer any RDM
// request to one of devices with that device's scripted response, so every
// Fixtures/Devices/Sensors/Params screen works end-to-end in --demo mode
// with no hardware. The scripted "proxied" device answers its first
// request with ACK_TIMER before ACKing, modelling a wireless RDM proxy's
// normal path (architecture rev 5 §1.1).
func installDemoResponder(tport *session.FakeTransport, ctrl *session.RDMController, devices []*demoDevice, tap func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry) {
	byUID := make(map[rdm.UID]*demoDevice, len(devices))
	for _, d := range devices {
		byUID[d.uid] = d
	}
	seenOnce := make(map[rdm.UID]bool)

	// replyWire encodes one RDM response as a full ArtRdm datagram addressed
	// back from the device's own node, for the capture tap — matching what
	// would actually be on the wire, so --demo mode's Analyzer/export show
	// the same shape of data a real bench session would.
	replyWire := func(resp rdm.Message, node netip.AddrPort, net, address byte) {
		pkt := artnet.EncodeRdmPacket(resp, artnet.DefaultProtocolVersion, net, address)
		wire := artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: pkt})
		tap(capture.DirIn, node, wire)
	}

	tport.OnSend = func(sp session.SentPacket) {
		tap(capture.DirOut, sp.Dst, sp.Data)
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		d, ok := byUID[msg.DestinationUID]
		if !ok {
			return
		}
		net, address := sp.Packet.Rdm.Net, sp.Packet.Rdm.Address

		if d.proxied && !seenOnce[d.uid] {
			seenOnce[d.uid] = true
			resp := rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: d.uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACKTimer),
				SubDevice: msg.SubDevice, CommandClass: responseClassFor(msg.CommandClass),
				ParameterID: msg.ParameterID, ParameterData: []byte{0x00, 0x14},
			}
			time.AfterFunc(5*time.Millisecond, func() {
				replyWire(resp, sp.Dst, net, address)
				ctrl.HandleRDMResponse(resp)
			})
			return
		}

		data, nack, reason := d.handle(msg)
		respClass := responseClassFor(msg.CommandClass)
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: d.uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseNackReason),
				SubDevice: msg.SubDevice, CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: []byte{byte(reason >> 8), byte(reason)},
			}
		} else {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: d.uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACK),
				SubDevice: msg.SubDevice, CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: data,
			}
		}
		delay := 3 * time.Millisecond
		if d.proxied {
			delay = 30 * time.Millisecond
		}
		time.AfterFunc(delay, func() {
			replyWire(resp, sp.Dst, net, address)
			ctrl.HandleRDMResponse(resp)
		})
	}
}

// installDemoNodeConfigResponder makes the two demo nodes "answer" ArtAddress/
// ArtInput/ArtIpProg the way a real node would (a follow-up ArtPollReply, or
// ArtIpProgReply for IP changes), so /api/node/{ip}/address, /input and
// /ipconfig are exercisable against --demo mode too (task ask: "the UI
// agent... can exercise everything with no hardware"). It chains onto
// whatever OnSend hook is already installed (installDemoResponder's RDM
// handling) rather than replacing it.
func installDemoNodeConfigResponder(tport *session.FakeTransport, nodes *session.ArtNetSession, en4IP, wirelessIP netip.Addr) {
	prev := tport.OnSend
	tport.OnSend = func(sp session.SentPacket) {
		if prev != nil {
			prev(sp)
		}
		if sp.DecodeErr != nil {
			return
		}
		switch sp.Packet.Kind {
		case artnet.KindAddress:
			ip, ok := demoTargetIP(sp, en4IP, wirelessIP)
			if !ok {
				return
			}
			a := sp.Packet.Address
			time.AfterFunc(5*time.Millisecond, func() {
				nodes.HandlePollReply(artnet.PollReply{
					IPAddress: ip.As4(), ShortName: a.ShortName, LongName: a.LongName,
					NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02,
				}, netip.AddrPortFrom(ip, session.ArtNetUDPPort))
			})
		case artnet.KindInput:
			ip, ok := demoTargetIP(sp, en4IP, wirelessIP)
			if !ok {
				return
			}
			time.AfterFunc(5*time.Millisecond, func() {
				nodes.HandlePollReply(artnet.PollReply{IPAddress: ip.As4(), NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02}, netip.AddrPortFrom(ip, session.ArtNetUDPPort))
			})
		case artnet.KindIpProg:
			ip, ok := demoTargetIP(sp, en4IP, wirelessIP)
			if !ok {
				return
			}
			p := sp.Packet.IpProg
			reply := artnet.IpProgReply{CurrentIP: p.ProgIP, CurrentSubnet: p.ProgSubnetMask, CurrentPort: session.ArtNetUDPPort}
			if p.Command&artnet.IpProgEnableDHCP != 0 {
				reply.Status = artnet.IpProgReplyDHCPEnabled
			}
			pkt := artnet.Encode(artnet.Packet{Kind: artnet.KindIpProgReply, IpProgReply: reply})
			time.AfterFunc(5*time.Millisecond, func() {
				nodes.HandleInbound(session.Inbound{Data: pkt, From: netip.AddrPortFrom(ip, session.ArtNetUDPPort)})
			})
		}
	}
}

func demoTargetIP(sp session.SentPacket, en4IP, wirelessIP netip.Addr) (netip.Addr, bool) {
	switch sp.Dst.Addr() {
	case en4IP:
		return en4IP, true
	case wirelessIP:
		return wirelessIP, true
	default:
		return netip.Addr{}, false
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
