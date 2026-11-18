package gdtfshare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// standardShare: two followed manufacturers with manufacturer AND user files,
// one manufacturer nobody follows.
func standardShare(t *testing.T) *fakeShare {
	f := newFakeShare(t)
	f.add(fakeFile{RID: 101, Fixture: "Spiider", Manufacturer: "Robe", Revision: "v1", Uploader: "Manuf.", UUID: "uuid-spiider", LastModified: 1600000000})
	f.add(fakeFile{RID: 102, Fixture: "Spiider custom", Manufacturer: "Robe", Revision: "u1", Uploader: "User", UUID: "uuid-spiider-user", LastModified: 1600000000})
	f.add(fakeFile{RID: 201, Fixture: "JDC1", Manufacturer: "GLP", Revision: "r3", Uploader: "Manuf.", UUID: "uuid-jdc1", LastModified: 1610000000})
	f.add(fakeFile{RID: 901, Fixture: "Sharpy", Manufacturer: "Clay Paky", Revision: "1", Uploader: "Manuf.", UUID: "uuid-sharpy", LastModified: 1600000000})
	return f
}

func gdtfFilesOnDisk(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, "files"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, d.Name())
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func indexRIDs(t *testing.T, s *Service) []int64 {
	t.Helper()
	res, err := s.Search("", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, r := range res {
		out = append(out, r.RID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestFirstSyncDownloadsFollowedManufacturerAndUserFiles(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	res := syncOK(t, s)
	if res.State != StateOK || res.Downloaded != 3 {
		t.Fatalf("first sync = %+v, want state ok with 3 downloads", res)
	}
	got := gdtfFilesOnDisk(t, s.cfg.Root)
	want := []string{"101.gdtf", "102.gdtf", "201.gdtf"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("files on disk = %v, want %v (Clay Paky is not followed)", got, want)
	}
	// Every indexed file carries its label and the SHA-256 of the bytes on disk.
	res2, _ := s.Search("", false, 0)
	labels := map[int64]string{}
	for _, r := range res2 {
		labels[r.RID] = r.UploaderLabel
		data, err := os.ReadFile(filepath.Join(s.cfg.Root, "files", fmt.Sprintf("%d.gdtf", r.RID)))
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		var served []byte
		for _, ff := range f.files {
			if ff.RID == r.RID {
				served = ff.Data
			}
		}
		f.mu.Unlock()
		if !bytes.Equal(data, served) {
			t.Fatalf("rid %d: bytes on disk differ from what GDTF Share served", r.RID)
		}
		fh, fe, err := s.OpenFile(r.RID)
		if err != nil {
			t.Fatal(err)
		}
		fh.Close()
		if fe.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("rid %d: index SHA-256 %q does not match the file", r.RID, fe.SHA256)
		}
	}
	if labels[101] != LabelManufacturer || labels[102] != LabelUser || labels[201] != LabelManufacturer {
		t.Fatalf("labels = %v", labels)
	}
	st := s.Status()
	if st.State != StateOK || st.Files != 3 || st.LastSync == nil {
		t.Fatalf("status = %+v", st)
	}
}

func TestSecondSyncDownloadsNothing(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	before := f.count(func(f *fakeShare) int { return len(f.downloads) })
	res := syncOK(t, s)
	after := f.count(func(f *fakeShare) int { return len(f.downloads) })
	if after != before || res.Downloaded != 0 || res.Unchanged != 3 {
		t.Fatalf("second sync made %d downloads (result %+v), want none and 3 unchanged", after-before, res)
	}
}

func TestNewerRevisionReplacesOld(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	f.add(fakeFile{RID: 202, Fixture: "JDC1", Manufacturer: "GLP", Revision: "r4", Uploader: "Manuf.", UUID: "uuid-jdc1", LastModified: 1620000000})
	res := syncOK(t, s)
	if res.Downloaded != 1 || res.Replaced != 1 {
		t.Fatalf("result = %+v, want one download replacing one file", res)
	}
	got := gdtfFilesOnDisk(t, s.cfg.Root)
	if fmt.Sprint(got) != "[101.gdtf 102.gdtf 202.gdtf]" {
		t.Fatalf("files on disk = %v, want 201 replaced by 202", got)
	}
	if fmt.Sprint(indexRIDs(t, s)) != "[101 102 202]" {
		t.Fatalf("index rids = %v", indexRIDs(t, s))
	}
	// An OLDER revision appearing later never replaces the newer one.
	f.add(fakeFile{RID: 203, Fixture: "JDC1", Manufacturer: "GLP", Revision: "r2", Uploader: "Manuf.", UUID: "uuid-jdc1", LastModified: 1500000000})
	syncOK(t, s)
	if fmt.Sprint(indexRIDs(t, s)) != "[101 102 202]" {
		t.Fatalf("after an older revision appeared, index rids = %v", indexRIDs(t, s))
	}
}

func TestCookieExpiryMidSyncReloginsOnceAndContinues(t *testing.T) {
	f := standardShare(t)
	f.set(func(f *fakeShare) { f.expireAfter = 1 })
	s, _ := newTestService(t, f, nil)
	res := syncOK(t, s)
	logins := f.count(func(f *fakeShare) int { return f.logins })
	if logins != 2 || res.Relogins != 1 {
		t.Fatalf("logins = %d (relogins %d), want exactly one re-login", logins, res.Relogins)
	}
	if res.Downloaded != 3 || res.State != StateOK {
		t.Fatalf("result = %+v, want all three downloaded after the re-login", res)
	}
}

func TestAuthLostAgainAfterReloginStopsAndReports(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	// login works, but every list/download says 401.
	f.set(func(f *fakeShare) { f.alwaysExpired = true })
	res, err := s.Sync(context.Background())
	if err == nil || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Sync err = %v, want ErrUnauthorized", err)
	}
	if res.State != StateStopped {
		t.Fatalf("state = %q, want stopped", res.State)
	}
	if n := f.count(func(f *fakeShare) int { return f.logins }); n != 2 {
		t.Fatalf("logins = %d, want 2 (the first, and one re-login)", n)
	}
}

func TestDownloadErrorStaysPendingRestCompleteNextSyncRetries(t *testing.T) {
	f := standardShare(t)
	f.set(func(f *fakeShare) { f.failRID[102] = 500 })
	s, _ := newTestService(t, f, nil)
	res := syncOK(t, s)
	if res.State != StatePartial || res.Downloaded != 2 || res.Failed != 1 {
		t.Fatalf("result = %+v, want partial with 2 downloaded and 1 failed", res)
	}
	st := s.Status()
	if len(st.Pending) != 1 || st.Pending[0].RID != 102 || st.Pending[0].Attempts != 1 || st.Pending[0].Error == "" {
		t.Fatalf("pending = %+v", st.Pending)
	}
	if fmt.Sprint(gdtfFilesOnDisk(t, s.cfg.Root)) != "[101.gdtf 201.gdtf]" {
		t.Fatalf("files = %v", gdtfFilesOnDisk(t, s.cfg.Root))
	}
	f.mu.Lock()
	delete(f.failRID, 102)
	n0 := len(f.downloads)
	f.mu.Unlock()
	res = syncOK(t, s)
	f.mu.Lock()
	retried := append([]int64(nil), f.downloads[n0:]...)
	f.mu.Unlock()
	if fmt.Sprint(retried) != "[102]" || res.State != StateOK {
		t.Fatalf("retry downloaded %v (result %+v), want only 102", retried, res)
	}
	if len(s.Status().Pending) != 0 {
		t.Fatalf("pending after retry = %+v", s.Status().Pending)
	}
}

func TestKilledMidFileLeavesNoFileAndResumeCompletes(t *testing.T) {
	f := standardShare(t)
	halfSent := make(chan struct{})
	f.set(func(f *fakeShare) { f.halfRID, f.halfSent = 102, halfSent })
	s, _ := newTestService(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-halfSent
		cancel()
	}()
	_, err := s.Sync(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync err = %v, want context.Canceled", err)
	}
	for _, name := range gdtfFilesOnDisk(t, s.cfg.Root) {
		if name == "102.gdtf" {
			t.Fatalf("a half-downloaded file is under its real name: %v", gdtfFilesOnDisk(t, s.cfg.Root))
		}
	}
	// A real kill (power cut) leaves the partial file behind; plant one and
	// check the next sync clears it.
	stale := filepath.Join(s.cfg.Root, "files", "102.gdtf.part-stale")
	if err := os.WriteFile(stale, []byte("PK\x03\x04half"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.halfRID = 0
	f.mu.Unlock()
	res := syncOK(t, s)
	if res.State != StateOK {
		t.Fatalf("resume = %+v", res)
	}
	got := gdtfFilesOnDisk(t, s.cfg.Root)
	if fmt.Sprint(got) != "[101.gdtf 102.gdtf 201.gdtf]" {
		t.Fatalf("after resume, files = %v", got)
	}
}

func TestOfflineLeavesIndexUntouchedAndSaysOffline(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	idxPath := filepath.Join(s.cfg.Root, "index.json")
	before, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	lastSync := s.Status().LastSync
	f.srv.Close()
	res, err := s.Sync(context.Background())
	var off *OfflineError
	if !errors.As(err, &off) {
		t.Fatalf("Sync err = %v, want OfflineError", err)
	}
	after, _ := os.ReadFile(idxPath)
	if !bytes.Equal(before, after) {
		t.Fatal("index.json changed during an offline sync")
	}
	st := s.Status()
	if res.State != StateOffline || st.State != StateOffline || !strings.Contains(strings.ToLower(st.Message), "offline") {
		t.Fatalf("status = %q %q, want offline", st.State, st.Message)
	}
	if st.LastSync == nil || lastSync == nil || !st.LastSync.Equal(*lastSync) {
		t.Fatalf("last sync = %v, want the earlier %v kept", st.LastSync, lastSync)
	}
	if st.Files != 3 {
		t.Fatalf("files = %d, want the 3 still there", st.Files)
	}
}

func TestFollowedNameMatchingNothingIsReported(t *testing.T) {
	f := standardShare(t)
	f.add(fakeFile{RID: 301, Fixture: "MAC Aura", Manufacturer: "Martin Professional", Revision: "1", Uploader: "Manuf.", UUID: "uuid-aura", LastModified: 1600000000})
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	var martin *ManufacturerResult
	st := s.Status()
	for i := range st.Manufacturers {
		if st.Manufacturers[i].Name == "Martin" {
			martin = &st.Manufacturers[i]
		}
	}
	if martin == nil {
		t.Fatalf("Martin missing from the report: %+v", st.Manufacturers)
	}
	if martin.Matched || !strings.Contains(martin.Result, "Martin") || fmt.Sprint(martin.Similar) != "[Martin Professional]" {
		t.Fatalf("Martin = %+v, want unmatched, named in the result, Martin Professional offered as similar", *martin)
	}
	// Not followed by alias: the Aura was not downloaded.
	for _, name := range gdtfFilesOnDisk(t, s.cfg.Root) {
		if name == "301.gdtf" {
			t.Fatal("an alias was guessed: Martin Professional's file was downloaded for Martin")
		}
	}
	// Case-insensitive exact match: "glp" (lower case) still matches GLP.
	if _, err := s.SetFollowed([]string{"glp", "Martin"}); err != nil {
		t.Fatal(err)
	}
	syncOK(t, s)
	for _, m := range s.Status().Manufacturers {
		if m.Name == "glp" && (!m.Matched || m.ShareName != "GLP") {
			t.Fatalf("glp = %+v, want matched to GLP", m)
		}
	}
}

func TestLightingAdapterIsRefused(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, func(c *Config) {
		c.LightingNetworks = func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")} }
	})
	res, err := s.Sync(context.Background())
	if !errors.Is(err, ErrLightingRoute) {
		t.Fatalf("Sync err = %v, want ErrLightingRoute", err)
	}
	if res.State != StateRefused || s.Status().Message != "the only route to the internet is the lighting network adapter" {
		t.Fatalf("state %q message %q", res.State, s.Status().Message)
	}
	if n := f.count(func(f *fakeShare) int { return f.conns + f.logins }); n != 0 {
		t.Fatalf("the fake GDTF Share saw %d connections/logins over the lighting network", n)
	}
	// A lighting subnet that does not hold the route is no obstacle.
	s2, _ := newTestService(t, f, func(c *Config) {
		c.LightingNetworks = func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("2.0.0.0/8")} }
	})
	syncOK(t, s2)
}

func TestOnlyOneSyncAtATime(t *testing.T) {
	f := standardShare(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	f.set(func(f *fakeShare) { f.blockRID, f.blocked, f.releaseCh = 101, blocked, release })
	s, _ := newTestService(t, f, nil)
	done := make(chan error, 1)
	go func() {
		_, err := s.Sync(context.Background())
		done <- err
	}()
	<-blocked
	if _, err := s.Sync(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Sync err = %v, want ErrBusy", err)
	}
	if _, err := s.Pull(context.Background(), 901); !errors.Is(err, ErrBusy) {
		t.Fatalf("Pull during a sync err = %v, want ErrBusy", err)
	}
	if !s.Status().Running {
		t.Fatal("status does not say running")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDownloadsAreSequentialAndSpaced(t *testing.T) {
	f := standardShare(t)
	for i := int64(0); i < 4; i++ {
		f.add(fakeFile{RID: 400 + i, Fixture: fmt.Sprintf("Fixture %d", i), Manufacturer: "Ayrton", Revision: "1", Uploader: "Manuf.", UUID: fmt.Sprintf("uuid-ay-%d", i), LastModified: 1600000000})
	}
	const gap = 30 * time.Millisecond
	s, _ := newTestService(t, f, func(c *Config) { c.DownloadDelay = gap })
	syncOK(t, s)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxInflight != 1 {
		t.Fatalf("max downloads in flight = %d, want 1", f.maxInflight)
	}
	if len(f.downloadAt) != 7 {
		t.Fatalf("downloads = %d, want 7", len(f.downloadAt))
	}
	for i := 1; i < len(f.downloadAt); i++ {
		if d := f.downloadAt[i].Sub(f.downloadAt[i-1]); d < gap {
			t.Fatalf("downloads %d and %d only %v apart, want at least %v", i-1, i, d, gap)
		}
	}
}

func TestPullUnfollowedManufacturer(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	e, err := s.Pull(context.Background(), 901)
	if err != nil {
		t.Fatal(err)
	}
	if e.Manufacturer != "Clay Paky" || e.UploaderLabel != LabelManufacturer || e.SHA256 == "" {
		t.Fatalf("pulled = %+v", e)
	}
	if fmt.Sprint(indexRIDs(t, s)) != "[101 102 201 901]" {
		t.Fatalf("index = %v", indexRIDs(t, s))
	}
	// Still there after the next sync: unfollowed files are not removed.
	syncOK(t, s)
	if fmt.Sprint(indexRIDs(t, s)) != "[101 102 201 901]" {
		t.Fatalf("after sync, index = %v", indexRIDs(t, s))
	}
}

func TestSearchIsCaseInsensitiveAndLabelled(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	syncOK(t, s)
	got, err := s.Search("SPIIDER", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("library search = %+v, want both Spiider files", got)
	}
	for _, r := range got {
		if r.UploaderLabel == "" || !r.InLibrary {
			t.Fatalf("hit without label or library flag: %+v", r)
		}
	}
	// The cached GDTF Share list covers unfollowed manufacturers too.
	share, _ := s.Search("sharpy", true, 0)
	if len(share) != 1 || share[0].RID != 901 || share[0].InLibrary || share[0].UploaderLabel != LabelManufacturer {
		t.Fatalf("share search = %+v", share)
	}
	// Words match across manufacturer and fixture name.
	if both, _ := s.Search("robe custom", false, 0); len(both) != 1 || both[0].RID != 102 {
		t.Fatalf("two-word search = %+v", both)
	}
}

func TestFollowedDefaultsAddRemove(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	if fmt.Sprint(s.Followed()) != fmt.Sprint(DefaultManufacturers) {
		t.Fatalf("defaults = %v", s.Followed())
	}
	got, err := s.SetFollowed([]string{" Robe ", "robe", "", "Clay Paky"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[Robe Clay Paky]" {
		t.Fatalf("SetFollowed = %v, want trimmed and de-duplicated", got)
	}
	// Survives a restart (a new Service over the same root).
	s2 := New(s.cfg)
	if fmt.Sprint(s2.Followed()) != "[Robe Clay Paky]" {
		t.Fatalf("after restart = %v", s2.Followed())
	}
	// Removing everything is allowed and is not silently reset to defaults.
	if got, _ := s2.SetFollowed(nil); len(got) != 0 {
		t.Fatalf("empty list = %v", got)
	}
	if got := New(s.cfg).Followed(); len(got) != 0 {
		t.Fatalf("empty list after restart = %v", got)
	}
}

func TestBadLoginReported(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, func(c *Config) {
		c.Credentials = func() (string, string, bool) { return "dom", "wrong", true }
	})
	res, err := s.Sync(context.Background())
	if !errors.Is(err, ErrBadLogin) || res.State != StateLoginFailed {
		t.Fatalf("Sync = %+v, %v; want login-failed", res, err)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Root, "index.json")); !os.IsNotExist(err) {
		t.Fatalf("index.json written after a refused login: %v", err)
	}
}

func TestPasswordNeverInLogsOrLibraryFiles(t *testing.T) {
	f := standardShare(t)
	f.set(func(f *fakeShare) { f.failRID[201] = 500 })
	s, logBuf := newTestService(t, f, nil)
	syncOK(t, s)
	f.srv.Close()
	_, _ = s.Sync(context.Background())
	if strings.Contains(logBuf.String(), f.pass) {
		t.Fatal("the password appears in the log output")
	}
	if logBuf.Len() == 0 {
		t.Fatal("nothing was logged: the grep proves nothing")
	}
	_ = filepath.WalkDir(s.cfg.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		if bytes.Contains(data, []byte(f.pass)) {
			t.Errorf("the password is in %s", p)
		}
		return nil
	})
	st := fmt.Sprintf("%+v", s.Status())
	if strings.Contains(st, f.pass) {
		t.Fatal("the password is in the status")
	}
}

func TestDamagedIndexIsLeftAloneAndRefused(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	p := filepath.Join(s.cfg.Root, "index.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Sync(context.Background())
	if err == nil || res.State != StateError {
		t.Fatalf("Sync over a damaged index = %+v, %v; want an error", res, err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "{not json" {
		t.Fatal("the damaged index was overwritten")
	}
}

// A pull that has to fetch the list records the list fetch but is not a
// sync attempt: the last attempt and result stay as they were.
func TestPullBeforeAnySyncIsNotASyncAttempt(t *testing.T) {
	f := standardShare(t)
	s, _ := newTestService(t, f, nil)
	if _, err := s.Pull(context.Background(), 901); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.LastAttempt != nil || st.State != StateNever || st.LastSync == nil {
		t.Fatalf("after a pull: lastAttempt %v state %q lastSync %v; want no attempt, never, and the list fetch recorded", st.LastAttempt, st.State, st.LastSync)
	}
}
