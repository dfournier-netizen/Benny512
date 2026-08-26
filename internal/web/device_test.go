package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// wireDeviceResponder scripts h.tport to answer any RDM request to uid
// using handler, delivering the response via a clock-fired callback (never
// synchronously — session.RDMController's Submit path holds its own mutex
// across the Send call, so a synchronous HandleRDMResponse from inside
// OnSend would deadlock; see internal/params's dynamicResponder, which
// this mirrors).
func (h *testHarness) wireDeviceResponder(uid rdm.UID, handler func(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason, messageCount byte)) {
	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		data, nack, reason, messageCount := handler(msg)
		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
		}
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseNackReason), SubDevice: msg.SubDevice, MessageCount: messageCount,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: []byte{byte(reason >> 8), byte(reason)},
			}
		} else {
			if msg.CommandClass == rdm.SetCommand {
				data = nil
			}
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice, MessageCount: messageCount,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
			}
		}
		h.clock.AfterFunc(time.Millisecond, func() { h.rdmc.HandleRDMResponse(resp) })
	}
}

// runHTTPAsync drives an HTTP request against handler on its own goroutine
// (since it blocks on RDM round-trips) while advancing h.clock until the
// response is ready.
//
// This intentionally does NOT race a real wall-clock deadline against the
// fake clock. An earlier version looped on `time.Now().Before(deadline)`
// with a 1ms real sleep between fake-clock advances: under CPU contention
// (parallel test processes, -race overhead, a loaded CI box) the real
// deadline could expire before the handler goroutine had been scheduled
// enough times to make progress, even though the fake-clock-driven state
// machine itself hadn't stalled and would have resolved fine given more
// wall-clock slack. That produced flaky failures with no logic bug behind
// them — sometimes a timeout, sometimes a downstream assertion tripped by
// an incompletely-processed response. Bounding on a fixed number of fake
// advances instead (with a cooperative yield, not a real sleep, in between)
// makes completion depend only on logical progress, never on how much real
// CPU time the scheduler happened to hand this goroutine.
func (h *testHarness) runHTTPAsync(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, h.srv.Handler(), method, path, body)
	}()

	// 20,000 advances of 2ms of fake time each is 40 fake seconds of
	// protocol time to resolve in — orders of magnitude more than any
	// real RDM round-trip/backoff in this codebase needs — while imposing
	// no real-time budget at all, so it can't be raced by CPU contention.
	const maxAdvances = 20000
	for i := 0; i < maxAdvances; i++ {
		select {
		case r := <-done:
			h.waitForRegistrySync(t)
			return r
		default:
		}
		h.clock.Advance(2 * time.Millisecond)
		select {
		case r := <-done:
			h.waitForRegistrySync(t)
			return r
		default:
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for HTTP handler to resolve (no progress after many fake-clock advances — a real hang, not scheduling jitter)")
	return nil
}

// waitForRegistrySync closes a second, independent race behind the same
// symptom: RDMController.Command.done fires the instant a command
// completes, but that completion only reaches the fixture table when
// Registry.Run() — a separate goroutine started by newHarness — later
// drains the corresponding EventCommandComplete off RDMController.Events()
// and applies it via handleRDMEvent (see completeLocked: it sends on
// cmd.done, *then* emits the event — so the HTTP response the caller just
// received can legitimately arrive before Registry has seen the same
// completion at all). A caller that immediately inspects Registry state
// right after runHTTPAsync returns (as
// TestDevicesBackfillPopulatesManufacturerAndModel does via /api/fixtures)
// would otherwise race that goroutine under contention — reproduced by
// running this file's flaky test under artificial CPU load until it failed,
// not merely inferred.
//
// The fix borrows devicesclear_test.go's seedToD technique (see its doc
// comment) and generalizes it into a barrier: submit a harmless broadcast
// RDM command (broadcasts complete synchronously in issueLocked, no
// simulated device required) and block until *that* command's own
// EventCommandComplete is republished on Registry.RDMEvents(). Registry.Run
// drains RDMController.Events() strictly in arrival order, one event at a
// time, handling each fully (handleRDMEvent) before republishing it and
// moving to the next — so by construction every event queued ahead of the
// sentinel (in particular, whatever the just-completed HTTP call produced)
// has already been applied by the time the sentinel's own event comes back
// out. That is a deterministic proof, not a poll-and-hope: it holds
// regardless of how many RDM round trips the request triggered, including
// zero (a request rejected before ever reaching Submit).
func (h *testHarness) waitForRegistrySync(t *testing.T) {
	t.Helper()
	sentinelUID := rdm.Broadcast(0x7FFE) // ESTA prototyping range; no test fixture ever uses it
	sentinelNode := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("0.0.0.1"), BindIndex: 0xFF},
		Addr: netip.MustParseAddrPort("0.0.0.1:6454"),
		Port: mustPort(t),
	}
	h.rdmc.Get(sentinelNode, sentinelUID, 0x0000, nil)

	const maxAdvances = 20000
	for i := 0; i < maxAdvances; i++ {
		select {
		case ev := <-h.srv.Registry.RDMEvents():
			if ev.Kind == session.EventCommandComplete && ev.UID == sentinelUID {
				return
			}
		default:
			h.clock.Advance(2 * time.Millisecond)
			runtime.Gosched()
		}
	}
	t.Fatal("timed out waiting for Registry to catch up with RDM completion events")
}

