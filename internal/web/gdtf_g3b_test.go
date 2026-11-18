package web

// G3b1: the server data the approved G3a mockup marks NEW
// (docs/design/gdtf-library/README.md): which adapters sync refuses
// (lightingAdapters), which adapter the last connection left by
// (internetRoute.adapter), the Fixture Library's GDTF cap, a version for
// About, and whether a GDTF file is already in the Fixture Library. Each is
// checked on the wire (decoded independently of the handler's types).

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

// loopbackIfi is this machine's loopback adapter, for a real adapter name
// and address in the tests (the fake GDTF Share listens on 127.0.0.1).
func loopbackIfi(t *testing.T) *net.Interface {
	t.Helper()
	ifs, err := net.Interfaces()
	if err != nil {
		t.Skip(err)
	}
	for i := range ifs {
		if ifs[i].Flags&net.FlagLoopback == 0 {
			continue
		}
		addrs, _ := ifs[i].Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(net.IPv4(127, 0, 0, 1)) {
				return &ifs[i]
			}
		}
	}
	t.Skip("no loopback adapter with 127.0.0.1")
	return nil
}

type adapterJSON struct {
	Protocol     string   `json:"protocol"`
	Bound        *bool    `json:"bound"`
	Name         string   `json:"name"`
	Addresses    []string `json:"addresses"`
	Auto         *bool    `json:"auto"`
	SameAsArtNet *bool    `json:"sameAsArtNet"`
}

type g3bStatusJSON struct {
	LightingAdapters []adapterJSON `json:"lightingAdapters"`
	InternetRoute    *struct {
		LocalAddress string `json:"localAddress"`
		Adapter      string `json:"adapter"`
		At           string `json:"at"`
	} `json:"internetRoute"`
	FixtureLibraryMaxBytes *int64 `json:"fixtureLibraryMaxBytes"`
	Progress               struct {
		Manufacturer *string `json:"manufacturer"`
	} `json:"progress"`
	UnreadableFiles []json.RawMessage `json:"unreadableFiles"`
}

func g3bStatus(t *testing.T, h http.Handler) (g3bStatusJSON, string) {
	t.Helper()
	rr := doJSON(t, h, "GET", "/api/gdtf/status", nil)
	var st g3bStatusJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("status: %v %s", err, rr.Body.String())
	}
	return st, rr.Body.String()
}

// TestG3b1StatusNamesTheLightingAdapters: the status names the adapters
// sync never uses (owner round 3 #1: say which adapter is the lighting
// network, before the user hits a refusal), whether Art-Net's was picked
// automatically, and whether sACN shares it. With no adapter bound (--demo,
// tests) it says so rather than naming one.
func TestG3b1StatusNamesTheLightingAdapters(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()

	st, body := g3bStatus(t, handler)
	if len(st.LightingAdapters) != 2 || st.LightingAdapters[0].Protocol != "Art-Net" || st.LightingAdapters[1].Protocol != "sACN" {
		t.Fatalf("lightingAdapters with nothing bound = %s", body)
	}
	if a := st.LightingAdapters[0]; a.Bound == nil || *a.Bound || a.Name != "" || a.Addresses == nil || len(a.Addresses) != 0 {
		t.Fatalf("Art-Net with no adapter bound = %+v, want bound false, no name, []", a)
	}
	if st.FixtureLibraryMaxBytes == nil || *st.FixtureLibraryMaxBytes != 20<<20 {
		t.Fatalf("fixtureLibraryMaxBytes = %v, want %d", st.FixtureLibraryMaxBytes, 20<<20)
	}
	if st.UnreadableFiles == nil || st.Progress.Manufacturer == nil {
		t.Fatalf("gdtfshare's new fields are not on the wire: %s", body)
	}

	lo := loopbackIfi(t)
	h.srv.OutputInterface = lo
	h.srv.OutputBindIP = net.ParseIP("127.0.0.1")
	h.srv.OutputNICAuto = true
	st, body = g3bStatus(t, handler)
	art, sacn := st.LightingAdapters[0], st.LightingAdapters[1]
	if art.Bound == nil || !*art.Bound || art.Name != lo.Name || art.Auto == nil || !*art.Auto || !containsPrefix(art.Addresses, "127.0.0.1/") {
		t.Fatalf("Art-Net adapter = %+v (%s)", art, body)
	}
	if sacn.SameAsArtNet == nil || !*sacn.SameAsArtNet || sacn.Name != lo.Name || sacn.Bound == nil || !*sacn.Bound {
		t.Fatalf("sACN on the same adapter = %+v", sacn)
	}

	// sACN on an adapter of its own that this computer does not have.
	h.srv.settingsMu.Lock()
	h.srv.settings.SACNNIC = "No Such Adapter 9"
	h.srv.settingsMu.Unlock()
	h.srv.OutputNICAuto = false
	st, _ = g3bStatus(t, handler)
	art, sacn = st.LightingAdapters[0], st.LightingAdapters[1]
	if art.Auto == nil || *art.Auto {
		t.Fatalf("Art-Net chosen in Settings still says auto: %+v", art)
	}
	if sacn.SameAsArtNet == nil || *sacn.SameAsArtNet || sacn.Name != "No Such Adapter 9" || sacn.Bound == nil || *sacn.Bound {
		t.Fatalf("sACN on a missing adapter = %+v, want its name, not bound", sacn)
	}
}

