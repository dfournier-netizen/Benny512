package gdtfshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Config is everything a Service needs. Root "" disables the service
// (tests of other packages, --demo): it then reports ErrDisabled and touches
// no disk and no network.
type Config struct {
	Root    string
	BaseURL string // DefaultBaseURL when empty
	// Credentials returns the saved login. ok false means there is nothing
	// complete to log in with. Called at the start of each conversation with
	// GDTF Share, so a login changed in the meantime is picked up.
	Credentials func() (user, password string, ok bool)
	// LightingNetworks returns the subnets of the adapters selected for
	// Art-Net and sACN output. No request may leave through them.
	LightingNetworks func() []netip.Prefix
	// DownloadDelay spaces downloads (default 1 s). Downloads are always
	// sequential; this is the pause between one finishing and the next.
	DownloadDelay time.Duration
	// Interval between scheduled syncs (default 120 min, the API page's
	// recommendation).
	Interval       time.Duration
	RequestTimeout time.Duration // per request (default 10 min)
	MaxFileBytes   int64         // per file (default 64 MB)
	Logf           func(format string, args ...any)
	Now            func() time.Time
}

const (
	defaultDelay    = time.Second
	defaultInterval = 120 * time.Minute
	defaultTimeout  = 10 * time.Minute
	defaultMaxFile  = 64 << 20
	indexSaveEvery  = 20
	maxFollowed     = 500
	maxNameLen      = 100
)

// Service owns one GDTF library folder.
type Service struct {
	cfg Config
	st  store

	// netMu is held for the whole of any conversation with GDTF Share — a
	// sync, a single pull, a login test — so there is never more than one,
	// and so downloads are strictly one after another. TryLock: a second
	// caller is told ErrBusy rather than queued behind an hour-long import.
	netMu sync.Mutex

	mu        sync.Mutex // guards everything below
	running   bool
	progress  Progress
	idx       *indexDoc
	sd        *syncDoc
	list      *listDoc
	runActive bool
	trigger   chan struct{}
}

