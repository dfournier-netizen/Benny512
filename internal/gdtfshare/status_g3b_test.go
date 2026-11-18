package gdtfshare

// G3b1: the status data the approved G3a mockup marks NEW
// (docs/design/gdtf-library/README.md, "What the API gives, and what is
// NEW"): the manufacturer being synced, the unreadable files themselves,
// the route the last conversation with GDTF Share left by, and each file's
// SHA-256 on search and match results so the web layer can say whether it is
// already in the Fixture Library.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
)

// TestProgressNamesTheManufacturer: while a file is downloading, progress
// says which GDTF Share manufacturer it belongs to and its place among the
// manufacturers this sync works through ("now Robe (manufacturer 2 of 2)").
func TestProgressNamesTheManufacturer(t *testing.T) {
	f := standardShare(t) // followed by default: GLP (201), Robe (101, 102)
	blocked, release := make(chan struct{}), make(chan struct{})
	f.set(func(f *fakeShare) { f.blockRID, f.blocked, f.releaseCh = 101, blocked, release })
	s, _ := newTestService(t, f, nil)
	done := make(chan error, 1)
	go func() {
		_, err := s.Sync(context.Background())
		done <- err
	}()
	<-blocked
	p := s.Status().Progress
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p.Done != 1 || p.Total != 3 || p.Manufacturer != "Robe" || p.ManufacturerIndex != 2 || p.ManufacturerCount != 2 {
		t.Fatalf("progress during rid 101 = %+v, want 1 of 3 files, Robe, manufacturer 2 of 2", p)
	}
	if p := s.Status().Progress; p.Manufacturer != "" || p.ManufacturerIndex != 0 {
		t.Fatalf("progress after the sync still names a manufacturer: %+v", p)
	}
}

// TestStatusListsUnreadableFiles: the status names each unreadable file and
// its reason, not just the count (mockup scene 7).
func TestStatusListsUnreadableFiles(t *testing.T) {
	f := g2Share(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, s)
	st := s.Status()
	if st.Unreadable != 1 || len(st.UnreadableFiles) != 1 {
		t.Fatalf("unreadable %d, files %+v", st.Unreadable, st.UnreadableFiles)
	}
	u := st.UnreadableFiles[0]
	if u.RID != 303 || u.Fixture != "Broken" || u.Manufacturer != "Robe" || u.Revision != "1" ||
		u.UploaderLabel != LabelManufacturer || u.Reason == "" {
		t.Fatalf("unreadable file = %+v", u)
	}
	// None unreadable: an empty list, never null.
	s2 := New(Config{Root: t.TempDir()})
	if got := s2.Status().UnreadableFiles; got == nil || len(got) != 0 {
		t.Fatalf("empty library UnreadableFiles = %#v, want []", got)
	}
}

// TestStatusReportsTheInternetRoute: after a conversation with GDTF Share,
// the status gives the local address the connection actually left from (the
// TCP connection's own local address, the dialer's authoritative check);
// before any, it is null, never a guessed adapter.
func TestStatusReportsTheInternetRoute(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	if r := s.Status().InternetRoute; r != nil {
		t.Fatalf("route before any connection = %+v, want null", r)
	}
	syncOK(t, s)
	r := s.Status().InternetRoute
	if r == nil || r.LocalAddress != "127.0.0.1" || r.At.IsZero() {
		t.Fatalf("route after a sync = %+v, want 127.0.0.1 (the fake listens on loopback)", r)
	}
}

// TestSearchAndMatchCarrySHA256: a file on this computer carries its SHA-256
// on search and match results (so it can be compared with the Fixture
// Library's stored source files); a GDTF Share entry not downloaded has none.
func TestSearchAndMatchCarrySHA256(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	want := map[int64]string{}
	f.mu.Lock()
	for _, ff := range f.files {
		want[ff.RID] = fmt.Sprintf("%x", sha256.Sum256(ff.Data))
	}
	f.mu.Unlock()
	lib, err := s.Search("", false, 0)
	if err != nil || len(lib) == 0 {
		t.Fatalf("library search %v %v", lib, err)
	}
	for _, r := range lib {
		if r.SHA256 != want[r.RID] {
			t.Fatalf("rid %d sha256 = %q, want %q", r.RID, r.SHA256, want[r.RID])
		}
	}
	share, _ := s.Search("sharpy", true, 0)
	if len(share) != 1 || share[0].InLibrary || share[0].SHA256 != "" {
		t.Fatalf("share entry not downloaded = %+v, want no sha256", share)
	}

	g := g2Share(t) // rid 301 and 901 carry FTRDM 0x22A6 / 0x1093
	m, _ := newTestService(t, g, nil)
	if _, err := m.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, m)
	idx := indexByRID(t, m)
	res, err := m.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093})
	if err != nil || len(res.Candidates) != 1 {
		t.Fatalf("match %+v %v", res, err)
	}
	for _, c := range res.Candidates {
		if c.SHA256 == "" || c.SHA256 != idx[c.RID].SHA256 {
			t.Fatalf("candidate rid %d sha256 %q, index %q", c.RID, c.SHA256, idx[c.RID].SHA256)
		}
	}
}
