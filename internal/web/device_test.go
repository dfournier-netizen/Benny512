package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
// response is ready or the deadline expires.
func (h *testHarness) runHTTPAsync(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, h.srv.Handler(), method, path, body)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.clock.Advance(2 * time.Millisecond)
		select {
		case r := <-done:
			return r
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for HTTP handler to resolve")
	return nil
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
