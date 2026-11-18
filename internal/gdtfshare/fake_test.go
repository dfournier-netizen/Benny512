package gdtfshare

// A fake GDTF Share, built from the request and response shapes on the API
// page (https://gdtf-share.com/help/developers/gdtf_share_dev/
// gdtf_share_api_1/index.html, read 2026-11-17):
//
//   - POST login.php, JSON {"user","password"}: 200 {"result":true,"notice":…}
//     plus a session cookie, or 401 {"result":false,"error":"No valid user or
//     password provided."}. The page never names the cookie, so this fake
//     uses a name of its own: the client must not depend on one.
//   - GET getList.php with the cookie: 200 {"result":true,"list":[…]} or 401
//     {"result":false,"error":"Unauthorized."}. Dates and rating are quoted
//     strings, as in the page's example.
//   - GET downloadFile.php?rid=N with the cookie: 200 application/octet-stream,
//     or a JSON error (404 {"result":false,"error":"File does not exist."}).

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeFile struct {
	RID          int64
	Fixture      string
	Manufacturer string
	Revision     string
	Uploader     string // "Manuf." | "User"
	UUID         string
	LastModified int64
	Data         []byte
}

type fakeShare struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	user     string
	pass     string
	files    []fakeFile
	sessions map[string]bool

	logins      int
	listCalls   int
	downloads   []int64     // every download request, in order
	downloadAt  []time.Time // when each started
	inflight    int
	maxInflight int
	conns       int

	failRID map[int64]int // rid -> HTTP status to answer instead of the file
	// expireAfter > 0: after that many successful downloads every session is
	// dropped once, as GDTF Share's two-hour inactivity expiry would.
	expireAfter   int
	expired       bool
	okDownloads   int
	alwaysExpired bool // every list/download answers 401, even after login

	// halfRID: answer with half the file, signal halfSent, then hold the
	// connection until the client goes away.
	halfRID  int64
	halfSent chan struct{}
	// blockRID: hold the request until release is closed.
	blockRID  int64
	blocked   chan struct{}
	releaseCh chan struct{}

	bodies []string // every request body seen, for the password grep
}

func newFakeShare(t *testing.T) *fakeShare {
	t.Helper()
	f := &fakeShare{t: t, user: "dom", pass: "s3cret-Pa55word!", sessions: map[string]bool{}, failRID: map[int64]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/public/login.php", f.login)
	mux.HandleFunc("/apis/public/getList.php", f.list)
	mux.HandleFunc("/apis/public/downloadFile.php", f.download)
	f.srv = httptest.NewUnstartedServer(mux)
	f.srv.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
		}
	}
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeShare) baseURL() string { return f.srv.URL + "/apis/public/" }

func (f *fakeShare) add(ff fakeFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ff.Data == nil {
		ff.Data = gdtfBytes(f.t, ff.Manufacturer+" "+ff.Fixture+" "+ff.Revision)
	}
	f.files = append(f.files, ff)
}

func (f *fakeShare) set(fn func(*fakeShare)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeShare) count(fn func(*fakeShare) int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func gdtfBytes(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("description.xml")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><GDTF DataVersion="1.2"><FixtureType Name=%q/></GDTF>`, name)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFakeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeShare) authed(r *http.Request) bool {
	c, err := r.Cookie("gdtfsess")
	if err != nil {
		return false
	}
	return f.sessions[c.Value] && !f.alwaysExpired
}

func (f *fakeShare) login(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	_, _ = body.ReadFrom(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, body.String())
	f.logins++
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		writeFakeJSON(w, 400, map[string]any{"result": false, "error": "Bad request."})
		return
	}
	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if json.Unmarshal(body.Bytes(), &req) != nil || req.User != f.user || req.Password != f.pass {
		writeFakeJSON(w, 401, map[string]any{"result": false, "error": "No valid user or password provided."})
		return
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	sid := hex.EncodeToString(b[:])
	f.sessions[sid] = true
	http.SetCookie(w, &http.Cookie{Name: "gdtfsess", Value: sid, Path: "/"})
	writeFakeJSON(w, 200, map[string]any{"result": true, "notice": "Welcome!"})
}

func (f *fakeShare) list(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if !f.authed(r) {
		writeFakeJSON(w, 401, map[string]any{"result": false, "error": "Unauthorized."})
		return
	}
	items := make([]map[string]any, 0, len(f.files))
	for _, ff := range f.files {
		items = append(items, map[string]any{
			"rid": ff.RID, "fixture": ff.Fixture, "manufacturer": ff.Manufacturer,
			"revision": ff.Revision, "creationDate": strconv.FormatInt(ff.LastModified-100, 10),
			"lastModified": strconv.FormatInt(ff.LastModified, 10), "uploader": ff.Uploader,
			"rating": "4.1", "version": "1.2", "creator": "someone", "uuid": ff.UUID,
			"filesize": len(ff.Data),
			"modes":    []map[string]any{{"name": "Standard", "dmxfootprint": 16}},
		})
	}
	writeFakeJSON(w, 200, map[string]any{"result": true, "list": items})
}

func (f *fakeShare) download(w http.ResponseWriter, r *http.Request) {
	rid, _ := strconv.ParseInt(r.URL.Query().Get("rid"), 10, 64)
	f.mu.Lock()
	f.downloads = append(f.downloads, rid)
	f.downloadAt = append(f.downloadAt, time.Now())
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if !f.authed(r) {
		f.mu.Unlock()
		writeFakeJSON(w, 401, map[string]any{"result": false, "error": "Unauthorized."})
		return
	}
	if code, ok := f.failRID[rid]; ok {
		f.mu.Unlock()
		writeFakeJSON(w, code, map[string]any{"result": false, "error": "Internal error."})
		return
	}
	var data []byte
	for _, ff := range f.files {
		if ff.RID == rid {
			data = ff.Data
		}
	}
	if data == nil {
		f.mu.Unlock()
		writeFakeJSON(w, 404, map[string]any{"result": false, "error": "File does not exist."})
		return
	}
	half, block := rid == f.halfRID, rid == f.blockRID
	halfSent, blocked, release := f.halfSent, f.blocked, f.releaseCh
	f.okDownloads++
	if f.expireAfter > 0 && !f.expired && f.okDownloads >= f.expireAfter {
		f.expired = true
		f.sessions = map[string]bool{}
	}
	f.mu.Unlock()

	if block {
		close(blocked)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	if half {
		_, _ = w.Write(data[:len(data)/2])
		w.(http.Flusher).Flush()
		close(halfSent)
		<-r.Context().Done()
		return
	}
	_, _ = w.Write(data)
}

// newTestService builds a Service against the fake with a temp root and no
// spacing between downloads unless the test sets one.
func newTestService(t *testing.T, f *fakeShare, mutate func(*Config)) (*Service, *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	var logMu sync.Mutex
	cfg := Config{
		Root:          t.TempDir(),
		BaseURL:       f.baseURL(),
		Credentials:   func() (string, string, bool) { return f.user, f.pass, true },
		DownloadDelay: time.Nanosecond,
		Logf: func(format string, args ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			fmt.Fprintf(&logBuf, format+"\n", args...)
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg), &logBuf
}

func syncOK(t *testing.T, s *Service) Result {
	t.Helper()
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v (result %+v)", err, res)
	}
	return res
}
