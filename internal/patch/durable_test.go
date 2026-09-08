package patch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveFailureDoesNotPublishMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	st := NewStore(path)
	if _, err := st.ReplaceChecked(Patch{Name: "Original", Entries: []Entry{{ID: "a", ChannelFunctions: map[uint16]ChannelFunction{1: {Attribute: "Dimmer"}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".bak", 0755); err != nil {
		t.Fatal(err)
	}
	_, err := st.Mutate(func(p *Patch) error {
		p.Name = "Lost"
		p.Entries[0].ChannelFunctions[1] = ChannelFunction{Attribute: "Pan"}
		return nil
	})
	if err == nil {
		t.Fatal("failed backup reported success")
	}
	p, _ := st.Get()
	if p.Name != "Original" || p.Entries[0].ChannelFunctions[1].Attribute != "Dimmer" {
		t.Fatal("failed edit leaked into memory")
	}
	reopened, _ := NewStore(path).Get()
	if reopened.Name != "Original" {
		t.Fatal("failed edit reached disk")
	}
}

func TestMutationCallbackFailureRollsBackNestedData(t *testing.T) {
	st := NewStore("")
	st.Replace(Patch{Entries: []Entry{{ID: "a", ChannelFunctions: map[uint16]ChannelFunction{1: {Attribute: "Dimmer"}}}}})
	st.Mutate(func(p *Patch) error {
		p.Entries[0].ChannelFunctions[1] = ChannelFunction{Attribute: "Pan"}
		return errors.New("reject")
	})
	p, _ := st.Get()
	if p.Entries[0].ChannelFunctions[1].Attribute != "Dimmer" {
		t.Fatal("callback error leaked nested mutation")
	}
}

func TestResetActivePreservesOtherShowsAndRecovery(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "show.json"))
	st.Replace(Patch{Name: "A", Entries: []Entry{{ID: "a"}}})
	ref, _, err := st.CreatePatch("B")
	if err != nil {
		t.Fatal(err)
	}
	st.Mutate(func(p *Patch) error { p.Entries = []Entry{{ID: "b"}}; return nil })
	if _, err = st.ResetActive(); err != nil {
		t.Fatal(err)
	}
	p, err := st.RecoverActive()
	if err != nil || len(p.Entries) != 1 {
		t.Fatalf("recover: %+v %v", p, err)
	}
	if len(st.ListPatches()) != 2 {
		t.Fatal("reset lost another show")
	}
	p, err = st.LoadPatch("default")
	if err != nil || p.Entries[0].ID != "a" {
		t.Fatal("A changed")
	}
	if _, err = st.LoadPatch(ref.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFailedShowSwitchKeepsOriginalActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	st := NewStore(path)
	st.Replace(Patch{Name: "A"})
	ref, _, err := st.CreatePatch("B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.LoadPatch("default"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path + ".patches.active"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path+".patches.active", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = st.LoadPatch(ref.ID); err == nil {
		t.Fatal("switch reported success")
	}
	p, _ := st.Get()
	if p.Name != "A" {
		t.Fatal("failed switch changed show")
	}
}

func TestDamagedSelectedShowDoesNotFallBackAndCanRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	st := NewStore(path)
	st.Replace(Patch{Name: "A"})
	ref, _, err := st.CreatePatch("B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Mutate(func(p *Patch) error { p.Entries = []Entry{{ID: "b"}}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(st.catalogPath(ref.ID), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(path)
	if p, ok := restarted.Get(); ok {
		t.Fatalf("silently fell back to %+v", p)
	}
	p, err := restarted.RecoverActive()
	if err != nil || p.Name != "B" {
		t.Fatalf("wrong recovery target: %+v %v", p, err)
	}
}
