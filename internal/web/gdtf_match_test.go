package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"benny512/internal/gdtfshare"
)

// matchJSON decodes GET /api/gdtf/match independently of the handler's
// own response type, so the test checks the wire, not the struct.
type matchJSON struct {
	Query struct {
		ManufacturerID    *uint16 `json:"manufacturerId"`
		DeviceModelID     *uint16 `json:"deviceModelId"`
		SoftwareVersionID *uint32 `json:"softwareVersionId"`
		Personality       *uint8  `json:"personality"`
	} `json:"query"`
	Count      *int                       `json:"count"`
	Candidates []gdtfshare.MatchCandidate `json:"candidates"`
	NotYetRead *int                       `json:"notYetRead"`
	Unreadable *int                       `json:"unreadable"`
	Note       string                     `json:"note"`
}

func TestGDTFMatchEndpoint(t *testing.T) {
	f := newWebFakeShare(t)
	// rid 101 (Robe Spiider, Manufacturer upload) carries an RDM block.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("description.xml")
	_, _ = w.Write([]byte(`<GDTF DataVersion="1.2"><FixtureType Name="Spiider" LongName="Robe Spiider" Manufacturer="Robe">
<DMXModes><DMXMode Name="Standard"/><DMXMode Name="Extended"/></DMXModes>
<Protocols><FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093">
<SoftwareVersionID Value="0x00020000"><DMXPersonality Value="0x1" DMXMode="Extended"/></SoftwareVersionID>
</FTRDM></Protocols></FixtureType></GDTF>`))
	zw.Close()
	f.files[101] = buf.Bytes()

	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()
	get := func(q string) (int, matchJSON, string) {
		t.Helper()
		rr := doJSON(t, handler, "GET", "/api/gdtf/match?"+q, nil)
		var m matchJSON
		if rr.Code == http.StatusOK {
			if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
				t.Fatalf("%s: %v %s", q, err, rr.Body.String())
			}
		}
		return rr.Code, m, rr.Body.String()
	}

	// Disabled (no library folder): 503, as the other GDTF endpoints.
	if code, _, body := get("manufacturerId=0x22A6&deviceModelId=0x1093"); code != http.StatusServiceUnavailable {
		t.Fatalf("disabled = %d %s", code, body)
	}

	useFakeGDTF(t, h, f, t.TempDir())
	doJSON(t, handler, "POST", "/api/gdtf/login", map[string]string{"user": "dom", "password": webFakePassword})
	doJSON(t, handler, "POST", "/api/gdtf/manufacturers", map[string][]string{"manufacturers": {"Robe", "GLP"}})
	if _, err := h.srv.gdtf.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	code, m, body := get("manufacturerId=0x22A6&deviceModelId=0x1093&softwareVersionId=0x00020000&personality=1")
	if code != http.StatusOK || m.Count == nil || *m.Count != 1 || len(m.Candidates) != 1 {
		t.Fatalf("match = %d %s", code, body)
	}
	c := m.Candidates[0]
	if c.RID != 101 || c.UploaderLabel != "Manufacturer" || c.MatchedOn != "software-version" || c.ModeState != "found" || c.DMXMode != "Extended" {
		t.Fatalf("candidate = %+v", c)
	}
	if m.Query.ManufacturerID == nil || *m.Query.ManufacturerID != 0x22A6 || m.Query.SoftwareVersionID == nil ||
		*m.Query.SoftwareVersionID != 0x20000 || m.Query.Personality == nil || *m.Query.Personality != 1 {
		t.Fatalf("query echo = %s", body)
	}
	if m.NotYetRead == nil || m.Unreadable == nil {
		t.Fatalf("counts must always be present: %s", body)
	}

	// Decimal works too (the Devices JSON carries the IDs as numbers):
	// 0x22A6 = 8870, 0x1093 = 4243. Optional values absent -> null echo.
	code, m, body = get("manufacturerId=8870&deviceModelId=4243")
	if code != http.StatusOK || len(m.Candidates) != 1 || m.Candidates[0].MatchedOn != "model" || m.Candidates[0].ModeState != "not-asked" {
		t.Fatalf("decimal = %d %s", code, body)
	}
	if !strings.Contains(body, `"softwareVersionId":null`) || !strings.Contains(body, `"personality":null`) {
		t.Fatalf("unknown optional values echo as null: %s", body)
	}

	// No RDM match: 200 with an empty list, not a name guess.
	code, _, body = get("manufacturerId=0x22A6&deviceModelId=0x7777")
	if code != http.StatusOK || !strings.Contains(body, `"count":0`) || !strings.Contains(body, `"candidates":[]`) {
		t.Fatalf("no match = %d %s", code, body)
	}

	// Bad query values: 400, naming the parameter.
	for q, param := range map[string]string{
		"deviceModelId=0x1093":                                           "manufacturerId",
		"manufacturerId=0x22A6":                                          "deviceModelId",
		"manufacturerId=0x10000&deviceModelId=1":                         "manufacturerId",
		"manufacturerId=-1&deviceModelId=1":                              "manufacturerId",
		"manufacturerId=0x&deviceModelId=1":                              "manufacturerId",
		"manufacturerId=1&deviceModelId=abc":                             "deviceModelId",
		"manufacturerId=1&deviceModelId=1&softwareVersionId=0x100000000": "softwareVersionId",
		"manufacturerId=1&deviceModelId=1&softwareVersionId=x":           "softwareVersionId",
		"manufacturerId=1&deviceModelId=1&personality=0":                 "personality",
		"manufacturerId=1&deviceModelId=1&personality=256":               "personality",
		"manufacturerId=1&deviceModelId=1&personality=":                  "personality",
	} {
		code, _, body := get(q)
		if code != http.StatusBadRequest || !strings.Contains(body, param) {
			t.Fatalf("%s = %d %s, want 400 naming %s", q, code, body, param)
		}
	}
}