// New builds a Service. It reads nothing until first used.
func New(cfg Config) *Service {
	if cfg.DownloadDelay <= 0 {
		cfg.DownloadDelay = defaultDelay
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultTimeout
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = defaultMaxFile
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Credentials == nil {
		cfg.Credentials = func() (string, string, bool) { return "", "", false }
	}
	return &Service{cfg: cfg, st: store{root: cfg.Root}, trigger: make(chan struct{}, 1)}
}

// Enabled reports whether this service has a library folder.
func (s *Service) Enabled() bool { return s.cfg.Root != "" }

// Root is the library folder ("" when disabled).
func (s *Service) Root() string { return s.cfg.Root }

func (s *Service) now() time.Time { return s.cfg.Now().UTC().Truncate(time.Second) }

// --- cached documents (s.mu held) ---------------------------------------

func (s *Service) indexLocked() (*indexDoc, error) {
	if s.idx == nil {
		doc, err := s.st.loadIndex()
		if err != nil {
			return nil, err
		}
		s.idx = &doc
	}
	return s.idx, nil
}

func (s *Service) syncLocked() (*syncDoc, error) {
	if s.sd == nil {
		doc, err := s.st.loadSync()
		if err != nil {
			return nil, err
		}
		s.sd = &doc
	}
	return s.sd, nil
}

func (s *Service) listLocked() (*listDoc, error) {
	if s.list == nil {
		doc, err := s.st.loadList()
		if err != nil {
			return nil, err
		}
		s.list = &doc
	}
	return s.list, nil
}

func (s *Service) setRunning(on bool) {
	s.mu.Lock()
	s.running = on
	s.progress = Progress{}
	s.mu.Unlock()
}

func (s *Service) setProgress(done, total int) {
	s.mu.Lock()
	s.progress = Progress{Done: done, Total: total}
	s.mu.Unlock()
}

// saveIndex writes files (by rid) as index.json and publishes it.
func (s *Service) saveIndex(files map[int64]IndexEntry) error {
	doc := indexDoc{Format: indexFormat, SchemaVersion: schemaV1, Files: make([]IndexEntry, 0, len(files))}
	for _, e := range files {
		doc.Files = append(doc.Files, e)
	}
	sort.Slice(doc.Files, func(i, j int) bool { return doc.Files[i].RID < doc.Files[j].RID })
	if err := s.st.saveJSON("index.json", doc); err != nil {
		return err
	}
	s.mu.Lock()
	s.idx = &doc
	s.mu.Unlock()
	return nil
}

func (s *Service) indexMap() (map[int64]IndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.indexLocked()
	if err != nil {
		return nil, err
	}
	m := make(map[int64]IndexEntry, len(idx.Files))
	for _, e := range idx.Files {
		m[e.RID] = e
	}
	return m, nil
}

// --- identity and ordering ------------------------------------------------

// fixtureKey is what "the same fixture" means for keep-newest-only.
//
// The API page documents uuid as the fixture type identifier, constant
// across revisions, so revisions of one fixture share it. The key is the
// uuid PLUS the uploader label: a user's upload of a fixture keeps the
// manufacturer's uuid when it is an edit of the manufacturer's file, and
// letting a newer user edit silently replace the manufacturer's own file
// would undo the Manufacturer/User distinction the API terms make us show.
// So a manufacturer's newest and a user's newest of the same fixture are
// both kept, each labelled. An entry with no uuid cannot be matched to
// anything and is its own fixture (never replaced).
func fixtureKey(e Entry) string {
	u := strings.ToLower(strings.TrimSpace(e.UUID))
	if u == "" {
		return fmt.Sprintf("rid:%d", e.RID)
	}
	return "uuid:" + u + "|" + e.UploaderLabel
}

// newer reports whether a is a newer revision than b: by lastModified, then
// creationDate, then rid. The page does not say rids increase over time, so
// rid only breaks ties.
func newer(a, b Entry) bool {
	if a.LastModified != b.LastModified {
		return a.LastModified > b.LastModified
	}
	if a.CreationDate != b.CreationDate {
		return a.CreationDate > b.CreationDate
	}
	return a.RID > b.RID
}

func foldName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// --- sync -----------------------------------------------------------------

// gsession is one login and the re-login rule: on a 401, log in again once
// and retry; a second 401 before anything has succeeded stops the sync.
type gsession struct {
	c         *Client
	user      string
	pass      string
	relogged  bool // a re-login has happened since the last success
	relogins  int
	logins    int
	succeeded bool
}

func (g *gsession) login(ctx context.Context) error {
	g.logins++
	return g.c.Login(ctx, g.user, g.pass)
}

func (g *gsession) do(ctx context.Context, fn func() error) error {
	err := fn()
	if errors.Is(err, ErrUnauthorized) && !g.relogged {
		if lerr := g.login(ctx); lerr != nil {
			return lerr
		}
		g.relogged = true
		g.relogins++
		err = fn()
	}
	if err == nil {
		g.relogged = false
		g.succeeded = true
	}
	return err
}

func (s *Service) newSession(ctx context.Context) (*gsession, error) {
	user, pass, ok := s.cfg.Credentials()
	if !ok || user == "" || pass == "" {
		return nil, ErrNoLogin
	}
	c, err := NewClient(s.cfg.BaseURL, s.cfg.LightingNetworks, s.cfg.RequestTimeout)
	if err != nil {
		return nil, err
	}
	g := &gsession{c: c, user: user, pass: pass}
	if err := g.login(ctx); err != nil {
		return nil, err
	}
	return g, nil
}

// resultFor turns a stopping error into a Result.
func resultFor(err error) Result {
	var off *OfflineError
	switch {
	case errors.Is(err, ErrLightingRoute):
		return Result{State: StateRefused, Message: ErrLightingRoute.Error()}
	case errors.As(err, &off):
		return Result{State: StateOffline, Message: "Offline: " + err.Error() + ". The library on this computer is unchanged."}
	case errors.Is(err, ErrBadLogin):
		return Result{State: StateLoginFailed, Message: err.Error()}
	case errors.Is(err, ErrNoLogin):
		return Result{State: StateNoLogin, Message: err.Error()}
	case errors.Is(err, ErrUnauthorized):
		return Result{State: StateStopped, Message: "Stopped: " + err.Error() + " (logging in again did not help)"}
	case errors.Is(err, context.Canceled):
		return Result{State: StateStopped, Message: "Stopped before it finished; the next sync carries on"}
	}
	return Result{State: StateError, Message: err.Error()}
}

// Sync runs one sync: one login, one list fetch, then the downloads the
// followed manufacturers need, one at a time. It returns ErrBusy at once if
// anything else is talking to GDTF Share.
func (s *Service) Sync(ctx context.Context) (Result, error) {
	if !s.Enabled() {
		return Result{State: StateError, Message: ErrDisabled.Error()}, ErrDisabled
	}
	if !s.netMu.TryLock() {
		return Result{}, ErrBusy
	}
	defer s.netMu.Unlock()
	s.setRunning(true)
	defer s.setRunning(false)
	return s.runSync(ctx)
}

type syncRecord struct {
	listFetched bool
	entries     []Entry
	mresults    []ManufacturerResult
	pending     []Pending
	recorded    bool // mresults/pending are this sync's and replace the old
}

func (s *Service) runSync(ctx context.Context) (Result, error) {
	started := s.now()
	rec := &syncRecord{}
	s.mu.Lock()
	sd, err := s.syncLocked()
	var followed []string
	var oldPending []Pending
	if err == nil {
		followed = append([]string(nil), sd.Followed...)
		oldPending = append([]Pending(nil), sd.Pending...)
	}
	s.mu.Unlock()
	if err != nil {
		// sync.json itself is damaged: nothing can be recorded in it.
		s.cfg.Logf("GDTF sync: %v", err)
		return Result{State: StateError, Message: err.Error(), At: started}, err
	}
	finish := func(res Result, err error) (Result, error) {
		res.At = started
		s.recordSync(started, res, rec)
		if err != nil {
			s.cfg.Logf("GDTF sync: %s", res.Message)
		} else {
			s.cfg.Logf("GDTF sync: %s (%d downloaded, %d replaced, %d unchanged, %d pending)", res.State, res.Downloaded, res.Replaced, res.Unchanged, res.Failed)
		}
		return res, err
	}

	files, err := s.indexMap()
	if err != nil {
		return finish(Result{State: StateError, Message: err.Error()}, err)
	}
	s.st.cleanPartials()
	g, err := s.newSession(ctx)
	if err != nil {
		return finish(resultFor(err), err)
	}
	var entries []Entry
	var skipped int
	err = g.do(ctx, func() error {
		var e error
		entries, skipped, e = g.c.List(ctx)
		return e
	})
	if err != nil {
		res := resultFor(err)
		res.Relogins = g.relogins
		return finish(res, err)
	}
	rec.listFetched, rec.entries = true, entries
	if skipped > 0 {
		s.cfg.Logf("GDTF sync: %d list entries could not be read and were skipped", skipped)
	}
	s.cacheList(started, entries)

	// What GDTF Share has, per followed name (case-insensitive, exact).
	want := map[string]string{} // folded -> the user's spelling
	for _, n := range followed {
		want[foldName(n)] = n
	}
	shareName := map[string]string{}
	manufCount, userCount := map[string]int{}, map[string]int{}
	newest := map[string]Entry{}
	for _, e := range entries {
		fm := foldName(e.Manufacturer)
		if _, ok := shareName[fm]; !ok {
			shareName[fm] = e.Manufacturer
		}
		if _, ok := want[fm]; !ok {
			continue
		}
		switch e.UploaderLabel {
		case LabelManufacturer:
			manufCount[fm]++
		case LabelUser:
			userCount[fm]++
		}
		k := fixtureKey(e)
		if cur, ok := newest[k]; !ok || newer(e, cur) {
			newest[k] = e
		}
	}
	plan := make([]Entry, 0, len(newest))
	for _, e := range newest {
		plan = append(plan, e)
	}
	sort.Slice(plan, func(i, j int) bool {
		a, b := plan[i], plan[j]
		if fa, fb := foldName(a.Manufacturer), foldName(b.Manufacturer); fa != fb {
			return fa < fb
		}
		if fa, fb := foldName(a.Fixture), foldName(b.Fixture); fa != fb {
			return fa < fb
		}
		return a.RID < b.RID
	})

	res := Result{}
	perDown, perRepl, perFail := map[string]int{}, map[string]int{}, map[string]int{}
	prevPending := map[int64]Pending{}
	for _, p := range oldPending {
		prevPending[p.RID] = p
	}
	pending := make([]Pending, 0)
	addPending := func(e Entry, err error) {
		p := Pending{RID: e.RID, Manufacturer: e.Manufacturer, Fixture: e.Fixture, Revision: e.Revision,
			UploaderLabel: e.UploaderLabel, Error: err.Error(), Attempts: 1, Since: started}
		if old, ok := prevPending[e.RID]; ok {
			p.Attempts, p.Since = old.Attempts+1, old.Since
		}
		pending = append(pending, p)
		res.Failed++
		perFail[foldName(e.Manufacturer)]++
		s.cfg.Logf("GDTF sync: rid %d (%s %s) not downloaded: %v", e.RID, e.Manufacturer, e.Fixture, err)
	}
	// supersede removes every other file of the same fixture.
	supersede := func(k string, keep int64) int {
		n := 0
		for rid, ie := range files {
			if rid != keep && fixtureKey(ie.Entry) == k {
				if err := s.st.removeFile(rid); err != nil {
					s.cfg.Logf("GDTF sync: old revision rid %d could not be removed: %v", rid, err)
					continue
				}
				delete(files, rid)
				n++
			}
		}
		return n
	}
	dirty, sinceSave, downloads := false, 0, 0
	save := func() {
		if err := s.saveIndex(files); err != nil {
			s.cfg.Logf("GDTF sync: index not saved: %v", err)
		}
		dirty, sinceSave = false, 0
	}
	stop := func(err error) (Result, error) {
		if dirty {
			save()
		}
		r := resultFor(err)
		r.Downloaded, r.Replaced, r.Unchanged, r.Failed, r.Relogins = res.Downloaded, res.Replaced, res.Unchanged, res.Failed, g.relogins
		rec.mresults = s.manufacturerResults(followed, shareName, manufCount, userCount, perDown, perRepl, perFail, files, started)
		rec.pending, rec.recorded = pending, true
		return finish(r, err)
	}

	for i, e := range plan {
		s.setProgress(i, len(plan))
		if ctx.Err() != nil {
			return stop(ctx.Err())
		}
		k := fixtureKey(e)
		fm := foldName(e.Manufacturer)
		// Something newer of this fixture is already here (the list lost
		// its newest): never step back to an older revision.
		newerLocal := false
		for rid, ie := range files {
			if rid != e.RID && fixtureKey(ie.Entry) == k && newer(ie.Entry, e) {
				newerLocal = true
			}
		}
		if newerLocal {
			res.Unchanged++
			continue
		}
		if ie, ok := files[e.RID]; ok && s.st.fileMatches(ie) {
			if !reflect.DeepEqual(ie.Entry, e) {
				ie.Entry = e
				files[e.RID] = ie
				dirty = true
			}
			if n := supersede(k, e.RID); n > 0 {
				res.Replaced += n
				perRepl[fm] += n
				dirty = true
			}
			res.Unchanged++
			continue
		}
		// A complete file under the real name that the index does not know:
		// a sync was killed after the rename and before the index was
		// saved. Files only ever reach their real name complete, so adopt it.
		if _, ok := files[e.RID]; !ok {
			if sha, n, err := s.st.hashFile(e.RID); err == nil && n > 0 {
				files[e.RID] = IndexEntry{Entry: e, File: s.st.relFile(e.RID), SHA256: sha, LocalSize: n, DownloadedAt: started}
				res.Downloaded++
				perDown[fm]++
				if r := supersede(k, e.RID); r > 0 {
					res.Replaced += r
					perRepl[fm] += r
				}
				dirty = true
				continue
			}
		}
		if downloads > 0 {
			select {
			case <-ctx.Done():
				return stop(ctx.Err())
			case <-time.After(s.cfg.DownloadDelay):
			}
		}
		downloads++
		var sha string
		var size int64
		err := g.do(ctx, func() error {
			var e2 error
			sha, size, e2 = s.st.writeFile(e.RID, func(w io.Writer) error {
				_, err := g.c.Download(ctx, e.RID, w, s.cfg.MaxFileBytes)
				return err
			})
			return e2
		})
		if err != nil {
			var off *OfflineError
			if ctx.Err() != nil {
				return stop(ctx.Err())
			}
			if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrBadLogin) || errors.Is(err, ErrLightingRoute) || errors.As(err, &off) {
				addPending(e, err)
				return stop(err)
			}
			addPending(e, err)
			continue
		}
		files[e.RID] = IndexEntry{Entry: e, File: s.st.relFile(e.RID), SHA256: sha, LocalSize: size, DownloadedAt: s.now()}
		res.Downloaded++
		perDown[fm]++
		if n := supersede(k, e.RID); n > 0 {
			res.Replaced += n
			perRepl[fm] += n
		}
		dirty = true
		sinceSave++
		if sinceSave >= indexSaveEvery {
			save()
		}
	}
	s.setProgress(len(plan), len(plan))
	if dirty {
		save()
	}
	res.Relogins = g.relogins
	if res.Failed > 0 {
		res.State = StatePartial
		res.Message = fmt.Sprintf("Synced with %d file(s) still to download; the next sync retries them", res.Failed)
	} else {
		res.State = StateOK
		res.Message = fmt.Sprintf("Library up to date: %d file(s) for the followed manufacturers", len(plan))
	}
	rec.mresults = s.manufacturerResults(followed, shareName, manufCount, userCount, perDown, perRepl, perFail, files, started)
	rec.pending, rec.recorded = pending, true
	return finish(res, nil)
}

