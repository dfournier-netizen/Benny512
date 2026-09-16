package web

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func readGo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

func mustFloat(t *testing.T, s, what string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%s is not a number: %q", what, s)
	}
	return v
}

// nearly compares rates written as decimal literals; exact float equality on
// values like 0.005 is a coin toss.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Rate-slider bounds live in the browser, so this is a source-reading guard in
// the family of TestUniverseScheme rather than a behavioural test: it asserts
// what rigcheck.js DECLARES and how the Rate row is wired, not what a rendered
// slider looks like. Said plainly so nobody reads more into a green run than
// it earns -- the rendered control is covered by the Playwright pass.
//
// What it does catch is the failure mode this change invites: someone retunes
// the shared bounds later and leaves Ballyhoo's override stranded at numbers
// that no longer bear the relationship Dom asked for.

var (
	reDefaultRateBounds = regexp.MustCompile(`DEFAULT_RATE_BOUNDS\s*=\s*\{\s*min:\s*([0-9.]+),\s*max:\s*([0-9.]+),\s*step:\s*([0-9.]+)\s*\}`)
	reBallyhooBounds    = regexp.MustCompile(`ballyhoo:\s*\{\s*min:\s*([0-9.]+),\s*max:\s*([0-9.]+),\s*step:\s*([0-9.]+)\s*\}`)
)

// TestBallyhooRateBounds pins Dom's instruction after the September gig: the
// Ballyhoo sine was far too fast at the slider's minimum, so its low end drops
// by a factor of ten and its high end halves. Expressed here as the RATIO to
// the shared bounds, because that is the form the instruction took -- a later
// retune of the shared slider must re-derive Ballyhoo's, not silently diverge.
func TestBallyhooRateBounds(t *testing.T) {
	js := readJS(t, "rigcheck.js")

	dm := reDefaultRateBounds.FindStringSubmatch(js)
	if dm == nil {
		t.Fatal("rigcheck.js declares no DEFAULT_RATE_BOUNDS — the shared Rate slider range must be in one place a test can read")
	}
	bm := reBallyhooBounds.FindStringSubmatch(js)
	if bm == nil {
		t.Fatal("rigcheck.js declares no per-kind ballyhoo rate bounds — Ballyhoo must override the shared range, not share it")
	}

	dMin, dMax := mustFloat(t, dm[1], "default min"), mustFloat(t, dm[2], "default max")
	bMin, bMax, bStep := mustFloat(t, bm[1], "ballyhoo min"), mustFloat(t, bm[2], "ballyhoo max"), mustFloat(t, bm[3], "ballyhoo step")

	if got, want := bMin, dMin/10; !nearly(got, want) {
		t.Errorf("ballyhoo minimum rate = %v Hz, want %v Hz — a tenth of the shared minimum %v, per Dom: \"slow the lowest value down by 10 fold\"", got, want, dMin)
	}
	if got, want := bMax, dMax/2; !nearly(got, want) {
		t.Errorf("ballyhoo maximum rate = %v Hz, want %v Hz — half the shared maximum %v, per Dom: \"make the max half of what it currently is\"", got, want, dMax)
	}

	// A control whose smallest increment is coarser than its own minimum
	// cannot reach its low end at all: with a 0.05 step the first stop above
	// zero is 0.05, i.e. ten times the 0.005 minimum, and the new slow end
	// would be unreachable while looking present.
	if bStep > bMin {
		t.Errorf("ballyhoo rate step = %v Hz is coarser than its %v Hz minimum: the slow end would be unreachable", bStep, bMin)
	}

	// The default 0.25 Hz stays put. Dom asked for the BOUNDS to move, not
	// every Ballyhoo run to start somewhere new.
	if !regexp.MustCompile(`return 0\.25`).MatchString(readGo(t, "../patch/testpattern.go")) {
		t.Error("testpattern.go's defaultRateForKind no longer returns 0.25 for continuous patterns; Ballyhoo's default rate was to stay unchanged")
	}

	// And the Rate row must actually use the per-kind bounds rather than the
	// literals it used before.
	if !regexp.MustCompile(`slider\(t\.id, 'rateHz', 'Rate', t\.rateHz, rb\.min, rb\.max, rb\.step`).MatchString(js) {
		t.Error("the Rate slider is not wired to rateBoundsFor(kind) — a per-kind table nothing reads is worse than no table")
	}
}

// TestSharedRateBoundsUnchangedForOtherKinds: the instruction was explicitly
// Ballyhoo-specific. Every other rate-bearing test keeps the range it was
// tuned with, so the override table must hold exactly one entry.
func TestSharedRateBoundsUnchangedForOtherKinds(t *testing.T) {
	js := readJS(t, "rigcheck.js")

	dm := reDefaultRateBounds.FindStringSubmatch(js)
	if dm == nil {
		t.Fatal("rigcheck.js declares no DEFAULT_RATE_BOUNDS")
	}
	if dm[1] != "0.05" || dm[2] != "5" || dm[3] != "0.05" {
		t.Errorf("shared rate bounds = min %s, max %s, step %s; want the untouched 0.05/5/0.05 — only Ballyhoo was to change", dm[1], dm[2], dm[3])
	}

	table := regexp.MustCompile(`(?s)const RATE_BOUNDS = \{(.*?)\};`).FindStringSubmatch(js)
	if table == nil {
		t.Fatal("rigcheck.js declares no RATE_BOUNDS table")
	}
	kinds := regexp.MustCompile(`(?m)^\s*([a-z_]+):\s*\{`).FindAllStringSubmatch(table[1], -1)
	if len(kinds) != 1 || kinds[0][1] != "ballyhoo" {
		var got []string
		for _, k := range kinds {
			got = append(got, k[1])
		}
		t.Errorf("RATE_BOUNDS overrides %v; want exactly [ballyhoo] — no other kind's slider was to move", got)
	}
}
