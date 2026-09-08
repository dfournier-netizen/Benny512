package main

import (
	"context"
	"encoding/binary"
	"net/netip"
	"sort"
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

	// mfrLabel is this device's own MANUFACTURER_LABEL (0x0081) report —
	// deliberately independent of the ESTA-table lookup keyed off uid's
	// manufacturer ID (registry.ManufacturerName), since real gear can (and
	// does) report a more specific/different name than its registered ESTA
	// entry — e.g. Obsidian Control Systems trading under an ADJ-family ID
	// (see en4Root below). Empty means "device doesn't override" and GET
	// MANUFACTURER_LABEL falls back to the ESTA lookup, matching the
	// pre-existing demo behavior for devices this pass didn't touch.
	mfrLabel string
	// noManufacturerLabel/noModelDescription make GET MANUFACTURER_LABEL /
	// GET DEVICE_MODEL_DESCRIPTION NACK outright — the Devices screen's
	// fallback-path demo devices (task ask: "keep at least one device that
	// NACKs DEVICE_MODEL_DESCRIPTION so the fallback path is visible").
	noManufacturerLabel bool
	noModelDescription  bool

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
	// curveLabels maps a CURVE index (1-based) to its CURVE_DESCRIPTION
	// label — task ask: "extend --demo so a fake fixture exposes a curve
	// PID with descriptions (so the labeled dropdown is exercisable)".
	// paramValues[rdm.PIDCurve] carries the current+count bytes; this map
	// is what handle() consults for CURVE_DESCRIPTION's per-index GET.
	curveLabels map[byte]string
	// personalityDescs maps a DMX_PERSONALITY index (1-based) to its
	// DMX_PERSONALITY_DESCRIPTION (report brief: "surface personality name +
	// slot count" fix — exercises the newly-wired PID end to end in
	// --demo). paramValues[rdm.PIDDMXPersonality] carries the current+count
	// bytes; this map is what handle() consults for the per-index GET, same
	// pattern as curveLabels above.
	personalityDescs map[byte]params.PersonalityDescription
	// proxiedDeviceCount > 0 marks this device as acting as an RDM proxy
	// (report §1.3's PROXIED_DEVICES/PROXIED_DEVICE_COUNT signal).
	proxiedDeviceCount uint16

	// --- E1.20 §10.11 "Device Control" (owner ask: "add every PID in
	// §10.11 to the device controls section") ---
	//
	// selfTestActive/selfTestDescs back PERFORM_SELFTEST/SELF_TEST_
	// DESCRIPTION/SELFTEST_ENHANCED. selfTestDescs's keys are exactly the
	// self test numbers SELFTEST_ENHANCED's packed list reports (both PIDs
	// must agree, same as real hardware would); values are the text label
	// SELF_TEST_DESCRIPTION returns for that number.
	selfTestActive bool
	selfTestDescs  map[byte]string
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
		// SET actually renames the device, the way a real fixture does.
		// This case used to ACK the write and then keep answering with the
		// OLD label, which made the demo rig lie in the one way that
		// matters most to the Reconcile screen: that screen re-READS every
		// setting after a push, specifically so the owner can watch the
		// write land, and a responder that silently discards a SET turns a
		// working Apply-to-confirm loop into an apparent product bug. The
		// default branch below has always stored SETs this way for every
		// paramValues-backed PID; DEVICE_LABEL was the outlier only because
		// its value lives in its own field rather than in that map.
		if msg.CommandClass == rdm.SetCommand {
			d.label = string(msg.ParameterData)
			return nil, false, 0
		}
		return []byte(d.label), false, 0
	case rdm.PIDDeviceModelDescription:
		if d.noModelDescription {
			return nil, true, rdm.NackUnknownPID
		}
		return []byte(d.model), false, 0
	case rdm.PIDManufacturerLabel:
		if d.noManufacturerLabel {
			return nil, true, rdm.NackUnknownPID
		}
		if d.mfrLabel != "" {
			return []byte(d.mfrLabel), false, 0
		}
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
		// 3-byte shape confirmed against the ANSI E1.20-2025 PDF (§8.4.1):
		// UINT16 count + 1-byte List Change flag — see internal/rdm/proxy.go.
		return rdm.EncodeProxiedDeviceCount(rdm.ProxiedDeviceCount{Count: d.proxiedDeviceCount}), false, 0
	case rdm.PIDResetDevice:
		// Phase D task 2/4: mirror real hardware's E1.20 §10.11.2 behavior
		// exactly, rather than falling into the generic paramValues
		// echo-back the default branch would otherwise give it (which would
		// wrongly let a GET succeed): no GET form exists at all, and a SET
		// is only legal with PD == Warm(0x01) or Cold(0xFF).
		if msg.CommandClass == rdm.GetCommand {
			return nil, true, rdm.NackUnsupportedCommandClass
		}
		if len(msg.ParameterData) != 1 || (msg.ParameterData[0] != byte(rdm.ResetWarm) && msg.ParameterData[0] != byte(rdm.ResetCold)) {
			return nil, true, rdm.NackFormatError
		}
		return nil, false, 0
	case rdm.PIDFactoryDefaults:
		// GET returns the 1-byte boolean status; SET (PDL=0, no PD) is the
		// "revert now" trigger and always succeeds in demo mode.
		if msg.CommandClass == rdm.SetCommand {
			d.paramValues[rdm.PIDFactoryDefaults] = []byte{0} // demo: reverting always lands on "not currently default" for realism
			return nil, false, 0
		}
		return d.paramValues[rdm.PIDFactoryDefaults], false, 0
	case rdm.PIDCurveDescription:
		// Task ask: "a fake fixture exposes a curve PID with descriptions
		// (so the labeled dropdown is exercisable)". CURVE itself (current +
		// count) is a plain paramValues entry (default branch below);
		// CURVE_DESCRIPTION needs per-index special-casing like
		// PARAMETER_DESCRIPTION does, since the response varies by the
		// requested index.
		if len(msg.ParameterData) < 1 {
			return nil, true, rdm.NackFormatError
		}
		idx := msg.ParameterData[0]
		label, ok := d.curveLabels[idx]
		if !ok {
			return nil, true, rdm.NackDataOutOfRange
		}
		return append([]byte{idx}, []byte(label)...), false, 0
	case rdm.PIDPowerState:
		// E1.20 §10.11.3: GET/SET share the 1-byte Table A-11 shape. A SET
		// of anything outside the four defined states is a malformed
		// request, same discipline RESET_DEVICE's SET already enforces.
		if msg.CommandClass == rdm.SetCommand {
			if len(msg.ParameterData) != 1 {
				return nil, true, rdm.NackFormatError
			}
			v := rdm.PowerState(msg.ParameterData[0])
			if !v.IsValid() {
				return nil, true, rdm.NackDataOutOfRange
			}
			d.paramValues[rdm.PIDPowerState] = []byte{byte(v)}
			return nil, false, 0
		}
		return d.paramValues[rdm.PIDPowerState], false, 0
	case rdm.PIDPerformSelfTest:
		// E1.20 §10.11.4: GET returns "Self Tests Active" bool; SET starts
		// (any non-zero Self Test #) or stops (SelfTestOff) whichever test
		// this demo device is scripted with.
		if msg.CommandClass == rdm.SetCommand {
			if len(msg.ParameterData) != 1 {
				return nil, true, rdm.NackFormatError
			}
			d.selfTestActive = rdm.SelfTestNumber(msg.ParameterData[0]) != rdm.SelfTestOff
			return nil, false, 0
		}
		active := byte(0)
		if d.selfTestActive {
			active = 1
		}
		return []byte{active}, false, 0
	case rdm.PIDSelfTestDescription:
		// E1.20 §10.11.5: per-index special-casing, same pattern as
		// CURVE_DESCRIPTION/DMX_PERSONALITY_DESCRIPTION above.
		if len(msg.ParameterData) != 1 {
			return nil, true, rdm.NackFormatError
		}
		n := msg.ParameterData[0]
		label, ok := d.selfTestDescs[n]
		if !ok {
			return nil, true, rdm.NackDataOutOfRange
		}
		return append([]byte{n}, []byte(label)...), false, 0
	case rdm.PIDSelfTestEnhanced:
		// E1.20 §10.11.8: packed list, one 6-byte entry per key of
		// selfTestDescs (sorted so the response is deterministic across
		// runs) — no manufacturer-specific Result Code PID declared (header
		// 0x0000, per §10.11.8's "Responder not supporting this feature
		// shall set the field to 0x0000").
		nums := make([]byte, 0, len(d.selfTestDescs))
		for n := range d.selfTestDescs {
			nums = append(nums, n)
		}
		sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
		out := make([]byte, 2, 2+6*len(nums))
		for i, n := range nums {
			status := rdm.SelfTestStatusNotRun
			if d.selfTestActive {
				status = rdm.SelfTestStatusActive
			} else if i == 0 {
				status = rdm.SelfTestStatusPass // demo flavor: the first test has "already been run"
			}
			entry := make([]byte, 6)
			entry[0] = n
			entry[1] = byte(status)
			binary.BigEndian.PutUint16(entry[2:4], rdm.SelfTestCapAutoTerminate)
			binary.BigEndian.PutUint16(entry[4:6], 0)
			out = append(out, entry...)
		}
		return out, false, 0
	case rdm.PIDCapturePreset:
		// E1.20 §10.11.6: SET_COMMAND only (no GET form at all) — mirror
		// RESET_DEVICE's unconditional GET rejection. Accepts either the
		// 2-byte (scene-only) or 8-byte (scene+timing) PDL shapes; the demo
		// doesn't actually persist captured scenes (nothing in this app
		// reads them back), so a well-formed request always succeeds.
		if msg.CommandClass == rdm.GetCommand {
			return nil, true, rdm.NackUnsupportedCommandClass
		}
		if len(msg.ParameterData) != 2 && len(msg.ParameterData) != 8 {
			return nil, true, rdm.NackFormatError
		}
		return nil, false, 0
	case rdm.PIDPresetPlayback:
		// E1.20 §10.11.7: 3-byte Mode+Level shape, GET/SET/echo.
		if msg.CommandClass == rdm.SetCommand {
			if len(msg.ParameterData) != 3 {
				return nil, true, rdm.NackFormatError
			}
			d.paramValues[rdm.PIDPresetPlayback] = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0
		}
		return d.paramValues[rdm.PIDPresetPlayback], false, 0
	case rdm.PIDInterfaceApplyConfiguration:
		// SET-only (PDL=4, bare interface ID) per ipconfig.go's convention —
		// demo always accepts a well-formed apply.
		if msg.CommandClass == rdm.GetCommand {
			return nil, true, rdm.NackUnsupportedCommandClass
		}
		if len(msg.ParameterData) != 4 {
			return nil, true, rdm.NackFormatError
		}
		return nil, false, 0
	case rdm.PIDDNSNameServer:
		// Demo: only index 0 is populated (a single upstream resolver),
		// matching ipconfig_test.go's own index+4-byte-IP shape — any other
		// index NACKs NackDataOutOfRange, the honest "nothing configured at
		// this slot" answer rather than echoing back index 0's address under
		// a different index.
		if len(msg.ParameterData) < 1 {
			return nil, true, rdm.NackFormatError
		}
		if msg.ParameterData[0] != 0 {
			return nil, true, rdm.NackDataOutOfRange
		}
		return []byte{0, 8, 8, 8, 8}, false, 0
	case rdm.PIDDMXPersonalityDescription:
		// Same per-index special-casing as CURVE_DESCRIPTION above, plus the
		// DMX-footprint field this PID (uniquely among the *_DESCRIPTION
		// family) carries — see params.PersonalityDescription's doc comment.
		if len(msg.ParameterData) < 1 {
			return nil, true, rdm.NackFormatError
		}
		idx := msg.ParameterData[0]
		pd, ok := d.personalityDescs[idx]
		if !ok {
			return nil, true, rdm.NackDataOutOfRange
		}
		pd.Index = idx
		return params.EncodePersonalityDescription(pd), false, 0
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
// 1c+ research report call out, and returns a start func that generates all
// of that traffic. See each demoDevice literal below for which report
// finding it demonstrates.
//
// Construction and traffic generation are deliberately split: everything
// up to and including installDemoResponder/installDemoNodeConfigResponder
// below only builds engines and wires the capture tap — nothing is sent or
// delivered yet, so nothing can reach srv.LogRDMEntry yet either. The
// returned start func is where the two demo nodes' seed ArtPollReply and
// the device-cache RDM warmup actually fire. This split exists because that
// seed traffic — both nodes' very first ArtPollReply, plus every warmup
// GET — is exactly the evidence --lognodes exists to capture, and
// cmd/benny512's caller wires --logrdm's disk logger via
// srv.SetLogRDMPath *after* getting srv back from this function. Returning
// srv fully built but silent, with start left for the caller to call once
// logging (and everything else in its startup sequence) is armed, is what
// makes "the log is non-empty from the first instant there's anything to
// log" true instead of "true after the first lucky re-poll" — see main()'s
// call site.
func buildDemo(ctx context.Context, legacyRdmStartCode bool, logNodes bool) (*web.Server, func()) {
	clock := session.RealClock{}
	tport := session.NewFakeTransport()

	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock, PollInterval: 3 * time.Second})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock, LegacyRdmStartCode: legacyRdmStartCode})
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

	// The two seed ArtPollReply values below are delivered further down
	// (after tap is wired), via deliverPollReply, so they flow through the
	// same capture tap real node traffic would — see that function's doc
	// comment for why a direct nodes.HandlePollReply call here would leave
	// --demo unable to exercise --lognodes' ArtPollReply rendering at all.
	en4Reply := artnet.PollReply{
		IPAddress: en4IP.As4(),
		ShortName: "EN4-Demo", LongName: "Netron EN4 (demo)",
		NumPorts:  4,
		PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80},
		// SwOut gives each of the 4 ports its own Port-Address (universe
		// 0/1/2/3, matching port0/port1/port2 below) — Phase B addition.
		// Without this the struct's zero value left every port decoding to
		// Port-Address 0, so the Devices screen's device/port picker (which
		// groups this node's 4 ports into one card) had no genuinely
		// distinct ports to pick between: a port-scoped Discover/Clear on
		// "port 1" would have silently acted on the same Port-Address as
		// "port 0". See devices.js's device/port picker survey notes.
		SwOut:          [4]byte{0x00, 0x01, 0x02, 0x03},
		Status1:        0x02, // RDM capable
		DefaultRespUID: uidToRespUID(en4RootUID),
	}
	auroraReply := artnet.PollReply{
		IPAddress: wirelessIP.As4(),
		ShortName: "Aurora-Demo", LongName: "LumenRadio Aurora (demo)",
		NumPorts:       1,
		PortTypes:      [4]byte{0x80, 0, 0, 0},
		Status1:        0x02,
		DefaultRespUID: uidToRespUID(auroraRootUID),
	}

	// realEN4IP/realEN4PortReplies reproduce the bind-per-port ArtPollReply
	// pattern RDM-LOG7 actually captured from a real Obsidian EN4 (bench
	// report, this round): the node sends one ArtPollReply PER PHYSICAL
	// PORT, each its own bind index, each declaring NumPorts=1 with only
	// port[0] populated — NOT one reply with NumPorts=4, the shape en4Reply
	// above models (also a real, valid Art-Net pattern, just a different
	// one — some nodes really do report all their ports in a single reply).
	// Investigating that report's "every port shows n/a" symptom found
	// nothing to fix in this app's decode/session/UI path — see the round's
	// notes entry for the full trace — but there was no demo coverage of
	// this exact wire shape to render-prove that finding against, only of
	// the single-reply-NumPorts=4 shape. This fills that gap: three bind
	// indices at the log's own IP, byte-for-byte matching NumPorts/PortTypes/
	// GoodInput/GoodOutputA/GoodOutputB/SwOut per port from the captured hex
	// (RDM-LOG7 lines 9-34), so the Nodes screen can be screenshotted
	// against the literal bench evidence: three single-port rows, no
	// phantom "n/a" ports.
	realEN4IP := netip.MustParseAddr("2.11.90.4")
	realEN4PortReplies := []artnet.PollReply{
		{
			IPAddress: realEN4IP.As4(), BindIndex: 1,
			ShortName: "Port 1", LongName: "NETRON EN4",
			NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0},
			GoodInput: [4]byte{0x08, 0, 0, 0}, GoodOutputB: [4]byte{0x40, 0, 0, 0},
			SwIn: [4]byte{0x00, 0, 0, 0}, SwOut: [4]byte{0x00, 0, 0, 0},
			Status1: 0xE2,
		},
		{
			IPAddress: realEN4IP.As4(), BindIndex: 2,
			ShortName: "Port 2", LongName: "NETRON EN4",
			NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0},
			GoodInput: [4]byte{0x08, 0, 0, 0}, GoodOutputB: [4]byte{0x40, 0, 0, 0},
			SwIn: [4]byte{0x01, 0, 0, 0}, SwOut: [4]byte{0x01, 0, 0, 0},
			Status1: 0xE2,
		},
		{
			IPAddress: realEN4IP.As4(), BindIndex: 3,
			ShortName: "Port 3", LongName: "NETRON EN4",
			NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0},
			GoodInput: [4]byte{0x08, 0, 0, 0}, GoodOutputB: [4]byte{0x40, 0, 0, 0},
			SwIn: [4]byte{0x02, 0, 0, 0}, SwOut: [4]byte{0x02, 0, 0, 0},
			Status1: 0xE2,
		},
	}

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
	srv.Simulation = true
	// unknownOpcodeThrottle mirrors buildReal's — demo traffic never
	// actually produces an unrecognized opcode, but wiring the tap
	// identically to production keeps this a faithful exercise of the same
	// code path rather than a simplified stand-in.
	unknownOpcodeThrottle := capture.NewUnknownOpcodeThrottle(0)
	// periodicNodeThrottle mirrors buildReal's --lognodes wiring — see
	// capture.PeriodicNodeThrottle's doc comment.
	periodicNodeThrottle := capture.NewPeriodicNodeThrottle(0)
	tap := func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry {
		e := capture.DecodeEntry(dir, peer, data)
		// Add stamps Time/Seq on its own returned copy — use that stamped
		// copy for the RDM ring and disk logger (see main.go's buildReal
		// for the same fix and fuller explanation of the bug this avoids).
		e = ring.Add(e)
		switch {
		case capture.IsRDMLoggable(e):
			e = rdmRing.Add(e)
			srv.LogRDMEntry(e)
		case logNodes && capture.IsNodeConfigKind(e.Kind):
			e = rdmRing.Add(e)
			srv.LogRDMEntry(e)
		case logNodes && capture.IsPeriodicNodeKind(e.Kind):
			if logEntry, ok := periodicNodeThrottle.Consider(e); ok {
				logEntry = rdmRing.Add(logEntry)
				srv.LogRDMEntry(logEntry)
			}
		default:
			if logEntry, ok := unknownOpcodeThrottle.Consider(e); ok {
				logEntry = rdmRing.Add(logEntry)
				srv.LogRDMEntry(logEntry)
			}
		}
		return e
	}

	installDemoResponder(tport, rdmc, devices, tap, legacyRdmStartCode)
	installDemoNodeConfigResponder(tport, nodes, en4IP, wirelessIP, realEN4IP, realEN4PortReplies, tap)

	// start is deferred to the caller — see this function's doc comment.
	// Nothing above this point sends or delivers a single packet.
	start := func() {
		go reg.Run()

		// Deliver the two nodes' initial ArtPollReply now that the caller
		// has finished arming --logrdm/--lognodes, so this seed state —
		// the app's very first view of each node's ports — reaches the
		// capture rings/disk log like real wire traffic would, not just
		// the node table. See deliverPollReply's doc comment.
		deliverPollReply(nodes, tap, en4Reply, netip.AddrPortFrom(en4IP, session.ArtNetUDPPort))
		deliverPollReply(nodes, tap, auroraReply, netip.AddrPortFrom(wirelessIP, session.ArtNetUDPPort))
		for _, reply := range realEN4PortReplies {
			deliverPollReply(nodes, tap, reply, netip.AddrPortFrom(realEN4IP, session.ArtNetUDPPort))
		}

		// Phase 2a: warm every demo device's manufacturer/model/footprint
		// cache before installing the sample patch, so the reconcile
		// matcher's type-corroboration tiers (manufacturer/model/footprint
		// — internal/patch's scorePair) have real evidence to work with
		// immediately, matching what Dom would actually see: he always
		// opens the Devices tab and browses the rig before touching Patch.
		// Without this, every demo device would start with an unfetched
		// DEVICE_INFO/label (registry.Fixture's zero state), and
		// corroboration alone could never clear the match threshold — see
		// buildDemoPatch's doc comment on why manufacturer text alone
		// isn't enough. Best-effort and blocking (RealClock — this is
		// --demo, not a test — real responses land in 3-30ms per device,
		// run concurrently below, so this adds well under 100ms to
		// startup) — this same blocking call used to happen inside
		// buildDemo itself, so moving it here changes when it runs
		// relative to --logrdm being armed, not the startup time budget.
		warmDemoDeviceCaches(ctx, rdmc, devices)

		// Phase 2a: pre-load a sample patch (task ask, item 5: "the whole
		// flow is exercisable") deliberately covering every reconcile
		// classification plus a channel-overlap collision — see
		// buildDemoPatch's doc comment. Not wire traffic, so its ordering
		// relative to --logrdm doesn't matter, but it belongs here anyway:
		// buildDemoPatch's own doc comment on the "correct match" case
		// depends on warmDemoDeviceCaches (just above) having already run.
		srv.PatchStore.Replace(buildDemoPatch(port0, port1, port2))

		go generateDemoTraffic(ctx, ring)
	}

	return srv, start
}

