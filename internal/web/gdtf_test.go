package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"benny512/internal/gdtfshare"
)

// A minimal fake GDTF Share in the documented shapes (the full fake, with
// expiry and failure injection, is internal/gdtfshare's fake_test.go).
type webFakeShare struct {
	srv   *httptest.Server
	mu    sync.Mutex
	user  string
	pass  string
	files map[int64][]byte
	conns int
	reqs  int
}

const webFakePassword = "Zq9-unique-GDTF-password-7731"

func newWebFakeShare(t *testing.T) *webFakeShare {
	t.Helper()
	f := &webFakeShare{user: "dom", pass: webFakePassword, files: map[int64][]byte{}}
	for _, rid := range []int64{101, 201, 901} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("description.xml")
		fmt.Fprintf(w, `<GDTF DataVersion="1.2"><FixtureType Name="f%d"/></GDTF>`, rid)
		zw.Close()
		f.files[rid] = buf.Bytes()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/public/login.php", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs++
		f.mu.Unlock()
		var req struct{ User, Password string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.User != f.user || req.Password != f.pass {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"result":false,"error":"No valid user or password provided."}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "ok", Path: "/"})
		fmt.Fprint(w, `{"result":true,"notice":"Welcome!"}`)
	})
	mux.HandleFunc("/apis/public/getList.php", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if c, err := r.Cookie("s"); err != nil || c.Value != "ok" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"result":false,"error":"Unauthorized."}`)
			return
		}
		item := func(rid int64, fixture, manuf, uploader, uuid string) string {
			return fmt.Sprintf(`{"rid":%d,"fixture":%q,"manufacturer":%q,"revision":"r1","creationDate":"1600000000","lastModified":"1600000000","uploader":%q,"rating":"4.0","version":"1.2","creator":"x","uuid":%q,"filesize":%d,"modes":[{"name":"Std","dmxfootprint":16}]}`,
				rid, fixture, manuf, uploader, uuid, len(f.files[rid]))
		}
		fmt.Fprintf(w, `{"result":true,"list":[%s,%s,%s]}`,
			item(101, "Spiider", "Robe", "Manuf.", "u-1"),
			item(201, "JDC1", "GLP", "User", "u-2"),
			item(901, "Sharpy", "Clay Paky", "Manuf.", "u-3"))
	})
	mux.HandleFunc("/apis/public/downloadFile.php", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs++
		f.mu.Unlock()
		if c, err := r.Cookie("s"); err != nil || c.Value != "ok" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"result":false,"error":"Unauthorized."}`)
			return
		}
		rid, _ := strconv.ParseInt(r.URL.Query().Get("rid"), 10, 64)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(f.files[rid])
	})
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

// useFakeGDTF points h's GDTF library at a temp folder and the fake.
func useFakeGDTF(t *testing.T, h *testHarness, f *webFakeShare, root string) {
	t.Helper()
	cfg := h.srv.gdtfConfig(root)
	cfg.BaseURL = f.srv.URL + "/apis/public/"
	cfg.DownloadDelay = time.Nanosecond
	h.srv.gdtf = gdtfshare.New(cfg)
}

