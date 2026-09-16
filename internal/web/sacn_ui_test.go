package web

import (
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// This file is the Go<->JS seam for selectable sACN output. Every test here
// reads the LITERAL file the browser loads, the way universe_scheme_test.go
// and rigcheck_scope_test.go already do, because the defects this class of
// test catches are the ones neither half can see alone: the JS is internally
// consistent, the Go is internally consistent, and the two disagree about a
// string.

func readJS(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("static/js/" + name)
	if err != nil {
		t.Fatalf("reading the literal %s the browser loads: %v", name, err)
	}
	return string(b)
}

// TestRigCheckProtocolUI runs rigcheck_protocol_test.js against the real
// ui.js/rigcheck.js/reconcile.js/patch.js: the numbering helpers, the
// Apply-to-confirm contract on the protocol control, the wire vocabulary the
// start body carries, and the 422 surfacing.
func TestRigCheckProtocolUI(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping rig check protocol UI test")
	}
	out, err := exec.Command(nodePath, "static/js/testdata/rigcheck_protocol_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("rigcheck_protocol_test.js failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// TestSACNSettingsFormUI runs settings_sacn_test.js against the real
// ui.js/settings.js: staged-not-live dirty tracking, the worked example, the
// two-document save, and the server's own refusal reaching the screen.
func TestSACNSettingsFormUI(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping sACN settings form test")
	}
	out, err := exec.Command(nodePath, "static/js/testdata/settings_sacn_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("settings_sacn_test.js failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// protocolIDsFromJS pulls the protocol vocabulary out of patch.js's own
// RC_PROTOCOLS table. It parses the TABLE rather than grepping the file for
// the two words, so a stray "sacn" in a comment cannot make this test pass
// while the control offers something else.
func protocolIDsFromJS(t *testing.T, src string) []string {
	t.Helper()
	start := strings.Index(src, "const RC_PROTOCOLS = [")
	if start < 0 {
		t.Fatal("patch.js has no RC_PROTOCOLS table — the protocol vocabulary the Rig Check page sends must be declared in one place a test can read")
	}
	rest := src[start:]
	end := strings.Index(rest, "];")
	if end < 0 {
		t.Fatal("patch.js's RC_PROTOCOLS table is not closed with `];`")
	}
	var ids []string
	for _, m := range regexp.MustCompile(`id:\s*'([^']*)'`).FindAllStringSubmatch(rest[:end], -1) {
		ids = append(ids, m[1])
	}
	sort.Strings(ids)
	return ids
}

// TestProtocolVocabularyMatchesServer is the boundary test. The protocol
// strings the browser puts in POST /api/patch/rigcheck/start must be exactly
// the ones patch.NormalizeProtocol accepts — in BOTH directions, so a rename
// on either side fails here rather than on a rig.
//
// The failure it exists for is silent: NormalizeProtocol answers 400 on an
// unknown value and never falls back, so a renamed JS constant does not
// quietly send Art-Net — but it does mean the sACN button simply stops
// working, with an error a tech will read as "sACN is broken".
func TestProtocolVocabularyMatchesServer(t *testing.T) {
	src := readJS(t, "patch.js")
	ids := protocolIDsFromJS(t, src)

	want := []string{string(patch.ProtocolArtNet), string(patch.ProtocolSACN)}
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("patch.js offers protocols %v; the server accepts %v — these must be the same set", ids, want)
	}

	// Every id the JS offers must normalize to ITSELF. A value that
	// normalizes to something else would mean the button says one thing and
	// the wire carries another.
	for _, id := range ids {
		got, err := patch.NormalizeProtocol(id)
		if err != nil {
			t.Errorf("patch.js offers protocol %q, which the server rejects: %v", id, err)
			continue
		}
		if string(got) != id {
			t.Errorf("patch.js's %q normalizes to %q on the server", id, got)
		}
	}

	// And the start body must actually carry the field. Absent means Art-Net
	// server-side, which is the backwards-compatibility contract — but this
	// screen has a protocol control, so it must say what it means.
	if !regexp.MustCompile(`protocol:`).MatchString(src) {
		t.Error("patch.js never sets a `protocol:` key — the Rig Check start body must state the protocol explicitly now that the screen offers a choice")
	}

	// A value neither side knows must be refused, not defaulted.
	if _, err := patch.NormalizeProtocol("sACN"); err == nil {
		t.Error("NormalizeProtocol accepted the mis-cased \"sACN\"; the JS vocabulary is lowercase and a near-miss must be refused, never defaulted")
	}
}

// TestSACNConfigKeysMatchServer pins the GET/POST /api/sacn wire shape to
// what the sACN settings form sends. The decoder uses DisallowUnknownFields,
// so a key the Go struct does not name is a 400 rather than a silent no-op —
// and the CID is server-owned and must never appear in the form at all.
func TestSACNConfigKeysMatchServer(t *testing.T) {
	var goKeys []string
	rt := reflect.TypeOf(sacnConfigJSON{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			goKeys = append(goKeys, tag)
		}
	}
	sort.Strings(goKeys)

	src := readJS(t, "settings.js")
	var jsKeys []string
	start := strings.Index(src, "function sacnPayload(")
	if start < 0 {
		t.Fatal("settings.js has no sacnPayload() — the POST /api/sacn body must be built in one place a test can read")
	}
	rest := src[start:]
	end := strings.Index(rest, "\n  }")
	if end < 0 {
		end = len(rest)
	}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9]*):`).FindAllStringSubmatch(rest[:end], -1) {
		jsKeys = append(jsKeys, m[1])
	}
	sort.Strings(jsKeys)

	if !reflect.DeepEqual(jsKeys, goKeys) {
		t.Errorf("settings.js sends %v to POST /api/sacn; the server's sacnConfigJSON names %v. "+
			"decodeJSON sets DisallowUnknownFields, so any extra key is a flat 400", jsKeys, goKeys)
	}

	// The CID is generated server-side and persisted; a receiver tells
	// sources apart by it (ANSI E1.31-2025 Section 6.2.3). A form field for
	// it would let a browser impersonate another source, and sending one is
	// a 400 anyway.
	for _, name := range []string{"settings.js", "patch.js", "api.js"} {
		body := readJS(t, name)
		if regexp.MustCompile(`(?i)\bcid\b\s*:`).MatchString(body) {
			t.Errorf("%s has a cid field; the CID is server-owned and never travels in either direction over /api/sacn", name)
		}
	}
}

// TestSACNUniverseDisplayGoesThroughTheHelpers is the numbering-discipline
// check for the new screens, mirroring TestEachScreenUsesItsOwnNumbering's
// reason for existing: one ambiguous formatter, applied by hand at a call
// site, is what shipped a universe off-by-one here before.
//
// So no screen may do the Art-Net -> sACN arithmetic or the Table 9-10
// address derivation inline. Both live in ui.js, named for what they RETURN.
func TestSACNUniverseDisplayGoesThroughTheHelpers(t *testing.T) {
	ui := readJS(t, "ui.js")
	for _, fn := range []string{"artnetToSacn", "formatSacn", "sacnMulticastAddress", "setSacnStart", "getSacnStart"} {
		if !regexp.MustCompile(`function\s+` + fn + `\s*\(`).MatchString(ui) {
			t.Errorf("ui.js has no %s — the sACN numbering must live beside formatUser/formatArtnet, not in a screen", fn)
		}
	}
	// The name has to say which numbering it produces. A bare
	// "formatUniverse" is exactly the defect the September 9 rework removed.
	if regexp.MustCompile(`function\s+formatUniverse\s*\(`).MatchString(ui) {
		t.Error("ui.js re-introduced a bare formatUniverse")
	}

	for _, name := range []string{"patch.js", "settings.js", "rigcheck.js"} {
		src := readJS(t, name)
		if strings.Contains(src, "239.255.") && !strings.Contains(src, "UI.sacnMulticastAddress") {
			t.Errorf("%s writes a 239.255 multicast address without going through UI.sacnMulticastAddress — "+
				"ANSI E1.31-2025 Table 9-10 is derived in exactly one place", name)
		}
		// The mapping is `sacnStart + showUniverse - 1`, and getting it wrong
		// lights the wrong universe. If a screen mentions an sACN start it
		// must be asking ui.js, not doing the sum.
		if regexp.MustCompile(`sacnStart\s*\+`).MatchString(src) {
			t.Errorf("%s computes an sACN universe inline; call UI.artnetToSacn/UI.formatSacn instead", name)
		}
	}
}

// TestRigCheckProtocolControlIsApplyToConfirm pins the decision, not just
// the wiring: the protocol choice is NOT one of the signed-off direct-action
// exceptions (Identify, the Send/Rig Check level faders, the Function-check
// test toggles). Those are all adjustments to something already live, made
// by feel while watching the rig. A protocol is what the next run puts on
// the wire, and switching it while output flows terminates one stream and
// opens another — so it stages, and Apply commits.
func TestRigCheckProtocolControlIsApplyToConfirm(t *testing.T) {
	src := readJS(t, "patch.js")
	if !strings.Contains(src, "rcProtocolDraft") {
		t.Error("patch.js has no rcProtocolDraft — a protocol press must stage a draft, never send")
	}
	if !strings.Contains(src, "rcProtocolApply") {
		t.Error("patch.js has no rcProtocolApply control — Apply is what commits the protocol")
	}
	// The press handler itself must not send. Everything between the
	// data-rc-protocol wiring and the end of that handler is local state.
	i := strings.Index(src, "data-rc-protocol]")
	if i < 0 {
		t.Fatal("patch.js never wires [data-rc-protocol]")
	}
	handler := src[i:]
	if j := strings.Index(handler, "}));"); j > 0 {
		handler = handler[:j]
	}
	for _, sent := range []string{"Api.rigCheckStart", "Api.rigCheckStop", "Api.postSACNConfig"} {
		if strings.Contains(handler, sent) {
			t.Errorf("the [data-rc-protocol] press handler calls %s — picking a protocol must send nothing; Apply does that", sent)
		}
	}
}