// warmDemoDeviceCaches issues GET DEVICE_INFO / MANUFACTURER_LABEL /
// DEVICE_MODEL_DESCRIPTION for every demo device concurrently, populating
// registry.Fixture's classification/label cache exactly as if the Devices
// screen had already been opened for each — see buildDemo's call site for
// why Phase 2a's reconcile demo needs this warmed. Errors are ignored (a
// device that NACKs one of these, like beam1's DEVICE_MODEL_DESCRIPTION or
// splitter's MANUFACTURER_LABEL by design — see buildDemoDevices — simply
// keeps that one field at its documented fallback).
func warmDemoDeviceCaches(ctx context.Context, rdmc *session.RDMController, devices []*demoDevice) {
	var wg sync.WaitGroup
	for _, d := range devices {
		ref := session.NodeRef{
			Key:  session.NodeKey{IP: d.nodeIP, BindIndex: 1},
			Addr: netip.AddrPortFrom(d.nodeIP, session.ArtNetUDPPort),
			Port: d.port,
		}
		client := params.New(rdmc, ref, d.uid)
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, _ = client.DeviceInfo(cctx)
			_, _ = client.ManufacturerLabel(cctx)
			_, _ = client.DeviceModelDescription(cctx)
		}()
	}
	wg.Wait()
}

