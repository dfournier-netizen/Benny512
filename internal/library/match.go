package library

import (
	"strings"

	"benny512/internal/patch"
)

// This file implements the library's TOLERANT fixture-type matching — the
// thing that makes "JDC 1", "GLP JDC-1" and "GLP JDC1 Strobe" all find the
// same library record.
//
// It does NOT own a normaliser. internal/patch owns this project's one
// fixture-name normaliser (patch.NormalizeTokens: case-, punctuation- and
// whitespace-insensitive, splitting at every letter/digit boundary so
// "JDC1" tokenizes identically to "JDC 1") together with its containment
// comparison (patch.TokenContainment), and this file calls both directly.
// Writing a second normaliser here would guarantee the two drift apart on
// exactly the inputs that matter — the failure mode this project has
// already suffered once, with patch.js's fuzzyTokenSet being a hand-port of
// the same function for the browser.
//
// HISTORY, so it is not re-introduced. Those two helpers used to be
// unexported, and this file reached them by calling patch.Reconcile with a
// synthetic one-entry/one-device pair and inferring containment from the
// returned score against patch's 0.25 proposal threshold. That worked only
// because the model weight and the threshold happened to be the same
// number: retuning either — a thing internal/patch is entitled to do
// without ever looking at this package — would have silently degraded the
// library to "nothing ever matches", with no test failing. The helpers are
// now exported (see internal/patch/match.go's note above NormalizeTokens)
// and the inference is gone. TestTokensContainedIn_TracksPatchNormalizer
// still pins the behaviour against patch's normaliser so a change to the
// TOKENIZER itself — which would legitimately change what the library
// considers the same fixture type — fails loudly here.

// fullContainment is the containment ratio that means "every token of ref
// appears in text". TokenContainment returns hit/len(ref), so a full hit is
// exactly 1.0 in float terms; the epsilon is belt-and-braces against a
// future implementation that computes the ratio some other way.
const fullContainment = 1.0
const containmentEpsilon = 1e-9

// tokensContainedIn reports whether every normalised token of ref also
// appears in text, using internal/patch's normaliser. An empty/whitespace-
// only ref is never contained in anything — a record with no model name
// must not silently match every query. (patch.TokenContainment already
// returns 0 for an empty ref set, so the guard below is belt-and-braces and
// documentation rather than the load-bearing check.)
func tokensContainedIn(ref, text string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	return patch.TokenContainment(patch.NormalizeTokens(text), patch.NormalizeTokens(ref)) >= fullContainment-containmentEpsilon
}

// Matches reports whether the record describes the fixture type named by
// manufacturer+model, tolerantly.
//
// The rule, in words: the two agree on MODEL if either side's model tokens
// are wholly contained in the other side's full "manufacturer model" text,
// and they do not DISAGREE on manufacturer (a manufacturer stated on both
// sides must be contained one way or the other; a manufacturer stated on
// only one side is not evidence against a match, since "JDC 1" scrawled on
// a patch sheet omits "GLP" and must still find the GLP record).
//
// Containment is checked against the other side's FULL text, not just its
// model, precisely so that a query of "GLP JDC1 Strobe" — where the
// manufacturer has been folded into what someone typed as the model —
// still matches a record whose model is "JDC-1".
//
// It is deliberately NOT enough for the two to merely share a token:
// "Martin MAC Aura" and "Martin MAC Viper" share {martin, mac} and must
// not collapse into one library record. See TestMatches_RejectsSiblingModels.
func (r Record) Matches(manufacturer, model string) bool {
	if r.Key == KeyFor(manufacturer, model) {
		return true
	}
	queryText := strings.TrimSpace(manufacturer + " " + model)
	recordText := strings.TrimSpace(r.Manufacturer + " " + r.Model)
	modelAgrees := tokensContainedIn(r.Model, queryText) || tokensContainedIn(model, recordText)
	if !modelAgrees {
		return false
	}
	if strings.TrimSpace(manufacturer) != "" && strings.TrimSpace(r.Manufacturer) != "" {
		if !tokensContainedIn(r.Manufacturer, queryText) && !tokensContainedIn(manufacturer, recordText) {
			return false
		}
	}
	return true
}
