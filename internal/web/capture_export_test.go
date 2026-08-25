package web

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/rdm"
)

var testExportPeer = netip.MustParseAddrPort("10.0.0.5:6454")

// seedRDMExchange writes one GET DEVICE_INFO request + ACK response pair
// directly into the harness's capture rings (as the real capture tap would
// on the wire), and returns the responder's UID plus both entries.
func seedRDMExchange(h *testHarness) (rdm.UID, capture.Entry, capture.Entry) {
	src := rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1}
	dst := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}

	req := rdm.Message{
		DestinationUID: dst, SourceUID: src, TransactionNumber: 5,
		PortIDOrResponseType: 1, CommandClass: rdm.GetCommand, ParameterID: rdm.PIDDeviceInfo,
	}
	reqPkt := artnet.EncodeRdmPacket(req, artnet.DefaultProtocolVersion, 0, 0, false)
	reqRaw := artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: reqPkt})
	reqEntry := h.srv.RDMCapture.AddPacket(capture.DirOut, testExportPeer, reqRaw)
	h.srv.Capture.Add(reqEntry)

	resp := rdm.Message{
		DestinationUID: src, SourceUID: dst, TransactionNumber: 5,
		PortIDOrResponseType: byte(rdm.ResponseACK), CommandClass: rdm.GetCommandResponse,
		ParameterID:   rdm.PIDDeviceInfo,
		ParameterData: make([]byte, 19), // valid DEVICE_INFO length, zeroed
	}
	respPkt := artnet.EncodeRdmPacket(resp, artnet.DefaultProtocolVersion, 0, 0, false)
	respRaw := artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: respPkt})
	respEntry := h.srv.RDMCapture.AddPacket(capture.DirIn, testExportPeer, respRaw)
	h.srv.Capture.Add(respEntry)

	return dst, reqEntry, respEntry
}

func TestRDMCaptureSnapshot_FiltersByUID(t *testing.T) {
	h := newHarness(t)
	uid, _, _ := seedRDMExchange(h)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/rdm/snapshot?uid="+uid.String(), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries (request+response) for uid %s, got %d: %s", uid, len(out), rr.Body.String())
	}

	rrNone := doJSON(t, h.srv.Handler(), "GET", "/api/capture/rdm/snapshot?uid=9999:00000009", nil)
	var outNone []map[string]any
	json.Unmarshal(rrNone.Body.Bytes(), &outNone)
	if len(outNone) != 0 {
		t.Fatalf("expected 0 entries for unrelated uid, got %d", len(outNone))
	}
}

func TestRDMCaptureSnapshot_BadUIDRejected(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/rdm/snapshot?uid=not-a-uid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestCaptureExport_JSONFormat(t *testing.T) {
	h := newHarness(t)
	seedRDMExchange(h)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/export?format=json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q, want attachment with .json filename", cd)
	}
	var doc struct {
		Header struct {
			AppVersion string `json:"appVersion"`
			EntryCount int    `json:"entryCount"`
		} `json:"header"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal export doc: %v\nbody: %s", err, rr.Body.String())
	}
	if doc.Header.AppVersion == "" {
		t.Error("Header.AppVersion is empty")
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 exported entries, got %d", len(doc.Entries))
	}
}

func TestCaptureExport_TXTFormatIsReadable(t *testing.T) {
	h := newHarness(t)
	seedRDMExchange(h)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/export?format=txt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Benny512 RDM Capture Export", "App version:", "Generated:", "NIC:", "Scope:",
		"Exchange 1", "GET_COMMAND", "GET_COMMAND_RESPONSE", "DEVICE_INFO", "round-trip:", "hex:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("TXT export missing %q:\n%s", want, body)
		}
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".txt") {
		t.Errorf("Content-Disposition = %q, want .txt filename", cd)
	}
}

func TestCaptureExport_ScopedToDevice(t *testing.T) {
	h := newHarness(t)
	uid, _, _ := seedRDMExchange(h)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/export?format=json&uid="+uid.String(), nil)
	var doc struct {
		Header struct {
			Scope string `json:"scope"`
		} `json:"header"`
		Entries []map[string]any `json:"entries"`
	}
	json.Unmarshal(rr.Body.Bytes(), &doc)
	if !strings.Contains(doc.Header.Scope, uid.String()) {
		t.Errorf("Scope = %q, want it to mention the device uid", doc.Header.Scope)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 entries scoped to the device, got %d", len(doc.Entries))
	}
}

func TestCaptureExport_BadFormatRejected(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/export?format=xml", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestLogRDMEntry_WritesToConfiguredFile(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	path := dir + "/rdm.log"
	if err := h.srv.SetLogRDMPath(path); err != nil {
		t.Fatalf("SetLogRDMPath: %v", err)
	}
	defer h.srv.Close()

	_, reqEntry, _ := seedRDMExchange(h)
	h.srv.LogRDMEntry(reqEntry)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "GET_COMMAND") {
		t.Errorf("log file missing expected content:\n%s", data)
	}
}