func (s *Service) manufacturerResults(followed []string, shareName map[string]string, manuf, user, down, repl, fail map[string]int, files map[int64]IndexEntry, at time.Time) []ManufacturerResult {
	local := map[string]int{}
	for _, ie := range files {
		local[foldName(ie.Manufacturer)]++
	}
	out := make([]ManufacturerResult, 0, len(followed))
	for _, n := range followed {
		fm := foldName(n)
		mr := ManufacturerResult{Name: n, CheckedAt: at, Similar: make([]string, 0), LocalFiles: local[fm]}
		if sn, ok := shareName[fm]; ok {
			mr.Matched, mr.ShareName = true, sn
			mr.ShareManufFiles, mr.ShareUserFiles = manuf[fm], user[fm]
			mr.Downloaded, mr.Replaced, mr.Failed = down[fm], repl[fm], fail[fm]
			mr.Result = fmt.Sprintf("%d on GDTF Share (%d manufacturer, %d user); %d downloaded, %d replaced, %d pending",
				manuf[fm]+user[fm], manuf[fm], user[fm], down[fm], repl[fm], fail[fm])
		} else {
			for fold, name := range shareName {
				if fm != "" && (strings.Contains(fold, fm) || strings.Contains(fm, fold)) && fold != "" {
					mr.Similar = append(mr.Similar, name)
				}
			}
			sort.Strings(mr.Similar)
			if len(mr.Similar) > 5 {
				mr.Similar = mr.Similar[:5]
			}
			mr.Result = fmt.Sprintf("No manufacturer named %q on GDTF Share; nothing was downloaded for it", n)
		}
		out = append(out, mr)
	}
	return out
}