// buildDemoPatch returns a sample patch pre-loaded for --demo mode (task
// ask, item 5), deliberately covering every reconcile classification:
//
//   - correct match: chromaQ (port2/addr1) — address, footprint,
//     manufacturer AND model all agree (warmDemoDeviceCaches has already
//     populated its DEVICE_INFO/labels, mirroring a tech who's browsed the
//     Devices tab before opening Patch — the realistic order of operations).
//   - planted address mismatch: par1 is really at port1/addr81, but the
//     patch says addr90 — the matcher must find it via manufacturer+model
//     text corroboration alone (its live-reported label "Elation
//     Professional"/model "Par" both present in the patched fixture type,
//     scoring well above the propose threshold with no address help).
//   - fuzzy-name near-match: spot1, patched with extra descriptive words and
//     punctuation ("SR Truss - Ayrton, Spot!! (silver)") the device itself
//     doesn't report, at its correct address — Matched, with fuzzy evidence
//     attached.
//   - missing fixture: "High End Systems SolaFrame 750" shares no
//     meaningful tokens with any demo device (its one weak accidental
//     overlap, "Systems" against en4Root's "Obsidian Control Systems"
//     label, scores well under the match threshold), so it resolves
//     cleanly as Missing.
//   - channel-overlap collision: two invented "Practical" entries on an
//     otherwise-unused universe (both also read as Missing in the reconcile
//     view, which is correct — nothing on the demo rig answers on universe
//     5 — the point here is purely DetectCollisions' overlap finding).
//
// Every other demo device (wash1, wash2, beam1, wirelessMover, splitter,
// en4Root, auroraRoot — see buildDemoDevices) is deliberately left out of
// this patch entirely, so each shows up as an Unpatched reconcile row.
// wash1/wash2 in particular are BOTH left out on purpose: they're
// identically typed ("Robe"/"Wash", footprint 20) demo siblings, and
// referencing only one of them from the sample patch while its identical
// twin sits nearby would manufacture a confusing false ambiguity that has
// nothing to do with the scenario being demonstrated.
func buildDemoPatch(port0, port1, port2 artnet.PortAddress) patch.Patch {
	entry := func(name, fixtureType, position, fixtureNumber string, universe artnet.PortAddress, addr, footprint uint16) patch.Entry {
		return patch.Entry{
			ID: patch.NewEntryID(), Name: name, FixtureType: fixtureType,
			Position: position, FixtureNumber: fixtureNumber,
			Universe: universe.RawValue(), StartAddress: addr, Footprint: footprint,
		}
	}
	practicalsUniverse, err := artnet.PortAddressFromRaw(5)
	if err != nil {
		practicalsUniverse = port1 // demo-only fallback; 5 is always a valid raw Port-Address
	}

	// --- Task 5, function-aware Rig Check foundation: at least one demo
	// entry of each provenance the taxonomy has to distinguish (task
	// brief's hard constraint on decision 3 — GDTF-derived and RDM-inferred
	// must never look alike, and this demo data is the easiest place for
	// that to be checked by eye). Every other entry below is left with no
	// ChannelFunctions at all (the third, "neither" case) — normalized to
	// a non-nil empty map once installed via patch.Store, same as any
	// hand-entered entry that's never touched a GDTF/RDM import.

	// CF2 48 (GDTF-derived): a small, realistic slice of what
	// gdtfparse.js's channelFunctions rule would resolve for a 4-channel
	// Dimmer+RGB colour mode — Source==SourceGDTF throughout.
	cf2Channels := map[uint16]patch.ChannelFunction{
		1: {Source: patch.SourceGDTF, Attribute: "Dimmer", FunctionName: "Dimmer", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		2: {Source: patch.SourceGDTF, Attribute: "ColorAdd_R", FunctionName: "Red", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		3: {Source: patch.SourceGDTF, Attribute: "ColorAdd_G", FunctionName: "Green", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		4: {Source: patch.SourceGDTF, Attribute: "ColorAdd_B", FunctionName: "Blue", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
	}

	// Spot 1 (RDM-inferred): built the same way a real live SLOT_INFO GET
	// would be — via web.BuildRDMInferredChannelFunctions, the exact
	// function stage 2's UI-facing code path uses — so the demo rig is
	// honest evidence this path produces Source==SourceRDMInferred, not a
	// hand-typed shortcut that happens to look similar. 16-bit pan/tilt
	// (coarse+fine) plus a rotating gobo wheel with its two secondary
	// modifier slots, matching E1.20-2025 §10.6.4's own worked example
	// (see internal/rdm/slotinfo_test.go's TestDecodeSlotInfo_MovingHeadExample).
	spotSlots := []rdm.SlotInfoEntry{
		{Offset: 1, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
		{Offset: 2, Type: rdm.SlotTypeSecondaryFine, Value: 1},
		{Offset: 3, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDTilt)},
		{Offset: 4, Type: rdm.SlotTypeSecondaryFine, Value: 3},
		{Offset: 5, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDRotoGoboWheel)},
		{Offset: 6, Type: rdm.SlotTypeSecondaryControl, Value: 5},
		{Offset: 7, Type: rdm.SlotTypeSecondaryQuantumRotate, Value: 5},
	}
	spotDescriptions := map[uint16]string{5: "Gobo Wheel 1"} // this one slot's SLOT_DESCRIPTION was fetched; the rest fall back to their symbolic Slot Label ID name
	spotChannels := web.BuildRDMInferredChannelFunctions(spotSlots, spotDescriptions)

	// Beam FX 1 (GDTF-derived): a synthetic fixture whose sole purpose is
	// making every stage 2 test-pattern group exercisable in --demo (task
	// brief: "make every pattern exercisable in --demo") — CF2 48 and Spot
	// 1 above only cover Dimmer/Colour(mix)/Position between them, so
	// nothing in the demo rig otherwise has Focus, Beam, or Shaper
	// functions a pattern could drive. ChannelSets are attached to
	// ColorWheel and Prism1 so PatternColourWheelStep/PatternPrismInOut
	// have real slot detail to exercise (rather than only ever hitting the
	// missing-detail degrade path in demo mode); every other function here
	// deliberately has none, so the degrade-to-raw-sweep path (prism/
	// animation spin, shaper rotate) is ALSO exercisable in demo without a
	// second fixture.
	beamFXChannels := map[uint16]patch.ChannelFunction{
		1:  {Source: patch.SourceGDTF, Attribute: "Focus", FunctionName: "Focus", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		2:  {Source: patch.SourceGDTF, Attribute: "Zoom", FunctionName: "Zoom", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		3:  {Source: patch.SourceGDTF, Attribute: "Frost1", FunctionName: "Frost Light", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		4:  {Source: patch.SourceGDTF, Attribute: "Frost2", FunctionName: "Frost Heavy", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		5:  {Source: patch.SourceGDTF, Attribute: "Prism1", FunctionName: "Prism", DMXFrom: 0, DMXTo: 255, ChannelSets: []patch.ChannelSet{{Name: "Open", DMXFrom: 0}, {Name: "Prism In", DMXFrom: 128}}},
		6:  {Source: patch.SourceGDTF, Attribute: "Prism1Rot", FunctionName: "Prism Rotate", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		7:  {Source: patch.SourceGDTF, Attribute: "Animation1", FunctionName: "Animation Wheel", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		8:  {Source: patch.SourceGDTF, Attribute: "ColorWheel", FunctionName: "Colour Wheel", DMXFrom: 0, DMXTo: 255, ChannelSets: []patch.ChannelSet{{Name: "Open", DMXFrom: 0}, {Name: "Red", DMXFrom: 32}, {Name: "Green", DMXFrom: 64}, {Name: "Blue", DMXFrom: 96}}},
		9:  {Source: patch.SourceGDTF, Attribute: "Blade1A", FunctionName: "Shaper 1 Insert", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		10: {Source: patch.SourceGDTF, Attribute: "Blade2A", FunctionName: "Shaper 2 Insert", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		11: {Source: patch.SourceGDTF, Attribute: "Blade1Rot", FunctionName: "Shaper Assembly Rotate", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
		12: {Source: patch.SourceGDTF, Attribute: "Blade2Rot", FunctionName: "Shaper Assembly Rotate", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]patch.ChannelSet, 0)},
	}

	p := patch.Patch{
		Name: "Demo Show",
		Entries: []patch.Entry{
			entry("CF2 48", "Chroma-Q Color Force II 48", "US Truss 1", "101", port2, 1, 20),
			entry("Par 1", "Elation Professional Par", "US Truss 2", "102", port1, 90, 8),
			entry("Spot 1", "SR Truss - Ayrton, Spot!! (silver)", "SR Truss", "103", port1, 1, 24),
			entry("Missing Fixture", "High End Systems SolaFrame 750", "Rear Truss", "104", port1, 200, 20),
			entry("Practical 1", "Practical LED", "", "201", practicalsUniverse, 100, 10),
			entry("Practical 2", "Practical LED", "", "202", practicalsUniverse, 105, 10),
			entry("Beam FX 1", "Demo Beam FX Fixture", "US Truss 1", "105", port1, 150, 12),
			// Wash L exists to make the Reconcile screen's two hardest cases
			// exercisable in --demo, neither of which the entries above
			// produce:
			//
			//  1. AMBIGUITY. The rig has two identical Robe Wash fixtures
			//     (wash1 at address 1, wash2 at 21 — same manufacturer, same
			//     model, same footprint; see buildDemoDevices). Patching
			//     Wash L at an address NEITHER of them sits at makes both
			//     score identically (footprint 0.20 + manufacturer 0.15 +
			//     model 0.25 = 0.60, with neither collecting the 0.40 address
			//     weight), landing inside match.go's 0.10 ambiguityMargin. The
			//     matcher therefore proposes both and refuses to pick — exactly
			//     the row the Reconcile UI has to EXPLAIN rather than resolve,
			//     since committing the wrong one of two identical lights is the
			//     mistake the whole screen exists to prevent.
			//
			//  2. SUBSTITUTION. wash1 advertises PAN_INVERT/TILT_INVERT/
			//     PAN_TILT_SWAP and wash2 deliberately does not, so decommitting
			//     one and committing the other demonstrates both the inheritance
			//     of the entry's intended settings AND the honest "this fixture
			//     will not answer for that setting" degrade on the substitute.
			entry("Wash L", "Robe Wash", "SL Boom", "106", port0, 41, 20),
		},
	}
	p.Entries[0].ChannelFunctions = cf2Channels    // CF2 48: GDTF-derived
	p.Entries[2].ChannelFunctions = spotChannels   // Spot 1: RDM-inferred
	p.Entries[6].ChannelFunctions = beamFXChannels // Beam FX 1: GDTF-derived (synthetic — see above)
	// Par 1, Missing Fixture, Practical 1/2: left with a nil
	// ChannelFunctions (the "absent"/"neither" case) — normalized to a
	// non-nil empty map once this Patch is installed via patch.Store,
	// exactly like any hand-entered entry that's never seen an import.
	return p
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
	//
	// wash1 additionally exercises Phase D's whole PID set (task 4: "at
	// least one demo device supporting the service-life PIDs, pan/tilt
	// invert, display settings and RESET_DEVICE"): DEVICE_HOURS/LAMP_HOURS/
	// LAMP_STRIKES/LAMP_STATE/DEVICE_POWER_CYCLES, PAN_INVERT/TILT_INVERT/
	// PAN_TILT_SWAP, DISPLAY_INVERT/DISPLAY_LEVEL, FACTORY_DEFAULTS and
	// RESET_DEVICE. wash2 is deliberately left as a plain fixture with NONE
	// of these advertised (task 4's other half: "at least one that does
	// NOT, to prove the UI degrades") — same manufacturer/model as wash1 so
	// the only visible difference on the Devices screen is which Phase D
	// fields actually resolve.
	wash1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}, label: "Wash 1", mfrLabel: "Robe", model: "Wash",
		nodeIP: en4IP, port: port0, startAdr: 1,
		deviceInfo: basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		supportedExtra: []rdm.ParameterID{
			rdm.PIDDeviceHours, rdm.PIDLampHours, rdm.PIDLampStrikes, rdm.PIDLampState, rdm.PIDDevicePowerCycles,
			rdm.PIDPanInvert, rdm.PIDTiltInvert, rdm.PIDPanTiltSwap,
			rdm.PIDDisplayInvert, rdm.PIDDisplayLevel,
			rdm.PIDFactoryDefaults, rdm.PIDResetDevice,
			// E1.20 §10.11 "Device Control" — owner ask: "at least one demo
			// device supporting the full §10.11 set" (wash2 below stays
			// unsupported, to prove the UI degrades).
			rdm.PIDPowerState, rdm.PIDPerformSelfTest, rdm.PIDSelfTestDescription,
			rdm.PIDSelfTestEnhanced, rdm.PIDCapturePreset, rdm.PIDPresetPlayback,
		},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0},
			rdm.PIDDeviceHours:       rdm.EncodeUint32Counter(2140),
			rdm.PIDLampHours:         rdm.EncodeUint32Counter(430),
			rdm.PIDLampStrikes:       rdm.EncodeUint32Counter(58),
			rdm.PIDLampState:         rdm.EncodeLampState(rdm.LampOn),
			rdm.PIDDevicePowerCycles: rdm.EncodeUint32Counter(112),
			rdm.PIDPanInvert:         {0},
			rdm.PIDTiltInvert:        {0},
			rdm.PIDPanTiltSwap:       {0},
			rdm.PIDDisplayInvert:     {0},
			rdm.PIDDisplayLevel:      {255},
			rdm.PIDFactoryDefaults:   {0}, // not currently at factory defaults
			rdm.PIDPowerState:        rdm.EncodePowerState(rdm.PowerStateNormal),
			rdm.PIDPresetPlayback:    rdm.EncodePresetPlayback(rdm.PresetPlayback{Mode: rdm.PresetPlaybackOff, Level: 0xFF}),
		},
		selfTestDescs: map[byte]string{
			1: "Lamp Check",
			2: "Pan/Tilt Sweep",
		},
	}
	wash2 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}, label: "Wash 2", mfrLabel: "Robe", model: "Wash",
		nodeIP: en4IP, port: port0, startAdr: 21,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(21), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}
	// Spot 1 demonstrates report §1.1 item 4: a device that lists a
	// manufacturer PID in SUPPORTED_PARAMETERS but NACKs
	// PARAMETER_DESCRIPTION for it — the UI's raw-hex-editor fallback path.
	spot1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0xAAAA, DeviceID: 3}, label: "Spot 1", mfrLabel: "Ayrton", model: "Spot",
		nodeIP: en4IP, port: port1, startAdr: 1,
		deviceInfo: basePV(rdm.CategoryFixtureMovingMirr, 24, 3, 0),
		// rdm.PIDDMXPersonalityDescription (report brief: "surface which
		// personality that is") exercises the newly-wired PID with a
		// multi-personality device so both the Info section's readable
		// "Personality 1/3 — ..." line and the Parameters section's labeled
		// dropdown / "Show all personality names" action are demonstrable.
		supportedExtra: []rdm.ParameterID{0x8500, rdm.PIDDMXPersonalityDescription},
		noDescribe:     map[rdm.ParameterID]bool{0x8500: true},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 3}, rdm.PIDIdentifyDevice: {0},
			0x8500: {0xDE, 0xAD}, // opaque 2-byte value, only ever editable as raw hex in the UI
		},
		personalityDescs: map[byte]params.PersonalityDescription{
			1: {DMXFootprint: 24, Description: "24ch Extended"},
			2: {DMXFootprint: 16, Description: "16ch Standard"},
			3: {DMXFootprint: 8, Description: "8ch Basic"},
		},
	}
	// Beam 1 is the Devices screen's DEVICE_MODEL_DESCRIPTION NACK demo
	// (task ask): it answers MANUFACTURER_LABEL normally but NACKs the
	// model description, so the UI must fall back to DEVICE_INFO's numeric
	// Device Model ID (basePV's DeviceModelID: 1 -> "0x0001") rather than
	// showing a blank Model column.
	beam1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x4D50, DeviceID: 4}, label: "Beam 1", mfrLabel: "Martin Professional",
		noModelDescription: true,
		nodeIP:             en4IP, port: port1, startAdr: 41,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 16, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(41), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}
	par1 := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x454C, DeviceID: 5}, label: "Par 1", mfrLabel: "Elation Professional", model: "Par",
		nodeIP: en4IP, port: port1, startAdr: 81,
		deviceInfo:  basePV(rdm.CategoryFixtureFixed, 8, 1, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(81), rdm.PIDDMXPersonality: {1, 1}, rdm.PIDIdentifyDevice: {0}},
	}
	// model is a plain fixture name — deliberately NOT "(via Aurora proxy)"
	// or similar (task ask, item 2: a fixture reached through an RDM proxy
	// still identifies its own manufacturer/type over the wire and must
	// look like an ordinary device in the UI, not specially labeled). This
	// device is still exercised on the wireless/ACK_TIMER path (proxied:
	// true below drives the fake responder's timing, not any UI text).
	wirelessMover := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x6C74, DeviceID: 6}, label: "Wireless Mover", model: "Wireless Mover",
		nodeIP: wirelessIP, port: port0, startAdr: 1, proxied: true,
		deviceInfo:  basePV(rdm.CategoryFixtureMovingYoke, 20, 2, 0),
		paramValues: map[rdm.ParameterID][]byte{rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 2}, rdm.PIDIdentifyDevice: {0}},
	}

	// --- Chroma-Q Color Force II-ish fixture: manufacturer PIDs + sensors,
	// one sensor reading outside its normal band (report §7.1, §1.2). ---
	chromaQ := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}, label: "CF2 48", mfrLabel: "Chroma-Q", model: "Color Force II 48",
		nodeIP: en4IP, port: port2, startAdr: 1,
		deviceInfo: basePV(rdm.CategoryFixtureFixed, 20, 1, 2),
		// 0x8010-0x8012 are the pre-existing generic manufacturer-PID demo
		// PIDs (self-describing via PARAMETER_DESCRIPTION). rdm.PIDCurve/
		// PIDCurveDescription (E1.37-1, task ask: "a fake fixture exposes a
		// curve PID with descriptions so the labeled dropdown is
		// exercisable") are standard PIDs with dedicated typed Client
		// methods, not manufacturer PIDs — still must be listed here since
		// they're outside baseSupportedParams. rdm.PIDBurnIn (E1.37-1,
		// 0x0440) demonstrates the generic-ESTA-PID fallback path (task ask:
		// "at least one unrecognized ESTA PID hitting the generic path"): no
		// paramDescriptions entry below, so it NACKs PARAMETER_DESCRIPTION
		// and renders via the raw-hex fallback with its label resolved from
		// the PID-name table (capture.PIDName) rather than a device-reported
		// description.
		supportedExtra: []rdm.ParameterID{0x8010, 0x8011, 0x8012, rdm.PIDCurve, rdm.PIDCurveDescription, rdm.PIDBurnIn},
		paramDescriptions: map[rdm.ParameterID]rdm.ParameterDescription{
			0x8010: {PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16, Description: "PIXEL COUNT"},
			0x8011: {PID: 0x8011, PDLSize: 2, DataType: rdm.DSUnsignedWord, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitHertz, Prefix: rdm.PrefixNone, MinValue: 750, MaxValue: 24000, DefaultValue: 1500, Description: "REFRESH RATE"},
			0x8012: {PID: 0x8012, PDLSize: 1, DataType: rdm.DSBoolean, CommandClass: rdm.PDCommandClassGetSet, Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 1, DefaultValue: 0, Description: "LED FLIP"},
		},
		noDescribe: map[rdm.ParameterID]bool{rdm.PIDBurnIn: true},
		curveLabels: map[byte]string{
			1: "Linear", 2: "Square Law", 3: "S-Curve",
		},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDDMXStartAddress: dmxAddr(1), rdm.PIDDMXPersonality: {1, 1}, rdm.PIDIdentifyDevice: {0},
			0x8010: {16}, 0x8011: {0x05, 0xDC} /* 1500 */, 0x8012: {0},
			rdm.PIDCurve:  {3, 3}, // current=3 (S-Curve) of 3
			rdm.PIDBurnIn: {0x00},
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
	// footprint 0, no sensors (report §1.3/§4.1-4.2). Also the Devices
	// screen's MANUFACTURER_LABEL NACK demo — cheap splitters commonly
	// don't implement the optional label PIDs at all, so this exercises the
	// "fall back to the ESTA table" leg (0x1900 -> "ADJ Products LLC")
	// distinctly from en4Root's "device's own report differs from ESTA"
	// leg below. ---
	splitter := &demoDevice{
		uid: rdm.UID{ManufacturerID: 0x1900, DeviceID: 2}, label: "Splitter 1x4", model: "5-Pin Splitter",
		noManufacturerLabel: true,
		nodeIP:              en4IP, port: port2, startAdr: 0,
		deviceInfo:     basePV(rdm.CategoryDataDistribution, 0, 1, 0),
		productDetails: []rdm.ProductDetail{rdm.DetailSplitter},
		paramValues:    map[rdm.ParameterID][]byte{rdm.PIDIdentifyDevice: {0}},
	}

	// --- EN4 root: the gateway's own RDM identity (report feature C's
	// "node view should show this node's own RDM parameters"), footprint
	// 0, DATA_DISTRIBUTION + ETHERNET_NODE, plus a couple of E1.37-2 IP
	// PIDs so /api/device/{uid}/param/0705 etc. have something to show.
	// mfrLabel is deliberately more specific than the ESTA-table name for
	// en4RootUID's manufacturer ID (0x1900 -> "ADJ Products LLC" — Obsidian
	// Control Systems is part of the ADJ Group, a realistic case of a
	// device's own report outranking its registered ESTA entry): the
	// Devices screen's "prefer the device's own report" demo leg. ---
	// supportedExtra below is deliberately narrower than every E1.37-2 PID
	// pids_ext.go declares: PIDInterfaceHardwareAddressType1, PIDIPv4Zero-
	// confMode and PIDIPv4DefaultRoute have no confirmed byte layout
	// anywhere in this app (see internal/params/ipconfig.go's doc comment),
	// and PIDInterfaceRenewDHCP/PIDInterfaceReleaseDHCP have no typed Client
	// method at all — advertising PIDs this app cannot actually decode/send
	// would just make the demo device lie about what it offers.
	//
	// It's ALSO the only demo device that pairs E1.37-2 network config with
	// a real (if narrower-than-wash1) E1.20 §10.11 device-control set —
	// closing the coverage hole a verification pass found: no demo device
	// exercised the "Device control" and "Network configuration" panels
	// together, so their independence (arm state, layout, re-render) had
	// never actually been rendered side by side. A real Obsidian EN4
	// plausibly answers RESET_DEVICE/POWER_STATE and offers a self test
	// (e.g. a network-link check) alongside its IP config, so this is a
	// realistic combination, not a synthetic one built just to pair panels.
	// CAPTURE_PRESET/PRESET_PLAYBACK are deliberately NOT added here: those
	// are lighting-fixture scene features (E1.20 §10.11.6/7 talk in terms of
	// DMX slot values and playback levels) with no sensible meaning for a
	// DMX-over-Ethernet gateway that has no footprint of its own
	// (deviceInfo above is DMXFootprint 0) — wash1 already covers that half
	// of §10.11 for a device where it actually applies. FACTORY_DEFAULTS is
	// left out too: it's demonstrated on wash1 already and a gateway
	// reverting its whole network config via the generic FACTORY_DEFAULTS
	// PID (rather than a dedicated reset-to-DHCP action) isn't a case this
	// app models anywhere.
	en4Root := &demoDevice{
		uid: en4RootUID, label: "EN4-Demo", mfrLabel: "Obsidian Control Systems", model: "Netron EN4",
		nodeIP: en4IP, port: port0, startAdr: 0,
		deviceInfo:     basePV(rdm.CategoryDataDistribution, 0, 1, 0),
		productDetails: []rdm.ProductDetail{rdm.DetailEthernetNode},
		supportedExtra: []rdm.ParameterID{
			rdm.PIDListInterfaces, rdm.PIDInterfaceLabel, rdm.PIDIPv4CurrentAddress,
			rdm.PIDIPv4StaticAddress, rdm.PIDIPv4DHCPMode, rdm.PIDInterfaceApplyConfiguration,
			rdm.PIDDNSHostname, rdm.PIDDNSDomainName, rdm.PIDDNSNameServer,
			// E1.20 §10.11 subset plausible on a gateway (see doc comment
			// above for why CAPTURE_PRESET/PRESET_PLAYBACK/FACTORY_DEFAULTS
			// are excluded).
			rdm.PIDPowerState, rdm.PIDResetDevice,
			rdm.PIDPerformSelfTest, rdm.PIDSelfTestDescription, rdm.PIDSelfTestEnhanced,
		},
		paramValues: map[rdm.ParameterID][]byte{
			rdm.PIDIdentifyDevice:     {0},
			rdm.PIDListInterfaces:     {0, 0, 0, 1},
			rdm.PIDInterfaceLabel:     append([]byte{0, 0, 0, 1}, []byte("eth0")...),
			rdm.PIDIPv4CurrentAddress: append([]byte{0, 0, 0, 1}, append(en4IP.AsSlice(), 255, 255, 0, 0)...),
			rdm.PIDIPv4StaticAddress:  append([]byte{0, 0, 0, 1}, append(en4IP.AsSlice(), 255, 255, 0, 0)...),
			rdm.PIDIPv4DHCPMode:       {0, 0, 0, 1, byte(rdm.DHCPStatusInactive)},
			rdm.PIDDNSHostname:        []byte("netron-en4"),
			rdm.PIDDNSDomainName:      []byte("local"),
			rdm.PIDPowerState:         rdm.EncodePowerState(rdm.PowerStateNormal),
		},
		// A gateway's self test is a network-link check, not a lamp/pan-tilt
		// check like wash1's — deliberately different label content so the
		// side-by-side screenshot can't be mistaken for wash1's panel reused
		// verbatim.
		selfTestDescs: map[byte]string{
			1: "Network Link Test",
		},
	}

	// --- Aurora root: LumenRadio-style RDM proxy with a signal-quality
	// sensor OUTSIDE its normal band (poor signal — a second warning-state
	// demo distinct from the Chroma-Q's) and a non-empty
	// PROXIED_DEVICE_COUNT marking it as a proxy (report §1.3, §7.2). ---
	auroraRoot := &demoDevice{
		uid: auroraRootUID, label: "Aurora-Demo", mfrLabel: "LumenRadio", model: "Aurora",
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
func installDemoResponder(tport *session.FakeTransport, ctrl *session.RDMController, devices []*demoDevice, tap func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry, legacyRdmStartCode bool) {
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
		pkt := artnet.EncodeRdmPacket(resp, artnet.DefaultProtocolVersion, net, address, legacyRdmStartCode)
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

// demoIpProgCurrentSubnet/demoIpProgCurrentGateway are the simulated demo
// nodes' unprogrammed subnet mask and gateway — the gateway value matches
// RDM-LOG19's real captured ArtIpProgReply trailing bytes
// (`...19360000a9fe6b010000` decodes to gateway 169.254.107.1, a link-local
// address a node with no gateway configured reports) so --demo mode
// reproduces the exact bench evidence this fix was verified against.
var (
	demoIpProgCurrentSubnet  = [4]byte{255, 255, 0, 0}
	demoIpProgCurrentGateway = [4]byte{169, 254, 107, 1}
)

// installDemoNodeConfigResponder makes the two demo nodes "answer" ArtAddress/
// ArtInput/ArtIpProg the way a real node would (a follow-up ArtPollReply, or
// ArtIpProgReply for IP changes), so /api/node/{ip}/address, /input and
// /ipconfig are exercisable against --demo mode too (task ask: "the UI
// agent... can exercise everything with no hardware"). It chains onto
// whatever OnSend hook is already installed (installDemoResponder's RDM
// handling) rather than replacing it.
// demoNodeSlot identifies which of the three demo Art-Net node identities a
// sent packet's destination currently resolves to — installDemoNode
// ConfigResponder tracks each one's live address separately (see that
// func's doc comment).
type demoNodeSlot int

const (
	demoNodeNone demoNodeSlot = iota
	demoNodeEN4
	demoNodeWireless
	demoNodeRealEN4
)

func installDemoNodeConfigResponder(tport *session.FakeTransport, nodes *session.ArtNetSession, en4IP, wirelessIP, realEN4IP netip.Addr, realEN4PortReplies []artnet.PollReply, tap func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry) {
	// curEN4/curWireless/curRealEN4 track where each demo node actually
	// lives right now. They start at the seeded en4IP/wirelessIP/realEN4IP
	// and move when a simulated ArtIpProg reprograms a node's own static IP
	// (Command's Program-IP bit set, DHCP not requested) — mirroring bench
	// reality (RDM-LOG20): a real EN4 programmed from 2.11.90.4 to
	// 2.11.90.12 (this exact demo's realEN4IP address) answered the very
	// same ArtIpProg from its NEW address, and went on living there for
	// every packet after. Only these three demo nodes' own Art-Net
	// node-config identity moves this way — their RDM fixtures stay
	// addressed by the *original* en4IP/wirelessIP (buildDemoDevices'
	// nodeIP), since the task this simulates is specifically "does the
	// Nodes-screen table and follow-up node-config commands follow the
	// node," not a full re-routing of RDM traffic through a changed gateway
	// address.
	curEN4, curWireless, curRealEN4 := en4IP, wirelessIP, realEN4IP

	resolve := func(sp session.SentPacket) (ip netip.Addr, slot demoNodeSlot, ok bool) {
		switch sp.Dst.Addr() {
		case curEN4:
			return curEN4, demoNodeEN4, true
		case curWireless:
			return curWireless, demoNodeWireless, true
		case curRealEN4:
			return curRealEN4, demoNodeRealEN4, true
		default:
			return netip.Addr{}, demoNodeNone, false
		}
	}

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
			ip, _, ok := resolve(sp)
			if !ok {
				return
			}
			a := sp.Packet.Address
			reply := artnet.PollReply{
				IPAddress: ip.As4(), ShortName: a.ShortName, LongName: a.LongName,
				NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02,
			}
			time.AfterFunc(5*time.Millisecond, func() {
				deliverPollReply(nodes, tap, reply, netip.AddrPortFrom(ip, session.ArtNetUDPPort))
			})
		case artnet.KindInput:
			ip, _, ok := resolve(sp)
			if !ok {
				return
			}
			reply := artnet.PollReply{IPAddress: ip.As4(), NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02}
			time.AfterFunc(5*time.Millisecond, func() {
				deliverPollReply(nodes, tap, reply, netip.AddrPortFrom(ip, session.ArtNetUDPPort))
			})
		case artnet.KindIpProg:
			ip, slot, ok := resolve(sp)
			if !ok {
				return
			}
			p := sp.Packet.IpProg
			// Gate each field on its own Command bit, the way a real node
			// does — echoing back whatever bytes were in the request
			// regardless of the bits would make this simulator pass a
			// request built with the wrong bits (exactly RDM-LOG19's bug:
			// Command=0x83, ProgIP set but its bit never on) instead of
			// reproducing the real node's "current IP unchanged" reply.
			// That is precisely the failure mode a golden fixture/demo
			// built from the same assumption as the encoder would hide —
			// see internal/artnet/nodeconfig.go's file doc comment.
			reply := artnet.IpProgReply{
				CurrentIP:      ip.As4(),
				CurrentSubnet:  demoIpProgCurrentSubnet,
				CurrentPort:    session.ArtNetUDPPort,
				CurrentGateway: demoIpProgCurrentGateway,
			}
			// replyFrom is where the reply is sent FROM. It starts as the
			// address the request was sent to, and moves to the newly
			// programmed address below — RDM-LOG20's bench evidence,
			// reproduced (see rekeyNodeLocked/handleIPProgReply in
			// internal/session/artnetsession.go, the fix this exercises).
			replyFrom := ip
			var movedTo netip.Addr
			if p.Command&artnet.IpProgProgramIP != 0 {
				reply.CurrentIP = p.ProgIP
				if newIP, ok := netip.AddrFromSlice(p.ProgIP[:]); ok {
					newIP = newIP.Unmap()
					replyFrom = newIP
					movedTo = newIP
					switch slot {
					case demoNodeEN4:
						curEN4 = newIP
					case demoNodeWireless:
						curWireless = newIP
					case demoNodeRealEN4:
						curRealEN4 = newIP
					}
				}
			}
			if p.Command&artnet.IpProgProgramSubnetMask != 0 {
				reply.CurrentSubnet = p.ProgSubnetMask
			}
			if p.Command&artnet.IpProgProgramGateway != 0 {
				reply.CurrentGateway = p.ProgGateway
			}
			if p.Command&artnet.IpProgEnableDHCP != 0 {
				reply.Status = artnet.IpProgReplyDHCPEnabled
			}
			pkt := artnet.Encode(artnet.Packet{Kind: artnet.KindIpProgReply, IpProgReply: reply})
			addr := netip.AddrPortFrom(replyFrom, session.ArtNetUDPPort)
			time.AfterFunc(5*time.Millisecond, func() {
				// Route through tap first (so the capture rings/disk log see
				// the ArtIpProgReply exactly as real wire traffic would),
				// then fold it into the node session same as production.
				tap(capture.DirIn, addr, pkt)
				nodes.HandleInbound(session.Inbound{Data: pkt, From: addr})

				// realEN4IP reports itself over three separate bind-index
				// ArtPollReplies (see realEN4PortReplies' own doc comment) —
				// a genuine address change means all three now answer from
				// the new IP, so re-deliver each at movedTo the way a real
				// node emitting its "config changed" unsolicited ArtPollReply
				// per bind would. Ordered after the ArtIpProgReply above,
				// same as ProgramIP's own doc comment expects a node to
				// follow up.
				if slot == demoNodeRealEN4 && movedTo.IsValid() {
					for _, portReply := range realEN4PortReplies {
						moved := portReply
						moved.IPAddress = movedTo.As4()
						deliverPollReply(nodes, tap, moved, netip.AddrPortFrom(movedTo, session.ArtNetUDPPort))
					}
				}
			})
		}
	}
}

// deliverPollReply encodes reply as a real ArtPollReply datagram and routes
// it through tap before folding it into the node session (via
// HandleInbound, which is what dispatches an already-decoded ArtPollReply
// into HandlePollReply — see that method's doc comment). Calling
// nodes.HandlePollReply directly, as earlier revisions of this file did, is
// a legitimate shortcut for driving the node table in tests, but it
// silently means the bytes never reach the capture tap — so --demo mode
// could never actually exercise --lognodes' ArtPollReply rendering (the
// PortTypes/GoodInput/... byte-for-byte evidence report task 1 added
// --lognodes for). Going through tap first, exactly like the real UDP
// demux in main.go's buildReal, keeps --demo a faithful exercise of the
// same code path production traffic takes.
func deliverPollReply(nodes *session.ArtNetSession, tap func(dir capture.Direction, peer netip.AddrPort, data []byte) capture.Entry, reply artnet.PollReply, addr netip.AddrPort) {
	pkt := artnet.Encode(artnet.Packet{Kind: artnet.KindPollReply, PollReply: reply})
	tap(capture.DirIn, addr, pkt)
	nodes.HandleInbound(session.Inbound{Data: pkt, From: addr})
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
