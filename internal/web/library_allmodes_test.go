package web

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestLibraryRemembersAllGdtfModes runs library_allmodes_test.js, which
// exercises the literal mvrimport.js the browser loads.
//
// What it guards: a GDTF describes a fixture's whole personality list, but
// patching applies exactly one mode. `patch.Entry` stores a single mode
// (Mode, Footprint, ChannelFunctions), so the modes not patched exist only
// during the import — harvesting the show afterwards can never recover them.
// Every GDTF entry point therefore writes the FULL list to the library.
//
// Same Node prerequisite and skip-not-fail policy as the other JS suites:
// the code under test is the browser's own file, not a Go port of it.
func TestLibraryRemembersAllGdtfModes(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping library all-modes test")
	}

	cmd := exec.Command(nodePath, "static/js/testdata/library_allmodes_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("library_allmodes_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}

// TestGdtfImportPathsAllReachTheLibrary is the coverage check the JS test
// cannot make: that every place a GDTF ENTERS the app actually calls the
// shared builder.
//
// The JS test proves the builder is correct. It cannot prove a fourth import
// path won't be added next month that quietly keeps only the applied mode —
// and that failure is invisible, because the patch looks right and the loss
// only shows up weeks later when the library turns out not to have the mode
// somebody needed.
//
// So this asserts against the literal screens: the three known entry points
// each reference the shared helpers, and none of them still hand-rolls a
// `format: 'benny512-fixture-library'` document of its own.
func TestGdtfImportPathsAllReachTheLibrary(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("static/js/" + name)
		if err != nil {
			t.Fatalf("reading the literal %s the browser loads: %v", name, err)
		}
		return string(b)
	}

	mvrimport := read("mvrimport.js")
	library := read("library.js")
	patch := read("patch.js")

	// The builder and the best-effort wrapper both live in one place.
	for _, want := range []string{"function libraryDocFromGdtf", "async function rememberGdtfInLibrary"} {
		if !strings.Contains(mvrimport, want) {
			t.Errorf("mvrimport.js is missing %q — the shared GDTF→library path", want)
		}
	}

	// Every screen that takes in a GDTF uses it.
	for _, c := range []struct{ name, src, want, why string }{
		{"library.js", library, "MvrImport.libraryDocFromGdtf",
			"the Fixture Library dialog must build its record with the shared builder, " +
				"not the inline copy it used to carry"},
		{"patch.js (single GDTF)", patch, "MvrImport.rememberGdtfInLibrary",
			"applying a GDTF to patch entries must also remember the fixture type's other modes"},
	} {
		if !strings.Contains(c.src, c.want) {
			t.Errorf("%s does not call %s — %s", c.name, c.want, c.why)
		}
	}

	// The MVR path carries whole GDTF files; those modes must be surfaced
	// and remembered too, not left inside the parse cache.
	if !strings.Contains(mvrimport, "gdtfs") {
		t.Error("parseMvrFile does not surface the GDTFs it resolved, so an MVR import " +
			"cannot put their other modes in the library — the modes are parsed and thrown away")
	}
	if strings.Count(patch, "MvrImport.rememberGdtfInLibrary") < 2 {
		t.Error("patch.js remembers a GDTF in only one of its two import paths; an MVR " +
			"import carries GDTF files too, and their unpatched modes are lost at that moment")
	}

	// And nobody CONSTRUCTS the document shape any more. This is the actual
	// regression: two copies of one shape drifting apart.
	//
	// Matching on construction (`format: 'benny512-fixture-library'`) rather
	// than on any mention of the string, because library.js legitimately
	// VALIDATES it — `pending.doc.format !== 'benny512-fixture-library'` is
	// how it refuses a patch JSON handed to the library importer, and
	// deleting that check to satisfy a test would be a real regression.
	construct := regexp.MustCompile(`format\s*:\s*['"]benny512-fixture-library`)
	for _, c := range []struct{ name, src string }{
		{"library.js", library},
		{"patch.js", patch},
	} {
		if construct.MatchString(c.src) {
			t.Errorf("%s builds a library document inline. Use MvrImport.libraryDocFromGdtf — "+
				"a second hand-rolled copy is how the two drift, which is this project's "+
				"most-repeated defect", c.name)
		}
	}
	// The builder itself is the one place that may construct it.
	if !construct.MatchString(mvrimport) {
		t.Error("mvrimport.js's libraryDocFromGdtf no longer emits the library format marker")
	}
}