// recordSync writes this sync's outcome into sync.json. Followed comes from
// the live document, so a list edited during a sync is not overwritten.
func (s *Service) recordSync(at time.Time, res Result, rec *syncRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sd, err := s.syncLocked()
	if err != nil {
		return
	}
	next := *sd
	next.LastAttempt = &at
	next.LastResult = res
	if rec.listFetched {
		t := at
		next.LastListFetch = &t
	}
	if rec.recorded {
		next.Manufacturers = rec.mresults
		next.Pending = rec.pending
	}
	if err := s.st.saveJSON("sync.json", next); err != nil {
		s.cfg.Logf("GDTF sync: sync.json not saved: %v", err)
		return
	}
	s.sd = &next
}

func (s *Service) cacheList(at time.Time, entries []Entry) {
	doc := listDoc{Format: listFormat, SchemaVersion: schemaV1, FetchedAt: at, List: entries}
	if err := s.st.saveJSON("list.json", doc); err != nil {
		s.cfg.Logf("GDTF sync: list cache not saved: %v", err)
	}
	s.mu.Lock()
	s.list = &doc
	s.mu.Unlock()
}

// --- single pull, login test ------------------------------------------------

// Pull downloads one rid on demand, followed manufacturer or not. The rid is
// looked up in the cached list; if it is not there the list is fetched once.
func (s *Service) Pull(ctx context.Context, rid int64) (IndexEntry, error) {
	if !s.Enabled() {
		return IndexEntry{}, ErrDisabled
	}
	if !s.netMu.TryLock() {
		return IndexEntry{}, ErrBusy
	}
	defer s.netMu.Unlock()
	s.setRunning(true)
	defer s.setRunning(false)
	files, err := s.indexMap()
	if err != nil {
		return IndexEntry{}, err
	}
	if ie, ok := files[rid]; ok && s.st.fileMatches(ie) {
		return ie, nil
	}
	s.st.cleanPartials()
	g, err := s.newSession(ctx)
	if err != nil {
		return IndexEntry{}, err
	}
	e, ok := s.listedEntry(rid)
	if !ok {
		var entries []Entry
		err := g.do(ctx, func() error {
			var e2 error
			entries, _, e2 = g.c.List(ctx)
			return e2
		})
		if err != nil {
			return IndexEntry{}, err
		}
		now := s.now()
		s.cacheList(now, entries)
		s.recordSync(now, s.lastResult(), &syncRecord{listFetched: true})
		if e, ok = s.listedEntry(rid); !ok {
			return IndexEntry{}, fmt.Errorf("GDTF Share's list has no file with rid %d", rid)
		}
	}
	k := fixtureKey(e)
	for other, ie := range files {
		if other != rid && fixtureKey(ie.Entry) == k && newer(ie.Entry, e) {
			return IndexEntry{}, fmt.Errorf("a newer revision of this fixture (rid %d, %q) is already in the library", other, ie.Revision)
		}
	}
	var sha string
	var size int64
	err = g.do(ctx, func() error {
		var e2 error
		sha, size, e2 = s.st.writeFile(rid, func(w io.Writer) error {
			_, err := g.c.Download(ctx, rid, w, s.cfg.MaxFileBytes)
			return err
		})
		return e2
	})
	if err != nil {
		return IndexEntry{}, err
	}
	ie := IndexEntry{Entry: e, File: s.st.relFile(rid), SHA256: sha, LocalSize: size, DownloadedAt: s.now()}
	files[rid] = ie
	for other, old := range files {
		if other != rid && fixtureKey(old.Entry) == k {
			if s.st.removeFile(other) == nil {
				delete(files, other)
			}
		}
	}
	if err := s.saveIndex(files); err != nil {
		return IndexEntry{}, err
	}
	s.cfg.Logf("GDTF: pulled rid %d (%s %s)", rid, e.Manufacturer, e.Fixture)
	return ie, nil
}

