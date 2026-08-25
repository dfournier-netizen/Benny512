package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"benny512/internal/capture"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/session"
	"benny512/internal/walk"
)

func TestReset_RequiresConfirmString(t *testing.T) {
	cases := []map[string]string{
		{"confirm": "reset"}, // wrong case
		{"confirm": "yes"},   // wrong string entirely
		{"confirm": ""},      // empty
		{},                   // field omitted
	}
	for _, body := range cases {
		h := newHarness(t)
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("confirm=%q: status=%d, want 400: %s", body["confirm"], rr.Code, rr.Body.String())
		}
		var got map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["error"] != "confirmation required" {
			t.Errorf("error = %q, want %q", got["error"], "confirmation required")
		}
	}
}

func TestReset_RequiresConfirmString_DoesNotClearAnything(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: mustPort(t)}, uid)

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "nope"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rr.Code)
	}
	if fx := h.srv.Registry.Devices(); len(fx) != 1 {
		t.Fatalf("a rejected reset must not touch state: devices=%+v", fx)
	}
}

func TestReset_DeletesBothFiles(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	walkPath := filepath.Join(dir, "benny512-rigwalk.json")
	patchPath := filepath.Join(dir, "benny512-patch.json")
	h.srv.SetWalkStorePath(walkPath)
	h.srv.SetPatchStorePath(patchPath)

	h.srv.walkStore.Replace(walk.Session{Devices: []walk.Device{{UID: "1900:00000001"}}})
	h.srv.PatchStore.Replace(patch.Patch{Entries: []patch.Entry{{ID: "e1", Name: "Wash 1"}}})

	if _, err := os.Stat(walkPath); err != nil {
		t.Fatalf("walk file should exist before reset: %v", err)
	}
	if _, err := os.Stat(patchPath); err != nil {
		t.Fatalf("patch file should exist before reset: %v", err)
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp resetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Error("OK should be true")
	}
	if len(resp.Deleted) != 2 {
		t.Fatalf("Deleted = %+v, want 2 paths", resp.Deleted)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("Errors = %+v, want none", resp.Errors)
	}
	foundWalk, foundPatch := false, false
	for _, p := range resp.Deleted {
		switch p {
		case walkPath:
			foundWalk = true
		case patchPath:
			foundPatch = true
		}
	}
	if !foundWalk || !foundPatch {
		t.Fatalf("Deleted = %+v, want both %q and %q", resp.Deleted, walkPath, patchPath)
	}

	if _, err := os.Stat(walkPath); !os.IsNotExist(err) {
		t.Errorf("walk file should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(patchPath); !os.IsNotExist(err) {
		t.Errorf("patch file should be gone, stat err = %v", err)
	}

	// In-memory state cleared too, not just the files.
	if _, ok := h.srv.walkStore.Get(); ok {
		t.Error("walk session should be discarded in-memory as well")
	}
	if _, ok := h.srv.PatchStore.Get(); ok {
		t.Error("patch should be discarded in-memory as well")
	}
}

func TestReset_ToleratesAlreadyMissingFiles(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	// Configure paths but never create anything at them (mirrors a fresh
	// install that has never had a walk session or patch, and --demo mode's
	// PatchStore, which never gets a path configured at all).
	h.srv.SetWalkStorePath(filepath.Join(dir, "benny512-rigwalk.json"))
	h.srv.SetPatchStorePath(filepath.Join(dir, "benny512-patch.json"))

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp resetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Error("OK should be true even when nothing was on disk to delete")
	}
	if len(resp.Deleted) != 0 {
		t.Errorf("Deleted = %+v, want none (nothing existed)", resp.Deleted)
	}
	if len(resp.Errors) != 0 {
		t.Errorf("Errors = %+v, want none (a missing file is not an error)", resp.Errors)
	}
}

func TestReset_ToleratesUnconfiguredStorePaths(t *testing.T) {
	// A Server built via New() directly (every test in this package, and
	// --demo mode's PatchStore — see cmd/benny512/main.go) never calls
	// SetWalkStorePath/SetPatchStorePath at all, so both paths are "".
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp resetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Deleted) != 0 || len(resp.Errors) != 0 {
		t.Errorf("resp = %+v, want empty Deleted/Errors when no store path was ever configured", resp)
	}
}

