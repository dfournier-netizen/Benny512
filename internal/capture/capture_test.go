package capture

import (
	"net/netip"
	"testing"
	"time"
)

func TestRingEvictsOldestPastCapacity(t *testing.T) {
	r := New(3)
	for i := 0; i < 5; i++ {
		r.Add(Entry{Kind: "ArtDmx", Size: 10})
	}
	got := r.Snapshot(Filter{}, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries retained, got %d", len(got))
	}
	// Sequence numbers should be the last 3 issued (3, 4, 5 since Seq starts at 1).
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Errorf("unexpected seq window: %+v", got)
	}
}

func TestRingSnapshotFilters(t *testing.T) {
	r := New(10)
	src := netip.MustParseAddrPort("10.0.0.5:6454")
	other := netip.MustParseAddrPort("10.0.0.6:6454")
	r.Add(Entry{Kind: "ArtDmx", Universe: 1, Peer: src})
	r.Add(Entry{Kind: "ArtRdm", Universe: 1, Peer: src})
	r.Add(Entry{Kind: "ArtDmx", Universe: 2, Peer: other})

	byKind := r.Snapshot(Filter{Kind: "ArtDmx"}, 0)
	if len(byKind) != 2 {
		t.Errorf("kind filter: got %d, want 2", len(byKind))
	}

	byUniv := r.Snapshot(Filter{HasUniv: true, Universe: 1}, 0)
	if len(byUniv) != 2 {
		t.Errorf("universe filter: got %d, want 2", len(byUniv))
	}

	bySrc := r.Snapshot(Filter{HasSrc: true, Source: other.Addr()}, 0)
	if len(bySrc) != 1 {
		t.Errorf("source filter: got %d, want 1", len(bySrc))
	}
}

func TestRingHexThreshold(t *testing.T) {
	r := New(10)
	small := make([]byte, 10)
	big := make([]byte, HexThreshold+1)
	e1 := r.AddPacket(DirIn, netip.AddrPort{}, small)
	e2 := r.AddPacket(DirIn, netip.AddrPort{}, big)
	if e1.HexTrunc {
		t.Error("small packet should not be truncated")
	}
	if e1.Hex == "" {
		t.Error("small packet should retain hex")
	}
	if !e2.HexTrunc {
		t.Error("big packet should be marked truncated")
	}
	if e2.Hex != "" {
		t.Error("big packet should have empty hex")
	}
	if e2.Size != len(big) {
		t.Errorf("size should still reflect true length: got %d want %d", e2.Size, len(big))
	}
}

func TestRingSubscribePublish(t *testing.T) {
	r := New(100)
	ch, cancel := r.Subscribe(Filter{Kind: "ArtDmx"})
	defer cancel()

	r.Add(Entry{Kind: "ArtDmx"})
	r.Add(Entry{Kind: "ArtPoll"}) // filtered out
	r.Add(Entry{Kind: "ArtDmx"})
	r.Publish()

	select {
	case batch := <-ch:
		if len(batch) != 2 {
			t.Fatalf("expected batch of 2 ArtDmx entries, got %d", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published batch")
	}

	// A second Publish with no new entries should not deliver anything.
	r.Publish()
	select {
	case batch := <-ch:
		t.Fatalf("unexpected extra batch: %+v", batch)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRingUnsubscribeStopsDelivery(t *testing.T) {
	r := New(10)
	ch, cancel := r.Subscribe(Filter{})
	cancel()
	r.Add(Entry{Kind: "ArtDmx"})
	r.Publish()
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after cancel")
	}
}

func TestAddPacketDecodesArtDmx(t *testing.T) {
	r := New(10)
	// Minimal valid-ish raw bytes aren't constructed here (that's artnet's
	// own job); AddPacket must simply not panic on garbage and fall back to
	// "unknown" gracefully.
	e := r.AddPacket(DirIn, netip.AddrPort{}, []byte("not art-net"))
	if e.Kind != "unknown" {
		t.Errorf("expected unknown kind for garbage input, got %q", e.Kind)
	}
}
