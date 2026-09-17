package web

import (
	"encoding/json"
	"net/netip"
	"testing"

	"benny512/internal/autoread"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
)

func TestDevicePortSummaryUsesLatestTableAndReplyEvidence(t *testing.T) {
	ip := netip.MustParseAddr("2.11.90.1")
	port := mustPortAddr(t, 21)
	uids := make([]rdm.UID, 11)
	fixtures := make([]registry.Fixture, 0)
	states := map[uint32]autoread.State{}
	for i := range uids {
		uids[i] = rdm.UID{ManufacturerID: 0x4d50, DeviceID: uint32(i + 1)}
		fixtures = append(fixtures, registry.Fixture{UID: uids[i], Node: session.NodeKey{IP: ip, BindIndex: 1}, Port: port, HasResponded: i < 8})
		states[uint32(i+1)] = autoread.StateGaveUp
	}
	state := func(f registry.Fixture) autoread.State { return states[f.UID.DeviceID] }
	td := session.ToDSnapshot{IP: ip, PortAddress: 21, UIDs: uids, Complete: true}
	got := summarizeDevicePort(td, fixtures, state)
	if got.Advertised != 11 || got.Answered != 8 || got.NoResponse != 3 {
		t.Fatalf("LOG30 shape: %+v", got)
	}
	// Duplicate bindings and duplicate advertisements must not inflate counts.
	duplicate := fixtures[0]
	duplicate.Node.BindIndex = 2
	fixtures = append(fixtures, duplicate)
	td.UIDs = append(append([]rdm.UID{}, uids...), uids[0])
	if next := summarizeDevicePort(td, fixtures, state); next != got {
		t.Fatalf("duplicate changed counts: %+v", next)
	}
	// A fresh partial table drops old UIDs even though registry history retains them.
	td.UIDs = uids[7:]
	td.Complete = false
	states[9] = autoread.StateReading
	states[10] = autoread.StateUnknown
	got = summarizeDevicePort(td, fixtures, state)
	if got.Advertised != 4 || got.Answered != 1 || got.Pending != 1 || got.NotRead != 1 || got.NoResponse != 1 || got.Complete {
		t.Fatalf("partial: %+v", got)
	}
	// Another port's same UID is not evidence for this one.
	fixtures[7].Port = mustPortAddr(t, 11)
	got = summarizeDevicePort(td, fixtures, state)
	if got.Answered != 0 || got.NotRead != 2 {
		t.Fatalf("cross-port contamination: %+v", got)
	}
}

func TestDevicePortsEndpointReplacesAndClearsWithoutSending(t *testing.T) {
	h := newHarness(t)
	ip := netip.MustParseAddr("2.11.90.1")
	port := mustPortAddr(t, 21)
	uid := rdm.UID{ManufacturerID: 0x4d50, DeviceID: 1}
	seedToD(t, h, ip, port, []rdm.UID{uid})
	get := func() []devicePortSummary {
		t.Helper()
		before := h.tport.SentCount()
		rr := doJSON(t, h.srv.Handler(), "GET", "/api/devices/ports", nil)
		if rr.Code != 200 {
			t.Fatalf("HTTP %d: %s", rr.Code, rr.Body.String())
		}
		var out []devicePortSummary
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if h.tport.SentCount() != before {
			t.Fatal("summary sent lighting traffic")
		}
		return out
	}
	if got := get(); len(got) != 1 || got[0].Advertised != 1 || got[0].Answered != 0 {
		t.Fatalf("initial: %+v", got)
	}
	seedToD(t, h, ip, port, nil)
	if got := get(); len(got) != 1 || got[0].Advertised != 0 || !got[0].Complete {
		t.Fatalf("empty replacement: %+v", got)
	}
	h.rdmc.ClearToDPort(ip, port)
	if got := get(); len(got) != 0 {
		t.Fatalf("clear retained table: %+v", got)
	}
}