func waitGDTFIdle(t *testing.T, h *testHarness) gdtfStatusResponse {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rr := doJSON(t, h.srv.Handler(), "GET", "/api/gdtf/status", nil)
		var st gdtfStatusResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
			t.Fatalf("status: %v %s", err, rr.Body.String())
		}
		if !st.Running && st.LastAttempt != nil {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("GDTF sync never finished: %s", rr.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestGDTFPasswordNeverLeaves: the password goes in through POST
// /api/gdtf/login and never comes out — not in any API response, the log,
// the settings file (on this non-DPAPI build), a show save, a library
// export, diagnostics or a capture export. Every output is grepped for the
// literal password.
func TestGDTFPasswordNeverLeaves(t *testing.T) {
	var logBuf bytes.Buffer
	var logMu sync.Mutex
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		logMu.Lock()
		defer logMu.Unlock()
		return logBuf.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	f := newWebFakeShare(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	dir := t.TempDir()
	if err := h.srv.SetSettingsStorePath(filepath.Join(dir, "benny512-settings.json")); err != nil {
		t.Fatal(err)
	}
	h.srv.SetPatchStorePath(filepath.Join(dir, "benny512-patch.json"))
	h.srv.SetLibraryStorePath(filepath.Join(dir, "benny512-library.json"))
	useFakeGDTF(t, h, f, filepath.Join(dir, "GDTF"))
	handler := h.srv.Handler()

	var outputs []string
	call := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		rr := doJSON(t, handler, method, path, body)
		if rr.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, rr.Code, want, rr.Body.String())
		}
		outputs = append(outputs, method+" "+path+": "+rr.Body.String()+fmt.Sprint(rr.Header()))
		return rr
	}

	rr := call("POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword}, 200)
	var login gdtfLoginStatus
	_ = json.Unmarshal(rr.Body.Bytes(), &login)
	if !login.Saved || login.User != "dom" {
		t.Fatalf("login = %+v", login)
	}
	if !gdtfshare.SecretsPersist && login.Storage != "memory" {
		t.Fatalf("storage = %q, want memory on a build with no protected store", login.Storage)
	}
	rr = call("POST", "/api/gdtf/login/test", nil, 200)
	if !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("test login = %s", rr.Body.String())
	}
	call("POST", "/api/gdtf/login/test", map[string]string{"user": "dom", "password": "wrong-" + webFakePassword}, 200)

	// The scheduler's start-up sync, with the login saved.
	ctx, cancel := context.WithCancel(context.Background())
	// Wait for the scheduler to stop before the temp folder is removed: a
	// sync the test triggered may still be writing index.json (seen as
	// "TempDir RemoveAll cleanup: ... directory not empty", 1-2 in 30 runs).
	runDone := make(chan struct{})
	defer func() { cancel(); <-runDone }()
	go func() { h.srv.gdtf.Run(ctx); close(runDone) }()
	st := waitGDTFIdle(t, h)
	if st.State != gdtfshare.StateOK || st.Files != 2 || st.Login.User != "dom" {
		t.Fatalf("after start-up sync: state %q files %d login %+v", st.State, st.Files, st.Login)
	}
	call("GET", "/api/gdtf/status", nil, 200)
	call("GET", "/api/settings", nil, 200)
	call("POST", "/api/settings", h.srv.SettingsSnapshot(), 200)
	call("GET", "/api/gdtf/search?in=share&q=", nil, 200)
	call("POST", "/api/gdtf/pull", map[string]int64{"rid": 901}, 200)
	call("GET", "/api/gdtf/file/101", nil, 200)
	call("POST", "/api/gdtf/sync", nil, 202)
	waitGDTFIdle(t, h)
	call("POST", "/api/patches", map[string]string{"name": "Leak check"}, 200)
	call("GET", "/api/patch/export", nil, 200)
	call("GET", "/api/library/export", nil, 200)
	call("GET", "/api/diagnostics/rdm", nil, 200)
	call("GET", "/api/capture/export", nil, 200)
	call("GET", "/api/context", nil, 200)

	// The login survived POST /api/settings (settings.js never sends it).
	if u, _, ok := h.srv.gdtfCredentials(); !ok || u != "dom" {
		t.Fatalf("POST /api/settings dropped the GDTF login (user %q, ok %v)", u, ok)
	}

	for _, out := range outputs {
		if strings.Contains(out, webFakePassword) {
			t.Fatalf("the password is in an API response: %.300s", out)
		}
	}
	logMu.Lock()
	logged := logBuf.String()
	logMu.Unlock()
	if strings.Contains(logged, webFakePassword) {
		t.Fatal("the password is in the log output")
	}
	if !strings.Contains(logged, "GDTF") {
		t.Fatalf("the GDTF sync logged nothing, so the log grep proves nothing: %q", logged)
	}
	files := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files++
		data, _ := os.ReadFile(p)
		if bytes.Contains(data, []byte(webFakePassword)) {
			t.Errorf("the password is in %s", p)
		}
		return nil
	})
	if files < 6 {
		t.Fatalf("only %d files written under the temp dir; the grep proves too little", files)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "benny512-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]json.RawMessage
	_ = json.Unmarshal(raw, &onDisk)
	if string(onDisk["gdtfShareUser"]) != `"dom"` {
		t.Fatalf("settings file gdtfShareUser = %s", onDisk["gdtfShareUser"])
	}
	if !gdtfshare.SecretsPersist {
		if _, ok := onDisk["gdtfSharePasswordDpapi"]; ok {
			t.Fatalf("a password entry was written on a build with no protected store: %s", raw)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// TestGDTFSettingsNeverSendsTheProtectedBlob: even encrypted, the password
// is not sent by GET /api/settings, and POST /api/settings (which
// settings.js sends without it) keeps the saved login.
func TestGDTFSettingsNeverSendsTheProtectedBlob(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	h.srv.settingsMu.Lock()
	h.srv.settings.GDTFShareUser = "dom"
	h.srv.settings.GDTFSharePasswordDPAPI = []byte("PROTECTED-BLOB-BYTES")
	h.srv.settingsMu.Unlock()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	if strings.Contains(rr.Body.String(), "gdtfSharePasswordDpapi") || strings.Contains(rr.Body.String(), "UFJPVEVDVEVE") {
		t.Fatalf("GET /api/settings sends the protected password: %s", rr.Body.String())
	}
	var sent map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &sent)
	delete(sent, "gdtfShareUser")
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", sent); rr.Code != 200 {
		t.Fatalf("POST /api/settings = %d %s", rr.Code, rr.Body.String())
	}
	got := h.srv.SettingsSnapshot()
	if got.GDTFShareUser != "dom" || string(got.GDTFSharePasswordDPAPI) != "PROTECTED-BLOB-BYTES" {
		t.Fatalf("after POST /api/settings the login is %q / %q", got.GDTFShareUser, got.GDTFSharePasswordDPAPI)
	}
}

// TestGDTFRefusesTheLightingAdapter: with the Art-Net adapter bound to the
// loopback address the fake GDTF Share listens on, the start-up sync is
// refused, says why, and the fake sees no connection at all.
func TestGDTFRefusesTheLightingAdapter(t *testing.T) {
	f := newWebFakeShare(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	h.srv.OutputBindIP = net.ParseIP("127.0.0.1")
	useFakeGDTF(t, h, f, t.TempDir())
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword}); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Wait for the scheduler to stop before the temp folder is removed: a
	// sync the test triggered may still be writing index.json (seen as
	// "TempDir RemoveAll cleanup: ... directory not empty", 1-2 in 30 runs).
	runDone := make(chan struct{})
	defer func() { cancel(); <-runDone }()
	go func() { h.srv.gdtf.Run(ctx); close(runDone) }()
	st := waitGDTFIdle(t, h)
	if st.State != gdtfshare.StateRefused || st.Message != "the only route to the internet is the lighting network adapter" {
		t.Fatalf("state %q message %q", st.State, st.Message)
	}
	f.mu.Lock()
	n := f.conns + f.reqs
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("the fake GDTF Share saw %d connections/requests over the lighting adapter", n)
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/gdtf/login/test", nil)
	if !strings.Contains(rr.Body.String(), `"state":"refused"`) {
		t.Fatalf("test login over the lighting adapter = %s", rr.Body.String())
	}
}

