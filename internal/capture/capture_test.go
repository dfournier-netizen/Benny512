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
	e1 := r.AddPacket(DirIn, netip.AddrPort{}, small)
	if e1.HexTrunc {
		t.Error("small packet should not be truncated")
	}
	if e1.Hex == "" {
		t.Error("small packet should retain hex")
	}

	// A big entry that DID decode (DecodeErr == "") is still truncated above
	// HexThreshold — this is Ring.Add's general size cap, unaffected by the
	// decode-error carve-out. Built directly (rather than via AddPacket)
	// because nothing this codebase actually decodes exceeds the threshold
	// (see HexThreshold's doc comment); Ring.Add doesn't care how an Entry
	// was produced.
	e2 := r.Add(Entry{Kind: "ArtDmx", Size: HexThreshold + 1, Hex: "aabbcc"})
	if !e2.HexTrunc {
		t.Error("big decoded packet should be marked truncated")
	}
	if e2.Hex != "" {
		t.Error("big decoded packet should have empty hex")
	}
	if e2.Size != HexThreshold+1 {
		t.Errorf("size should still reflect true length: got %d want %d", e2.Size, HexThreshold+1)
	}
}

// TestRingHexThresholdBypassedForDecodeError covers the carve-out described
// in HexThreshold's doc comment: an entry that failed to decode keeps its
// full hex (and is never HexTrunc) no matter how large the raw datagram was,
// because the raw bytes are the only diagnostic evidence such an entry
// carries — exactly the case size-based truncation would otherwise hide.
func TestRingHexThresholdBypassedForDecodeError(t *testing.T) {
	r := New(10)
	garbageOversized := make([]byte, HexThreshold+50) // all-zero, fails ID check
	e := r.AddPacket(DirIn, netip.AddrPort{}, garbageOversized)
	if e.Kind != "unknown" {
		t.Fatalf("Kind = %q, want unknown", e.Kind)
	}
	if e.DecodeErr == "" {
		t.Fatal("expected DecodeErr to be populated for undecodable input")
	}
	if e.HexTrunc {
		t.Error("undecodable oversized entry should NOT be marked truncated")
	}
	if e.Hex == "" {
		t.Error("undecodable oversized entry should retain full hex despite exceeding HexThreshold")
	}
	if len(e.Hex) != len(garbageOversized)*2 {
		t.Errorf("hex length = %d, want %d (full raw bytes)", len(e.Hex), len(garbageOversized)*2)
	}
	if e.Size != len(garbageOversized) {
		t.Errorf("Size = %d, want %d", e.Size, len(garbageOversized))
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

// TestRingClearEmptiesButKeepsCapacityAndSeqMonotonic covers the full-reset
// flow's use of Clear (task ask: "everything") — capacity/subscribers must
// survive, and Seq must not rewind (see Clear's doc comment for why: a
// rewound Seq would make an existing subscriber's high-water mark look like
// it already covers whatever gets Added next).
func TestRingClearEmptiesButKeepsCapacityAndSeqMonotonic(t *testing.T) {
	r := New(5)
	r.Add(Entry{Kind: "ArtDmx"})
	r.Add(Entry{Kind: "ArtDmx"})
	before := r.Snapshot(Filter{}, 0)
	if len(before) != 2 {
		t.Fatalf("seeded %d entries, want 2", len(before))
	}
	lastSeqBefore := before[len(before)-1].Seq

	r.Clear()

	if got := r.Snapshot(Filter{}, 0); len(got) != 0 {
		t.Fatalf("Snapshot after Clear = %+v, want empty", got)
	}
	if got := r.Capacity(); got != 5 {
		t.Fatalf("Capacity after Clear = %d, want 5 (unchanged)", got)
	}

	next := r.Add(Entry{Kind: "ArtRdm"})
	if next.Seq <= lastSeqBefore {
		t.Fatalf("Seq after Clear = %d, want > %d (monotonic, not reset to 0)", next.Seq, lastSeqBefore)
	}
	got := r.Snapshot(Filter{}, 0)
	if len(got) != 1 || got[0].Kind != "ArtRdm" {
		t.Fatalf("Snapshot after post-Clear Add = %+v", got)
	}
}

func TestRingClearOnEmptyRingIsANoOp(t *testing.T) {
	r := New(5)
	r.Clear() // must not panic
	if got := r.Snapshot(Filter{}, 0); len(got) != 0 {
		t.Fatalf("Snapshot = %+v, want empty", got)
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
