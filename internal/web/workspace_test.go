package web

import (
	"benny512/internal/patch"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceGroupsPresetsAndBaselinesPersist(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "show.json")
	h.srv.SetPatchStorePath(path)
	e := patch.Entry{ID: "a", Name: "Wash", StartAddress: 1, Footprint: 1, ChannelFunctions: map[uint16]patch.ChannelFunction{1: {Source: patch.SourceGDTF, Attribute: "Dimmer"}}}
	h.srv.PatchStore.Replace(patch.Patch{Name: "Show", Entries: []patch.Entry{e}})
	if _, err := h.srv.RigCheck.SetPatternTests([]patch.Entry{e}, []patch.PatternSpec{{Kind: patch.PatternDimmerSine}}, false); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"save-group", "save-preset", "snapshot"} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/"+action, map[string]any{"name": action, "entryIds": []string{"a"}})
		if rr.Code != 200 {
			t.Fatalf("%s: %d %s", action, rr.Code, rr.Body.String())
		}
	}
	p, _ := patch.NewStore(path).Get()
	data := workspaceFor(p)
	if len(data.Groups) != 1 || len(data.Presets) != 1 || len(data.Baselines) != 1 {
		t.Fatalf("%+v", data)
	}
	if len(data.Baselines[0].Entries[0].ChannelFunctions) != 0 || data.Baselines[0].ProfileDigests["a"] == "" {
		t.Fatal("baseline should store a profile digest, not duplicate channel maps")
	}
	if got := baselineChanges(data.Baselines[0], p, nil); len(got) != 0 {
		t.Fatalf("unchanged rig: %v", got)
	}
	h.srv.RigCheck.StartPatternOutput()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/load-preset", map[string]string{"id": data.Presets[0].ID})
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if h.srv.RigCheck.PatternStatus().OutputEnabled || h.srv.DMX.OutputRunning() {
		t.Fatal("preset load must stop output")
	}
	h.srv.PatchStore.Mutate(func(p *patch.Patch) error { p.Entries[0].StartAddress = 42; return nil })
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/workspace/report/"+data.Baselines[0].ID, nil)
	var report struct {
		Changes []string `json:"changes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 1 {
		t.Fatalf("address drift missing: %s", rr.Body.String())
	}
}

func TestWorkspaceRejectsPresetWithRemovedFixture(t *testing.T) {
	h := newHarness(t)
	d := showWorkspace{Presets: []savedTestPreset{{savedGroup: savedGroup{ID: "one", Name: "Old", EntryIDs: []string{"gone"}}, Specs: []patch.PatternSpec{{Kind: patch.PatternDimmerSine}}}}}
	b, _ := json.Marshal(d)
	h.srv.PatchStore.Replace(patch.Patch{Name: "Show", Workspace: b})
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/load-preset", map[string]string{"id": "one"})
	if rr.Code != 409 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

func TestBaselineIgnoresObservationTimeButDetectsCoverageLoss(t *testing.T) {
	d := observedFixture{UID: "x", LastSeen: time.Unix(1, 0), AddressKnown: true, Address: 1}
	b := rigBaseline{Devices: []observedFixture{d}}
	d.LastSeen = time.Unix(100, 0)
	if len(baselineChanges(b, patch.Patch{}, []observedFixture{d})) != 0 {
		t.Fatal("time is not configuration drift")
	}
	d.AddressKnown = false
	if len(baselineChanges(b, patch.Patch{}, []observedFixture{d})) != 1 {
		t.Fatal("read coverage change not reported")
	}
}

func TestWorkspaceDeletesOnlyRequestedSavedItem(t *testing.T) {
	h := newHarness(t)
	h.srv.SetPatchStorePath(filepath.Join(t.TempDir(), "show.json"))
	d := showWorkspace{Groups: []savedGroup{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}, Presets: []savedTestPreset{}, Baselines: []rigBaseline{}}
	b, _ := json.Marshal(d)
	h.srv.PatchStore.Replace(patch.Patch{Name: "Show", Workspace: b})
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/delete-group", map[string]string{"id": "one"})
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	p, _ := h.srv.PatchStore.Get()
	got := workspaceFor(p)
	if len(got.Groups) != 1 || got.Groups[0].ID != "two" {
		t.Fatalf("%+v", got)
	}
	if _, err := h.srv.PatchStore.RecoverActive(); err != nil {
		t.Fatal(err)
	}
	p, _ = h.srv.PatchStore.Get()
	if len(workspaceFor(p).Groups) != 2 {
		t.Fatal("deleted group was not recoverable")
	}
}