// TestGDTFAPIEndpoints: manufacturers, search with labels, file bytes, and
// the sync trigger's refusals.
func TestGDTFAPIEndpoints(t *testing.T) {
	f := newWebFakeShare(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()

	// Disabled (no folder): the status says so.
	rr := doJSON(t, handler, "GET", "/api/gdtf/status", nil)
	if !strings.Contains(rr.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled status = %s", rr.Body.String())
	}
	useFakeGDTF(t, h, f, t.TempDir())
	if rr := doJSON(t, handler, "POST", "/api/gdtf/sync", nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("sync with no login = %d %s", rr.Code, rr.Body.String())
	}
	doJSON(t, handler, "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword})
	if rr := doJSON(t, handler, "POST", "/api/gdtf/sync", nil); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("sync with no scheduler = %d %s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, handler, "POST", "/api/gdtf/manufacturers", map[string][]string{"manufacturers": {"robe", "GLP", "Nobody"}})
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"followed":["robe","GLP","Nobody"]`) {
		t.Fatalf("manufacturers = %d %s", rr.Code, rr.Body.String())
	}
	if _, err := h.srv.gdtf.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var st gdtfStatusResponse
	_ = json.Unmarshal(doJSON(t, handler, "GET", "/api/gdtf/status", nil).Body.Bytes(), &st)
	var nobody *gdtfshare.ManufacturerResult
	for i := range st.Manufacturers {
		if st.Manufacturers[i].Name == "Nobody" {
			nobody = &st.Manufacturers[i]
		}
	}
	if nobody == nil || nobody.Matched || !strings.Contains(nobody.Result, "Nobody") {
		t.Fatalf("an unmatched name is not reported: %+v", st.Manufacturers)
	}
	rr = doJSON(t, handler, "GET", "/api/gdtf/search?q=JDC", nil)
	var sr gdtfSearchResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &sr)
	if sr.Count != 1 || sr.Results[0].RID != 201 || sr.Results[0].UploaderLabel != "User" {
		t.Fatalf("search = %s", rr.Body.String())
	}
	rr = doJSON(t, handler, "GET", "/api/gdtf/search?q=nothing-like-this", nil)
	if !strings.Contains(rr.Body.String(), `"count":0`) || !strings.Contains(rr.Body.String(), `"results":[]`) {
		t.Fatalf("empty search = %s", rr.Body.String())
	}
	rr = doJSON(t, handler, "GET", "/api/gdtf/file/101", nil)
	if rr.Code != 200 || !bytes.Equal(rr.Body.Bytes(), f.files[101]) {
		t.Fatalf("file bytes = %d, %d bytes", rr.Code, rr.Body.Len())
	}
	if _, params, err := mime.ParseMediaType(rr.Header().Get("Content-Disposition")); err != nil || params["filename"] != "Robe@Spiider@r1.gdtf" {
		t.Fatalf("Content-Disposition = %q (%v)", rr.Header().Get("Content-Disposition"), err)
	}
	if rr.Header().Get("X-Benny-GDTF-Uploader") != "Manufacturer" {
		t.Fatalf("uploader header = %q", rr.Header().Get("X-Benny-GDTF-Uploader"))
	}
	if rr := doJSON(t, handler, "GET", "/api/gdtf/file/901", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("file not in the library = %d", rr.Code)
	}
	rr = doJSON(t, handler, "POST", "/api/gdtf/pull", map[string]int64{"rid": 901})
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"uploaderLabel":"Manufacturer"`) {
		t.Fatalf("pull = %d %s", rr.Code, rr.Body.String())
	}
	if rr := doJSON(t, handler, "DELETE", "/api/gdtf/login", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"storage":"none"`) {
		t.Fatalf("clear login = %d %s", rr.Code, rr.Body.String())
	}
}