func (s *Service) lastResult() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sd, err := s.syncLocked(); err == nil {
		return sd.LastResult
	}
	return Result{}
}

func (s *Service) listedEntry(rid int64) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.listLocked()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range l.List {
		if e.RID == rid {
			return e, true
		}
	}
	return Entry{}, false
}

// TestLogin logs in with the given login (not saved) and reports the answer.
func (s *Service) TestLogin(ctx context.Context, user, password string) error {
	if user == "" || password == "" {
		return ErrNoLogin
	}
	if !s.netMu.TryLock() {
		return ErrBusy
	}
	defer s.netMu.Unlock()
	c, err := NewClient(s.cfg.BaseURL, s.cfg.LightingNetworks, s.cfg.RequestTimeout)
	if err != nil {
		return err
	}
	return c.Login(ctx, user, password)
}

// --- scheduler ----------------------------------------------------------

// Run is the scheduler: a sync at start-up when a login is saved, then one
// every Interval, plus Trigger's on-demand syncs. Blocks until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	s.runActive = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.runActive = false
		s.mu.Unlock()
	}()
	scheduled := func() {
		if _, _, ok := s.cfg.Credentials(); !ok {
			return
		}
		_, _ = s.Sync(ctx) // ErrBusy (a pull in progress) waits for the next turn
	}
	scheduled()
	timer := time.NewTimer(s.cfg.Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			_, _ = s.Sync(ctx)
		case <-timer.C:
			scheduled()
		}
		timer.Reset(s.cfg.Interval)
	}
}

