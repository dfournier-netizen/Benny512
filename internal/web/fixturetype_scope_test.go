package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Literal wire objects deliberately avoid sharing request/status structs with
// the implementation: this is the browser contract, not a struct round-trip.
func TestFixtureTypeScopeWireContract(t *testing.T) {
	h := newHarness(t)
	for i, typ := range []string{"Martin ERA 800 Performance", "GLP JDC1", " Martin ERA 800 Performance ", "", "Martin ERA 800 Performance Plus"} {
		e := jdcLikeEntryRequest("Fixture", 0, uint16(1+i*20))
		e.FixtureType = typ
		if i == 2 {
			e.Mode = "Different mode"
			e.Universe = 1
		}
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", e)
		if rr.Code != http.StatusOK {
			t.Fatalf("seed fixture: %s", rr.Body.String())
		}
	}
	decode := func(body []byte) map[string]json.RawMessage {
		t.Helper()
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatal(err)
		}
		return obj
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	obj := decode(rr.Body.Bytes())
	var opts []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(obj["fixtureTypes"], &opts); err != nil {
		t.Fatalf("status must expose fixtureTypes options: %v; body=%s", err, rr.Body.String())
	}
	if len(opts) != 3 || opts[0].Key != "GLP JDC1" || opts[1].Key != "Martin ERA 800 Performance" || opts[1].Label != opts[1].Key || opts[1].Count != 2 {
		t.Fatalf("type options must group exact trimmed type across modes and omit blanks: %+v", opts)
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", map[string]any{
		"scopeKind": "fixtureType", "fixtureType": "Martin ERA 800 Performance",
		"tests": []any{map[string]any{"kind": "dimmer_sine", "rateHz": 1, "max": 255}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("fixtureType scope rejected: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.TotalScope != 2 || st.SelectedCount != 1 || st.OutputEnabled {
		t.Fatalf("type selection must select only the two exact matches without starting output: %+v", st)
	}
	obj = decode(rr.Body.Bytes())
	if string(obj["scopeFixtureType"]) != `"Martin ERA 800 Performance"` || string(obj["scopeKind"]) != `"fixtureType"` {
		t.Fatalf("missing accepted scope echo: %s", rr.Body.String())
	}
	if err := json.Unmarshal(obj["fixtureTypes"], &opts); err != nil || len(opts) != 3 {
		t.Fatalf("options narrowed to active scope: %s", rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", map[string]any{"enabled": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("start fake output: %s", rr.Body.String())
	}
	for _, key := range []string{"", " ", "Stale deleted type", "martin era 800 performance"} {
		rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/scope", map[string]any{"scopeKind": "fixtureType", "fixtureType": key})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid type %q must fail closed: status=%d body=%s", key, rr.Code, rr.Body.String())
		}
		rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
		mustUnmarshal(t, rr, &st)
		obj = decode(rr.Body.Bytes())
		if st.TotalScope != 2 || !st.OutputEnabled || st.SelectedCount != 1 || string(obj["scopeFixtureType"]) != `"Martin ERA 800 Performance"` {
			t.Fatalf("rejected type changed existing scope/output: %s", rr.Body.String())
		}
	}
}

func TestFixtureTypeOptionsEmptyArray(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	var obj map[string]json.RawMessage
	mustUnmarshal(t, rr, &obj)
	if string(obj["fixtureTypes"]) != "[]" || string(obj["scopeFixtureType"]) != `""` {
		t.Fatalf("empty fixture types and scope key must be explicit: %s", rr.Body.String())
	}
}
