package walk

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSessionSummary(t *testing.T) {
	sess := Session{Devices: []Device{
		{UID: "a", Status: StatusConfirmed},
		{UID: "b", Status: StatusConfirmed},
		{UID: "c", Status: StatusProblem},
		{UID: "d", Status: StatusUnvisited},
		{UID: "e", Status: StatusUnvisited},
	}}
	got := sess.Summary()
	want := Summary{Total: 5, Confirmed: 2, Problems: 1, Remaining: 2}
	if got != want {
		t.Fatalf("Summary() = %+v, want %+v", got, want)
	}
}

func TestSessionIndexOfAndCurrentDevice(t *testing.T) {
	sess := Session{
		Devices: []Device{{UID: "a"}, {UID: "b"}, {UID: "c"}},
		Current: 1,
	}
	if idx := sess.IndexOf("c"); idx != 2 {
		t.Errorf("IndexOf(c) = %d, want 2", idx)
	}
	if idx := sess.IndexOf("nope"); idx != -1 {
		t.Errorf("IndexOf(nope) = %d, want -1", idx)
	}
	dev, ok := sess.CurrentDevice()
	if !ok || dev.UID != "b" {
		t.Errorf("CurrentDevice() = %+v, %v; want b, true", dev, ok)
	}

	sess.Current = -1
	if _, ok := sess.CurrentDevice(); ok {
		t.Error("CurrentDevice() with Current=-1 should report ok=false")
	}
	sess.Current = 99
	if _, ok := sess.CurrentDevice(); ok {
		t.Error("CurrentDevice() with out-of-range Current should report ok=false")
	}
}

func TestStoreGetNoSession(t *testing.T) {
	st := NewStore("")
	if _, ok := st.Get(); ok {
		t.Fatal("fresh store should report no active session")
	}
	if _, err := st.Mutate(func(*Session) error { return nil }); err != ErrNoSession {
		t.Fatalf("Mutate on empty store: err = %v, want ErrNoSession", err)
	}
}

func TestStoreReplaceAndMutate(t *testing.T) {
	st := NewStore("")
	sess := st.Replace(Session{
		Devices: []Device{{UID: "a", Status: StatusUnvisited}, {UID: "b", Status: StatusUnvisited}},
		Current: 0,
	})
	if sess.CreatedAt.IsZero() || sess.UpdatedAt.IsZero() {
		t.Fatal("Replace should stamp CreatedAt/UpdatedAt")
	}

	before := sess.UpdatedAt
	time.Sleep(time.Millisecond)
	updated, err := st.Mutate(func(s *Session) error {
		s.Devices[0].Status = StatusConfirmed
		return nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if updated.Devices[0].Status != StatusConfirmed {
		t.Errorf("mutation didn't stick: %+v", updated.Devices[0])
	}
	if !updated.UpdatedAt.After(before) {
		t.Error("Mutate should bump UpdatedAt")
	}

	// Get() must hand back a defensive copy — mutating the returned slice
	// must not corrupt the store's own state.
	got, _ := st.Get()
	got.Devices[1].Status = StatusProblem
	got2, _ := st.Get()
	if got2.Devices[1].Status == StatusProblem {
		t.Error("Get() leaked a mutable reference to the store's device slice")
	}
}

func TestStoreClear(t *testing.T) {
	st := NewStore("")
	st.Replace(Session{Devices: []Device{{UID: "a"}}})
	st.Clear()
	if _, ok := st.Get(); ok {
		t.Fatal("Clear should discard the active session")
	}
}

// TestStorePersistenceRoundTrip covers the task's explicit ask: "persist
// the walk session... a simple in-memory session plus JSON file next to the
// exe" — a session written by one Store must be recovered by a fresh Store
// opened against the same path, e.g. after a server restart.
func TestStorePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rigwalk.json")

	st1 := NewStore(path)
	st1.Replace(Session{
		Order: OrderAddress,
		Scope: Scope{Kind: "all", Label: "All devices"},
		Devices: []Device{
			{UID: "2222:00000001", Manufacturer: "Robe", Model: "Wash", Status: StatusUnvisited, PortAddress: 0, DMXStartAddress: 1},
			{UID: "2222:00000002", Manufacturer: "Robe", Model: "Wash", Status: StatusUnvisited, PortAddress: 0, DMXStartAddress: 21},
		},
		Current:     0,
		AutoAdvance: true,
	})
	if _, err := st1.Mutate(func(s *Session) error {
		s.Devices[0].Status = StatusConfirmed
		s.Devices[0].VisitedAt = time.Now()
		s.Current = 1
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	st2 := NewStore(path)
	got, ok := st2.Get()
	if !ok {
		t.Fatal("session should have been loaded from disk")
	}
	if len(got.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(got.Devices))
	}
	if got.Devices[0].Status != StatusConfirmed {
		t.Errorf("Devices[0].Status = %q, want confirmed", got.Devices[0].Status)
	}
	if got.Devices[0].VisitedAt.IsZero() {
		t.Error("VisitedAt should have survived the round trip")
	}
	if got.Current != 1 {
		t.Errorf("Current = %d, want 1", got.Current)
	}
	if got.Devices[1].UID != "2222:00000002" {
		t.Errorf("Devices[1].UID = %q", got.Devices[1].UID)
	}
}

// TestStoreClearRemovesFile ensures ending a walk doesn't leave a stale
// session file for the next server start to accidentally resurrect.
func TestStoreClearRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rigwalk.json")
	st := NewStore(path)
	st.Replace(Session{Devices: []Device{{UID: "a"}}})
	st.Clear()

	st2 := NewStore(path)
	if _, ok := st2.Get(); ok {
		t.Fatal("a cleared session should not be resurrected by a fresh Store")
	}
}
