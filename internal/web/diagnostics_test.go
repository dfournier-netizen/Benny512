package web

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// handleRDMDiagnostics carries a promise in its own doc comment —
// "Meaningful zeros stay explicit: zero reissues is the healthy result and
// must never disappear from the wire" — and until this file nothing checked
// it. A comment is not a test, and on this project that specific promise has
// been broken ten-plus times: `omitempty` on a numeric field whose zero is
// real data, after which the browser reads `undefined` and renders it.
//
// The diagnostics payload is the worst possible place for that failure,
// because ZERO IS THE ANSWER AN OPERATOR WANTS. "reissued 0" means the line
// is healthy. If that key vanishes, the status line reads "reissued
// undefined" — and the reading a tech would take from it is that the app is
// broken, on the exact screen they opened to find out whether the RIG is.

// TestRDMDiagnosticsJSON_RemainingMeaningfulZeroesSurvive covers the four
// counters TestRDMDiagnosticsKeepsMeaningfulZeroCounters (patch_test.go)
// does not. That test guards collects/collectHits/reissues through the real
// endpoint; these four were left unprotected, and `ackTimerCollectTimeouts`
// in particular is the one whose zero an operator most wants to trust —
// "zero collect timeouts" is the difference between a busy line and a
// responder that has stopped answering.
//
// Asserted on the marshalled BYTES, not the struct: a struct field reading 0
// is precisely what an omitted key unmarshals to, so a field-level assertion
// passes vacuously against this exact bug.
func TestRDMDiagnosticsJSON_RemainingMeaningfulZeroesSurvive(t *testing.T) {
	b, err := json.Marshal(rdmDiagnosticsJSON{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	for _, want := range []string{
		`"ackTimerCollectTimeouts":0`,
		`"ackTimerCollectors":0`,
		`"proxyBufferFull":0`,
		`"queuedMessagesDrained":0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics JSON is missing %s — a healthy zero was erased, and the "+
				"Analyzer status line will render \"undefined\" where an operator expects a count\ngot: %s",
				want, got)
		}
	}
}

// TestRDMDiagnosticsJSON_MatchesTheKeysAnalyzerReads is the boundary test.
//
// The Go struct spells one counter `AckTimerTimeouts` and puts it on the
// wire as `ackTimerCollectTimeouts` — the field name and the JSON key
// genuinely differ, which is legal and fine, and is also exactly the shape
// of this project's most-repeated defect: the same value spelled two ways in
// two files. Nothing but a test that reads BOTH SIDES can catch a rename on
// either one.
//
// So this reads the literal analyzer.js the browser loads, extracts every
// property it pulls off the diagnostics object, and requires each to be a
// key the handler actually emits. It fails if Go renames a JSON tag, and it
// fails if the JS reaches for a counter that was never on the wire.
func TestRDMDiagnosticsJSON_MatchesTheKeysAnalyzerReads(t *testing.T) {
	b, err := json.Marshal(rdmDiagnosticsJSON{})
	if err != nil {
		t.Fatal(err)
	}
	var emitted map[string]any
	if err := json.Unmarshal(b, &emitted); err != nil {
		t.Fatal(err)
	}

	src, err := os.ReadFile("static/js/analyzer.js")
	if err != nil {
		t.Fatalf("reading the literal analyzer.js the browser loads: %v", err)
	}

	// analyzer.js aliases the payload as `const d = rdmDiagnostics;` inside
	// the status-line block. `d` is a common local name elsewhere in the file
	// (capture rows use it too), so the search is SCOPED to that block rather
	// than run over the whole file — an unscoped match pulls in d.pid,
	// d.destUid and friends from the packet list and reports them as missing
	// diagnostics keys, which would be a false alarm this project has already
	// spent time on once.
	js := string(src)
	const alias = "const d = rdmDiagnostics;"
	start := strings.Index(js, alias)
	if start < 0 {
		t.Fatalf("could not find %q in analyzer.js — the status line was restructured; "+
			"re-scope this test rather than deleting it, the Go/JS key boundary it "+
			"guards is this project's most-repeated defect", alias)
	}
	block := js[start:]
	if end := strings.Index(block, "renderRDM("); end > 0 {
		block = block[:end]
	}

	re := regexp.MustCompile(`\bd\.([A-Za-z_][A-Za-z0-9_]*)`)
	matches := re.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		t.Fatal("found no diagnostics property reads in analyzer.js's status block — either " +
			"the status line stopped reporting controller health, or this test's pattern " +
			"went stale; both are worth a look rather than a silent pass")
	}

	seen := map[string]bool{}
	var read []string
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		read = append(read, name)
	}
	sort.Strings(read)

	for _, name := range read {
		if _, ok := emitted[name]; !ok {
			keys := make([]string, 0, len(emitted))
			for k := range emitted {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Errorf("analyzer.js reads d.%s, which the diagnostics handler never emits — "+
				"the status line will render \"undefined\".\nemitted keys: %v", name, keys)
		}
	}
	t.Logf("analyzer.js reads %d diagnostics counters, all emitted: %v", len(read), read)
}

// TestRDMDiagnosticsJSON_SurfacesTheFailureSignal pins a product decision
// rather than a wire format, because the counter that matters most was the
// one being fetched and thrown away.
//
// The status line reported collected/attempted and reissues. A reissue on
// its own is ambiguous — it can be ordinary policy. A reissue that follows a
// COLLECT TIMEOUT is the expensive failure path, and an operator staring at
// "reissued 12" cannot tell a busy line from a responder that has stopped
// answering. `ackTimerCollectTimeouts` is the counter that disambiguates it,
// and `proxyBufferFull` is the one that says the problem is the proxy rather
// than the fixture — both were on the wire and neither reached the screen.
func TestRDMDiagnosticsJSON_SurfacesTheFailureSignal(t *testing.T) {
	src, err := os.ReadFile("static/js/analyzer.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)

	for _, c := range []struct{ key, why string }{
		{"ackTimerCollectTimeouts", "distinguishes a policy reissue from a responder that stopped answering"},
		{"proxyBufferFull", "says the bottleneck is the proxy, not the fixture"},
	} {
		if !strings.Contains(js, c.key) {
			t.Errorf("analyzer.js never reads %s — it is on the wire and not on the screen; "+
				"it %s", c.key, c.why)
		}
	}
}