func TestReset_StopsRigCheck(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "A", Universe: pa.RawValue(), StartAddress: 1, Footprint: 4})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", rigCheckStartRequest{ScopeKind: "all", Mode: "highlight", Level: 200})
	if rr.Code != http.StatusOK {
		t.Fatalf("rigcheck start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st rigCheckStateJSON
	mustUnmarshal(t, rr, &st)
	if !st.Running {
		t.Fatal("rig check should be running before reset")
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("reset: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck", nil)
	mustUnmarshal(t, rr, &st)
	if st.Running {
		t.Error("rig check should be stopped by reset")
	}

	// The universe it was driving must be blacked out, not just marked
	// stopped (task ask: "never leave the rig lit").
	frame, ok := h.srv.DMX.Frame(pa)
	if ok {
		for i, b := range frame {
			if b != 0 {
				t.Fatalf("frame slot %d = %d after reset, want 0", i, b)
			}
		}
	}
}

func TestReset_BlacksOutAndStopsDirectDMXOutsideAnyRigCheck(t *testing.T) {
	// A universe lit via a plain /api/dmx send (no rig check involved) must
	// still be zeroed by reset — RigCheck.Stop alone only knows about
	// universes it started itself.
	h := newHarness(t)
	pa := mustPortAddr(t, 0)
	doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: pa.RawValue(), Channels: map[string]byte{"1": 255}})
	frame, ok := h.srv.DMX.Frame(pa)
	if !ok || frame[0] != 255 {
		t.Fatalf("seed: frame[0] = %v ok=%v, want 255/true", frame, ok)
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	frame, ok = h.srv.DMX.Frame(pa)
	if !ok || frame[0] != 0 {
		t.Fatalf("frame[0] after reset = %v ok=%v, want 0/true", frame, ok)
	}
}

func TestReset_ClearsRegistryNodeTableToDAndCaches(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	pa := mustPort(t)
	uid := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	ref := session.NodeRef{Key: node.Key, Addr: node.Addr, Port: pa}
	h.srv.Registry.NoteFixture(ref, uid)
	seedToD(t, h, node.Key.IP, pa, []rdm.UID{uid})
	h.srv.Capture.Add(capture.Entry{Kind: "ArtDmx", Size: 10})
	h.srv.RDMCapture.Add(capture.Entry{Kind: "ArtRdm", Size: 20})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	if fx := h.srv.Registry.Devices(); len(fx) != 0 {
		t.Errorf("devices after reset = %+v, want none", fx)
	}
	if nodes := h.srv.Registry.Nodes(); len(nodes) != 0 {
		t.Errorf("nodes after reset = %+v, want none (unlike devices/clear, reset DOES wipe the node table)", nodes)
	}
	if _, ok := h.rdmc.ToD(node.Key.IP, pa); ok {
		t.Error("ToD cache after reset should be gone")
	}
	if got := h.srv.Capture.Snapshot(capture.Filter{}, 0); len(got) != 0 {
		t.Errorf("general capture ring after reset = %+v, want empty", got)
	}
	if got := h.srv.RDMCapture.Snapshot(capture.Filter{}, 0); len(got) != 0 {
		t.Errorf("RDM capture ring after reset = %+v, want empty", got)
	}
}

func TestReset_ResetsSettingsToDefaults(t *testing.T) {
	h := newHarness(t)
	custom := Settings{NIC: "eth0", PollIntervalMS: 9999, CaptureLimit: 42, TimeoutProfiles: map[string]string{"x": "y"}}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", custom); rr.Code != http.StatusOK {
		t.Fatalf("seed settings: status=%d", rr.Code)
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	got := h.srv.SettingsSnapshot()
	want := defaultSettings()
	if got.NIC != want.NIC || got.PollIntervalMS != want.PollIntervalMS || got.CaptureLimit != want.CaptureLimit || got.LogRDMPath != want.LogRDMPath {
		t.Errorf("settings after reset = %+v, want defaults %+v", got, want)
	}
}

func TestReset_ExitingFalseWithoutShutdownHook(t *testing.T) {
	h := newHarness(t) // OnShutdownRequest left nil, as every test-built Server does
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	var resp resetResponse
	mustUnmarshal(t, rr, &resp)
	if resp.Exiting {
		t.Error("Exiting should be false when OnShutdownRequest is nil")
	}
}

func TestReset_ExitingTrueAndShutdownHookFiresAfterResponding(t *testing.T) {
	h := newHarness(t)
	fired := make(chan string, 1)
	h.srv.OnShutdownRequest = func(reason string) { fired <- reason }

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	var resp resetResponse
	mustUnmarshal(t, rr, &resp)
	if !resp.Exiting {
		t.Fatal("Exiting should be true when OnShutdownRequest is set")
	}

	select {
	case reason := <-fired:
		if reason != "reset" {
			t.Errorf("shutdown reason = %q, want %q", reason, "reset")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnShutdownRequest was never invoked")
	}
}