// Trigger asks the scheduler for a sync now.
func (s *Service) Trigger() error {
	if !s.Enabled() {
		return ErrDisabled
	}
	s.mu.Lock()
	running, active := s.running, s.runActive
	s.mu.Unlock()
	if running {
		return ErrBusy
	}
	if !active {
		return ErrNotRunning
	}
	select {
	case s.trigger <- struct{}{}:
	default: // one already queued
	}
	return nil
}

// --- reading -------------------------------------------------------------

// Status reports the library's state.
func (s *Service) Status() Status {
	st := Status{Enabled: s.Enabled(), State: StateNever, IntervalMin: int(s.cfg.Interval / time.Minute),
		Followed: make([]string, 0), Manufacturers: make([]ManufacturerResult, 0), Pending: make([]Pending, 0)}
	if !st.Enabled {
		st.State, st.Message = StateError, ErrDisabled.Error()
		return st
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Running, st.Progress = s.running, s.progress
	sd, err := s.syncLocked()
	if err != nil {
		st.State, st.Message = StateError, err.Error()
		return st
	}
	if sd.LastResult.State != "" {
		st.State, st.Message = sd.LastResult.State, sd.LastResult.Message
	}
	st.LastResult = sd.LastResult
	st.LastAttempt, st.LastSync = sd.LastAttempt, sd.LastListFetch
	st.Pending = append(st.Pending, sd.Pending...)
	st.Followed = append(st.Followed, sd.Followed...)
	local := map[string]int{}
	if idx, err := s.indexLocked(); err != nil {
		st.State, st.Message = StateError, err.Error()
	} else {
		st.Files = len(idx.Files)
		for _, e := range idx.Files {
			local[foldName(e.Manufacturer)]++
		}
	}
	for _, n := range sd.Followed {
		var mr *ManufacturerResult
		for i := range sd.Manufacturers {
			if sd.Manufacturers[i].Name == n {
				mr = &sd.Manufacturers[i]
			}
		}
		if mr == nil {
			st.Manufacturers = append(st.Manufacturers, ManufacturerResult{Name: n, NotYetChecked: true,
				Result: "Not checked against GDTF Share yet", Similar: make([]string, 0), LocalFiles: local[foldName(n)]})
			continue
		}
		c := *mr
		c.Similar = append(make([]string, 0, len(mr.Similar)), mr.Similar...)
		c.LocalFiles = local[foldName(n)]
		st.Manufacturers = append(st.Manufacturers, c)
	}
	if st.Running {
		st.State, st.Message = StateSyncing, "Syncing with GDTF Share"
	}
	return st
}

// Followed is the followed-manufacturer list.
func (s *Service) Followed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sd, err := s.syncLocked()
	if err != nil {
		return make([]string, 0)
	}
	return append(make([]string, 0, len(sd.Followed)), sd.Followed...)
}