func containsPrefix(list []string, p string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// TestG3b1StatusInternetRouteNamesTheAdapter: after a sync the route says
// which adapter the connection left by; before any it is null.
func TestG3b1StatusInternetRouteNamesTheAdapter(t *testing.T) {
	lo := loopbackIfi(t)
	f := newWebFakeShare(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()
	useFakeGDTF(t, h, f, t.TempDir())
	if st, body := g3bStatus(t, handler); st.InternetRoute != nil || !strings.Contains(body, `"internetRoute":null`) {
		t.Fatalf("route before any connection = %s", body)
	}
	doJSON(t, handler, "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword})
	if _, err := h.srv.gdtf.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, body := g3bStatus(t, handler)
	if st.InternetRoute == nil || st.InternetRoute.LocalAddress != "127.0.0.1" || st.InternetRoute.Adapter != lo.Name || st.InternetRoute.At == "" {
		t.Fatalf("route after a sync = %s, want 127.0.0.1 on %s", body, lo.Name)
	}
}

// TestG3b1VersionEndpoint: About shows what --version prints.
func TestG3b1VersionEndpoint(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	var v struct{ Version, Commit, Built, Line string }
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/version", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || rr.Code != 200 {
		t.Fatalf("version = %d %s", rr.Code, rr.Body.String())
	}
	if v.Version != "dev" || v.Commit != "unknown" || v.Built != "unknown" || v.Line != "benny512 dev (commit unknown, built unknown)" {
		t.Fatalf("an unstamped server = %+v, want what an unstamped --version says", v)
	}
	h.srv.SetBuildInfo(BuildInfo{Version: "v0.3.5", Commit: "abc1234", Built: "2026-11-18T09:00:00Z"})
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/version", nil)
	if !strings.Contains(rr.Body.String(), `"line":"benny512 v0.3.5 (commit abc1234, built 2026-11-18T09:00:00Z)"`) ||
		!strings.Contains(rr.Body.String(), `"version":"v0.3.5"`) {
		t.Fatalf("stamped version = %s", rr.Body.String())
	}
}

// TestG3b1SearchAndMatchSayInFixtureLibrary: a GDTF file whose bytes the
// Fixture Library already keeps is marked inFixtureLibrary on search and
// match; the import through the existing path also takes a GDTF of up to
// 20 MB and keeps the uploader label.
func TestG3b1SearchAndMatchSayInFixtureLibrary(t *testing.T) {
	f := newWebFakeShare(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()
	useFakeGDTF(t, h, f, t.TempDir())
	doJSON(t, handler, "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword})
	doJSON(t, handler, "POST", "/api/gdtf/manufacturers", map[string][]string{"manufacturers": {"Robe", "GLP"}})
	if _, err := h.srv.gdtf.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	type hit struct {
		RID              int64  `json:"rid"`
		SHA256           string `json:"sha256"`
		InFixtureLibrary *bool  `json:"inFixtureLibrary"`
	}
	search := func() map[int64]hit {
		var sr struct{ Results []hit }
		rr := doJSON(t, handler, "GET", "/api/gdtf/search?q=", nil)
		if err := json.Unmarshal(rr.Body.Bytes(), &sr); err != nil {
			t.Fatal(err)
		}
		out := map[int64]hit{}
		for _, r := range sr.Results {
			if r.InFixtureLibrary == nil {
				t.Fatalf("inFixtureLibrary missing: %s", rr.Body.String())
			}
			out[r.RID] = r
		}
		return out
	}
	before := search()
	if *before[101].InFixtureLibrary || *before[201].InFixtureLibrary {
		t.Fatalf("nothing imported yet, but %+v", before)
	}
	// Import rid 101's bytes through the existing path, labelled.
	doc := fmt.Sprintf(`{"mode":"merge","library":{"format":"benny512-fixture-library","schemaVersion":2,"records":[{"manufacturer":"Robe","model":"Spiider","sourceFiles":[{"name":"Robe@Spiider@r1.gdtf","uploaderLabel":"Manufacturer","data":%q}]}]}}`,
		base64.StdEncoding.EncodeToString(f.files[101]))
	if rr := doRaw(t, handler, "POST", "/api/library/import", doc); rr.Code != 200 {
		t.Fatalf("import = %d %s", rr.Code, rr.Body.String())
	}
	after := search()
	if !*after[101].InFixtureLibrary || *after[201].InFixtureLibrary || after[101].SHA256 != fmt.Sprintf("%x", sha256.Sum256(f.files[101])) {
		t.Fatalf("after importing rid 101: %+v", after)
	}
	rr := doJSON(t, handler, "GET", "/api/library", nil)
	if !strings.Contains(rr.Body.String(), `"uploaderLabel":"Manufacturer"`) {
		t.Fatalf("the imported record lost its label: %s", rr.Body.String())
	}
	// Share search: a listed entry not downloaded is never "in the library".
	rr = doJSON(t, handler, "GET", "/api/gdtf/search?in=share&q=sharpy", nil)
	if !strings.Contains(rr.Body.String(), `"inFixtureLibrary":false`) {
		t.Fatalf("share search = %s", rr.Body.String())
	}

	// Match candidates carry it too (rid 101 with an RDM block, as in
	// TestGDTFMatchEndpoint).
	h2 := newHarness(t)
	t.Cleanup(h2.srv.Close)
	f2 := newWebFakeShare(t)
	f2.files[101] = rdmGDTF(t)
	useFakeGDTF(t, h2, f2, t.TempDir())
	hd := h2.srv.Handler()
	doJSON(t, hd, "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword})
	doJSON(t, hd, "POST", "/api/gdtf/manufacturers", map[string][]string{"manufacturers": {"Robe"}})
	if _, err := h2.srv.gdtf.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	match := func() string {
		return doJSON(t, hd, "GET", "/api/gdtf/match?manufacturerId=0x22A6&deviceModelId=0x1093", nil).Body.String()
	}
	if m := match(); !strings.Contains(m, `"inFixtureLibrary":false`) {
		t.Fatalf("match before import = %s", m)
	}
	doc = fmt.Sprintf(`{"mode":"merge","library":{"format":"benny512-fixture-library","schemaVersion":2,"records":[{"manufacturer":"Robe","model":"Spiider","sourceFiles":[{"name":"Robe@Spiider@r1.gdtf","data":%q}]}]}}`,
		base64.StdEncoding.EncodeToString(f2.files[101]))
	if rr := doRaw(t, hd, "POST", "/api/library/import", doc); rr.Code != 200 {
		t.Fatalf("import = %d %s", rr.Code, rr.Body.String())
	}
	if m := match(); !strings.Contains(m, `"inFixtureLibrary":true`) {
		t.Fatalf("match after import = %s", m)
	}
}

// TestG3b1LibraryImportTakes20MBGDTF: POST /api/library/import (the
// existing path every GDTF import uses) accepts a GDTF of exactly 20 MB and
// refuses one byte more, saying 20 MB.
func TestG3b1LibraryImportTakes20MBGDTF(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	body := func(n int) string {
		return fmt.Sprintf(`{"mode":"merge","library":{"format":"benny512-fixture-library","schemaVersion":2,"records":[{"manufacturer":"QA","model":"Big%d","sourceFiles":[{"name":"big.gdtf","data":%q}]}]}}`,
			n, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, n)))
	}
	if rr := doRaw(t, h.srv.Handler(), "POST", "/api/library/import", body(20<<20)); rr.Code != 200 {
		t.Fatalf("20 MB GDTF import = %d %.300s", rr.Code, rr.Body.String())
	}
	rr := doRaw(t, h.srv.Handler(), "POST", "/api/library/import", body(20<<20+1))
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "20 MB") {
		t.Fatalf("over 20 MB = %d %.300s", rr.Code, rr.Body.String())
	}
}

func rdmGDTF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("description.xml")
	_, _ = w.Write([]byte(`<GDTF DataVersion="1.2"><FixtureType Name="Spiider" LongName="Robe Spiider" Manufacturer="Robe">
<DMXModes><DMXMode Name="Standard"/></DMXModes>
<Protocols><FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093"/></Protocols></FixtureType></GDTF>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
