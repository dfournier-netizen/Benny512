package main

import (
	"benny512/internal/patch"
	"context"
	"testing"
)

func TestRehearsalCopiesPatchAndSimulatesMissing(t *testing.T) {
	original := patch.Patch{Name: "Real show", Entries: []patch.Entry{
		{ID: "a", Name: "Wash A", Universe: 16, StartAddress: 1, Footprint: 10},
		{ID: "b", Name: "Wash B", Universe: 31, StartAddress: 11, Footprint: 10},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, start, err := buildRehearsal(ctx, original, "missing")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer srv.RDM.Stop()
	defer srv.Nodes.Stop()
	defer srv.DMX.Stop()
	start()
	if !srv.Simulation {
		t.Fatal("rehearsal must be labelled")
	}
	if srv.DMX.OutputRunning() {
		t.Fatal("rehearsal started output")
	}
	if got := len(srv.Registry.Devices()); got != 1 {
		t.Fatalf("missing scenario has %d devices", got)
	}
	p, _ := srv.PatchStore.Get()
	if p.Name == original.Name || len(p.Entries) != 2 {
		t.Fatal("missing fixture removed from intended patch")
	}
	srv.PatchStore.Mutate(func(p *patch.Patch) error { p.Entries[0].Name = "Simulated edit"; return nil })
	if original.Entries[0].Name != "Wash A" {
		t.Fatal("rehearsal changed real patch")
	}
}

func TestRehearsalRejectsInvalidInput(t *testing.T) {
	if _, _, err := buildRehearsal(context.Background(), patch.Patch{}, "none"); err == nil {
		t.Fatal("empty rig accepted")
	}
	p := patch.Patch{Entries: []patch.Entry{{ID: "bad", Universe: 65535, StartAddress: 1}}}
	if _, _, err := buildRehearsal(context.Background(), p, "none"); err == nil {
		t.Fatal("invalid universe accepted")
	}
}