// SetFollowed replaces the followed-manufacturer list: names trimmed, blanks
// dropped, duplicates (case-insensitive) dropped keeping the first spelling.
func (s *Service) SetFollowed(names []string) ([]string, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if len(n) > maxNameLen || strings.IndexFunc(n, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("manufacturer name %.40q is not usable (at most %d characters, no control characters)", n, maxNameLen)
		}
		if seen[foldName(n)] {
			continue
		}
		seen[foldName(n)] = true
		out = append(out, n)
	}
	if len(out) > maxFollowed {
		return nil, fmt.Errorf("at most %d manufacturers can be followed", maxFollowed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sd, err := s.syncLocked()
	if err != nil {
		return nil, err
	}
	next := *sd
	next.Followed = out
	if err := s.st.saveJSON("sync.json", next); err != nil {
		return nil, err
	}
	s.sd = &next
	return append([]string(nil), out...), nil
}

// Search finds files by name, case-insensitively: every word of q must
// appear in the manufacturer or fixture name. share false searches the files
// on this computer; share true searches the last list fetched from GDTF
// Share (every manufacturer). limit 0 means no limit.
//
// The API terms ask for search over Name AND LongName. getList gives only
// one name (fixture); the LongName is inside each file's description.xml,
// which this package does not read (G2 reads files). Until then only the
// name GDTF Share lists is searched.
func (s *Service) Search(q string, share bool, limit int) ([]SearchResult, error) {
	out := make([]SearchResult, 0)
	if !s.Enabled() {
		return out, ErrDisabled
	}
	words := strings.Fields(strings.ToLower(q))
	match := func(e Entry) bool {
		hay := strings.ToLower(e.Manufacturer + " " + e.Fixture)
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	s.mu.Lock()
	idx, err := s.indexLocked()
	if err != nil {
		s.mu.Unlock()
		return out, err
	}
	have := make(map[int64]bool, len(idx.Files))
	for _, e := range idx.Files {
		have[e.RID] = true
		if !share && match(e.Entry) {
			out = append(out, SearchResult{Entry: e.Entry, InLibrary: true})
		}
	}
	if share {
		l, err := s.listLocked()
		if err != nil {
			s.mu.Unlock()
			return out, err
		}
		for _, e := range l.List {
			if match(e) {
				out = append(out, SearchResult{Entry: e, InLibrary: have[e.RID]})
			}
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if fa, fb := foldName(a.Manufacturer), foldName(b.Manufacturer); fa != fb {
			return fa < fb
		}
		if fa, fb := foldName(a.Fixture), foldName(b.Fixture); fa != fb {
			return fa < fb
		}
		return a.RID < b.RID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// OpenFile opens a downloaded file by rid. The caller closes it.
func (s *Service) OpenFile(rid int64) (*os.File, IndexEntry, error) {
	if !s.Enabled() {
		return nil, IndexEntry{}, ErrDisabled
	}
	files, err := s.indexMap()
	if err != nil {
		return nil, IndexEntry{}, err
	}
	ie, ok := files[rid]
	if !ok {
		return nil, IndexEntry{}, fmt.Errorf("rid %d is not in the GDTF library: %w", rid, os.ErrNotExist)
	}
	f, err := os.Open(s.st.absFile(rid))
	if err != nil {
		return nil, IndexEntry{}, err
	}
	return f, ie, nil
}