func TestDeviceParamsFlow(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	pd := rdm.ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16,
		Description: "PIXEL COUNT",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)
	current := byte(10)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0, 0
		case 0x8010:
			if msg.CommandClass == rdm.SetCommand {
				current = msg.ParameterData[0]
				return nil, false, 0, 0
			}
			return []byte{current}, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/param/8010", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET param status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got paramValueJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Kind != "int" || got.Int != 10 {
		t.Fatalf("got %+v", got)
	}
	if !got.Descriptor.SelfDescribing || got.Descriptor.Label != "PIXEL COUNT" {
		t.Fatalf("descriptor=%+v", got.Descriptor)
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/param/8010", map[string]any{"value": 12})
	if rr.Code != http.StatusOK {
		t.Fatalf("SET param status=%d body=%s", rr.Code, rr.Body.String())
	}
	if current != 12 {
		t.Fatalf("device-side value = %d, want 12", current)
	}

	// Out-of-range SET should be rejected with 4xx and not reach the device.
	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/param/8010", map[string]any{"value": 999})
	if rr.Code < 400 {
		t.Fatalf("expected error status for out-of-range SET, got %d", rr.Code)
	}
	if current != 12 {
		t.Fatalf("out-of-range SET should not reach device: current=%d", current)
	}

	// Cached descriptor list should now include 0x8010.
	rr2 := doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET params status=%d", rr2.Code)
	}
	var list []paramDescriptorJSON
	if err := json.Unmarshal(rr2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, d := range list {
		if d.PID == "8010" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 8010 in cached descriptors: %+v", list)
	}
}

func TestDeviceSensorsEndpoint(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 2}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	deviceInfo := params.EncodeDeviceInfo(params.DeviceInfo{ProtocolVersionMajor: 1, SensorCount: 1})
	def := rdm.EncodeSensorDefinition(rdm.SensorDefinition{
		SensorNumber: 0, Type: rdm.SensorVoltage, Unit: rdm.UnitVoltsDC, Prefix: rdm.PrefixNone,
		RangeMin: 0, RangeMax: 300, NormalMin: 100, NormalMax: 250, SupportsRecording: 0x03,
		Description: "Signal Quality",
	})
	val := rdm.EncodeSensorValue(rdm.SensorValue{SensorNumber: 0, Present: 50, Lowest: 10, Highest: 60, Recorded: 50})
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDDeviceInfo:
			return deviceInfo, false, 0, 0
		case rdm.PIDSensorDefinition:
			return def, false, 0, 0
		case rdm.PIDSensorValue:
			if msg.CommandClass == rdm.SetCommand {
				return nil, false, 0, 0
			}
			return val, false, 0, 0
		case rdm.PIDRecordSensors:
			return nil, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/sensors", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var readings []sensorReadingJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &readings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(readings) != 1 || readings[0].Present != 50 {
		t.Fatalf("readings=%+v", readings)
	}
	// present=50 falls outside the normal band [100,250] — that's the
	// out-of-band-warning-state case the demo mode is meant to exercise.
	if readings[0].InNormalBand {
		t.Fatalf("50 should read as outside the [100,250] normal band: %+v", readings[0])
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/sensors/record", map[string]any{"sensor": 0})
	if rr.Code != http.StatusOK {
		t.Fatalf("record status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestIntrospectEndpointAsyncCompletion(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 3}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{0x8020})
	pdBytes := rdm.EncodeParameterDescription(rdm.ParameterDescription{
		PID: 0x8020, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGet,
		Description: "REFRESH RATE",
	})
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0, 0
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	// POST /introspect kicks off the walk on its own goroutine and must
	// return immediately (202) without waiting for RDM traffic.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uidStr+"/introspect", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Advance the fake clock repeatedly until the background introspection
	// goroutine's RDM traffic resolves and the descriptor becomes visible
	// via the cached-descriptors endpoint.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.clock.Advance(2 * time.Millisecond)
		listRR := doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
		var list []paramDescriptorJSON
		if err := json.Unmarshal(listRR.Body.Bytes(), &list); err == nil && len(list) == 1 && list[0].PID == "8020" {
			if list[0].Label != "REFRESH RATE" {
				t.Fatalf("descriptor=%+v", list[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for introspection to populate the descriptor cache")
}

func TestDeviceUnknownUID(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/device/5370:00000099/sensors", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeviceBadUID(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/device/not-a-uid/sensors", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func mustPort(t *testing.T) artnet.PortAddress {
	t.Helper()
	p, err := artnet.NewPortAddress(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
