package gdtfshare

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// g2Share: a followed manufacturer with a file carrying full RDM data, one
// with no RDM block, and a zip with no description.xml (the client already
// refuses a download that is not a zip at all); plus an
// unfollowed manufacturer with RDM data for the single pull.
func g2Share(t *testing.T) *fakeShare {
	f := newFakeShare(t)
	f.add(fakeFile{RID: 301, Fixture: "Test Spot", Manufacturer: "Robe", Revision: "1", Uploader: "Manuf.", UUID: "u-spot", LastModified: 1700000000,
		Data: zipOf(t, map[string]string{"description.xml": fullDescription})})
	f.add(fakeFile{RID: 302, Fixture: "Plain Wash", Manufacturer: "Robe", Revision: "1", Uploader: "User", UUID: "u-wash", LastModified: 1700000000,
		Data: zipOf(t, map[string]string{"description.xml": `<GDTF DataVersion="1.1"><FixtureType Name="Plain Wash" LongName="Robe Plain Wash XL"/></GDTF>`})})
	f.add(fakeFile{RID: 303, Fixture: "Broken", Manufacturer: "Robe", Revision: "1", Uploader: "Manuf.", UUID: "u-broken", LastModified: 1700000000,
		Data: zipOf(t, map[string]string{"thumbnail.png": "x"})})
	f.add(fakeFile{RID: 901, Fixture: "Other", Manufacturer: "Clay Paky", Revision: "1", Uploader: "Manuf.", UUID: "u-other", LastModified: 1700000000,
		Data: zipOf(t, map[string]string{"description.xml": fullDescription})})
	return f
}

func indexByRID(t *testing.T, s *Service) map[int64]IndexEntry {
	t.Helper()
	m, err := s.indexMap()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSyncReadsEachFileAtDownloadAndAnUnreadableOneStays(t *testing.T) {
	f := g2Share(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	res := syncOK(t, s)
	if res.State != StateOK || res.Downloaded != 3 {
		t.Fatalf("an unreadable description must not block the sync: %+v", res)
	}
	idx := indexByRID(t, s)
	spot := idx[301].Description
	if spot == nil || spot.State != DescRead || spot.LongName != "Acme Test Spot 700" || spot.RDM == nil ||
		spot.RDM.DeviceModelID == nil || *spot.RDM.DeviceModelID != 0x1093 || spot.ReadAt.IsZero() {
		t.Fatalf("rid 301 description = %+v", spot)
	}
	if w := idx[302].Description; w == nil || w.State != DescRead || w.RDM != nil {
		t.Fatalf("rid 302 (no FTRDM) = %+v", w)
	}
	b, ok := idx[303]
	if !ok {
		t.Fatal("the unreadable file was dropped from the index")
	}
	if b.Description == nil || b.Description.State != DescUnreadable || !strings.Contains(b.Description.Reason, "no description.xml") {
		t.Fatalf("rid 303 = %+v", b.Description)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "files", "303.gdtf")); err != nil {
		t.Fatalf("the unreadable file must stay on disk: %v", err)
	}
	st := s.Status()
	if st.Unreadable != 1 || st.NotYetRead != 0 {
		t.Fatalf("status counts unreadable %d not-yet-read %d", st.Unreadable, st.NotYetRead)
	}
	// A single pull reads its file too.
	ie, err := s.Pull(context.Background(), 901)
	if err != nil {
		t.Fatal(err)
	}
	if ie.Description == nil || ie.Description.State != DescRead || ie.Description.RDM == nil {
		t.Fatalf("pulled file description = %+v", ie.Description)
	}
	if d := indexByRID(t, s)[901].Description; d == nil || d.State != DescRead {
		t.Fatalf("pulled file in index = %+v", d)
	}
}

// stripDescriptions rewrites index.json as G1 wrote it: no description.
func stripDescriptions(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, "index.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, f := range doc["files"].([]any) {
		delete(f.(map[string]any), "description")
	}
	out, _ := json.Marshal(doc)
	if strings.Contains(string(out), "description") {
		t.Fatal("strip failed")
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillReadsPreG2EntriesOnce(t *testing.T) {
	f := g2Share(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, s)
	root := s.Root()
	stripDescriptions(t, root)

	s2 := New(Config{Root: root})
	if st := s2.Status(); st.NotYetRead != 3 {
		t.Fatalf("a G1 index has 3 files not yet read, status says %d", st.NotYetRead)
	}
	n, err := s2.Backfill()
	if err != nil || n != 3 {
		t.Fatalf("first backfill read %d (%v), want 3", n, err)
	}
	idx := indexByRID(t, s2)
	if d := idx[301].Description; d == nil || d.State != DescRead || d.RDM == nil || d.LongName != "Acme Test Spot 700" {
		t.Fatalf("backfilled 301 = %+v", d)
	}
	if d := idx[303].Description; d == nil || d.State != DescUnreadable {
		t.Fatalf("backfilled 303 = %+v", d)
	}
	if len(idx) != 3 {
		t.Fatalf("backfill changed the file set: %d entries", len(idx))
	}
	if n, err := s2.Backfill(); err != nil || n != 0 {
		t.Fatalf("second backfill read %d (%v), want 0", n, err)
	}
	// It was saved: a fresh service on the same folder reads nothing again.
	s3 := New(Config{Root: root})
	if n, err := s3.Backfill(); err != nil || n != 0 {
		t.Fatalf("backfill after restart read %d (%v), want 0", n, err)
	}
	if st := s3.Status(); st.NotYetRead != 0 || st.Unreadable != 1 {
		t.Fatalf("status after backfill: not yet read %d, unreadable %d", st.NotYetRead, st.Unreadable)
	}
}

func TestRunBackfillsAtStartUp(t *testing.T) {
	f := g2Share(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, s)
	root := s.Root()
	stripDescriptions(t, root)
	// No login: Run syncs nothing, but still reads the library's files.
	s2 := New(Config{Root: root, Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s2.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	allRead := func() bool {
		m, err := s2.indexMap()
		if err != nil || len(m) != 3 {
			return false
		}
		for _, e := range m {
			if e.Description == nil {
				return false
			}
		}
		return true
	}
	for !allRead() {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run did not read the library's files at start-up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestSearchCoversLongName(t *testing.T) {
	f := g2Share(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.SetFollowed([]string{"Robe"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, s)
	// "700" is only in rid 301's LongName; getList's name is "Test Spot".
	got, err := s.Search("acme 700", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RID != 301 || got[0].UploaderLabel != LabelManufacturer {
		t.Fatalf("library LongName search = %+v", got)
	}
	if got[0].Description == nil || got[0].Description.LongName != "Acme Test Spot 700" {
		t.Fatalf("hit carries its description: %+v", got[0].Description)
	}
	if got, _ := s.Search("plain wash xl", false, 0); len(got) != 1 || got[0].RID != 302 || got[0].UploaderLabel != LabelUser {
		t.Fatalf("user file LongName search = %+v", got)
	}
	// The GDTF Share list search uses a downloaded file's LongName too.
	if got, _ := s.Search("acme 700", true, 0); len(got) != 1 || got[0].RID != 301 || !got[0].InLibrary {
		t.Fatalf("share LongName search = %+v", got)
	}
}
