package patch

import "fmt"

// This file implements patch validation: overlapping channel ranges within
// a universe, footprint running past 512, duplicate fixture numbers, and
// zero/invalid footprints/addresses (task ask, item 1's "collision
// detection"). Findings are structured (severity + entry IDs + channel
// range), not strings, so the UI can render icon+text (never color alone —
// accessibility standing constraint) and so a future rig-check pass could
// refuse to drive a colliding universe without parsing prose.

// Severity classifies how serious a Finding is.
type Severity string

// Severities.
const (
	SeverityError   Severity = "error"   // definitely wrong — e.g. two fixtures overlapping
	SeverityWarning Severity = "warning" // worth a look but not necessarily wrong — e.g. footprint 0
)

// FindingKind identifies what kind of problem a Finding describes.
type FindingKind string

// Finding kinds.
const (
	// KindOverlap: two or more entries in the same universe occupy at least
	// one common channel.
	KindOverlap FindingKind = "overlap"
	// KindOverflow: an entry's channel range runs past DMX512's last slot
	// (512).
	KindOverflow FindingKind = "overflow"
	// KindDuplicateFixtureNumber: two or more entries share the same
	// non-empty FixtureNumber.
	KindDuplicateFixtureNumber FindingKind = "duplicate_fixture_number"
	// KindZeroFootprint: an entry's Footprint is 0. Not necessarily wrong
	// (a splitter/gateway/data device legitimately occupies no DMX
	// channels — see internal/walk.FormatAddressRange's identical
	// tolerance), so this is a Warning, not an Error, and such entries are
	// excluded from overlap/overflow checks entirely (they occupy no
	// channels to collide with).
	KindZeroFootprint FindingKind = "zero_footprint"
	// KindInvalidAddress: StartAddress is 0 or > 512 — not a valid DMX slot
	// at all, distinct from KindOverflow (which is a valid start address
	// whose *range* runs past 512).
	KindInvalidAddress FindingKind = "invalid_address"
)

// Finding is one collision-detection result.
type Finding struct {
	Kind     FindingKind `json:"kind"`
	Severity Severity    `json:"severity"`
	Message  string      `json:"message"`
	// EntryIDs lists every entry this finding concerns (2+ for overlap/
	// duplicate-fixture-number, exactly 1 for overflow/zero-footprint/
	// invalid-address).
	EntryIDs []string `json:"entryIds"`
	// Universe/ChannelStart/ChannelEnd describe the affected channel range,
	// when applicable (zero value for findings that aren't channel-scoped,
	// e.g. KindDuplicateFixtureNumber spans entries that may be in
	// different universes entirely).
	Universe     uint16 `json:"universe,omitempty"`
	ChannelStart uint16 `json:"channelStart,omitempty"`
	ChannelEnd   uint16 `json:"channelEnd,omitempty"`
}

// DetectCollisions validates every entry in p and returns every Finding, in
// a deterministic order (invalid-address / zero-footprint / overflow per
// entry in patch order, then overlaps in patch order, then duplicate
// fixture numbers in patch order) so tests and the UI never see findings
// reshuffle between calls on the same input.
func DetectCollisions(p Patch) []Finding {
	var findings []Finding

	// --- per-entry checks: invalid address, zero footprint, overflow -----
	for _, e := range p.Entries {
		if e.StartAddress == 0 || e.StartAddress > 512 {
			findings = append(findings, Finding{
				Kind: KindInvalidAddress, Severity: SeverityError,
				Message:  fmt.Sprintf("start address %d is not a valid DMX slot (1-512)", e.StartAddress),
				EntryIDs: []string{e.ID}, Universe: e.Universe, ChannelStart: e.StartAddress,
			})
			continue // an invalid start address makes overflow math meaningless
		}
		if e.Footprint == 0 {
			findings = append(findings, Finding{
				Kind: KindZeroFootprint, Severity: SeverityWarning,
				Message:  "footprint is 0 (no DMX channels) — correct for a splitter/gateway/data device, otherwise likely a missing value",
				EntryIDs: []string{e.ID}, Universe: e.Universe, ChannelStart: e.StartAddress,
			})
			continue // zero-footprint entries occupy no channels — nothing to overflow or overlap
		}
		end := e.EndAddress()
		if end > 512 {
			findings = append(findings, Finding{
				Kind: KindOverflow, Severity: SeverityError,
				Message:  fmt.Sprintf("channels %d-%d run past 512 (footprint %d starting at %d)", e.StartAddress, end, e.Footprint, e.StartAddress),
				EntryIDs: []string{e.ID}, Universe: e.Universe, ChannelStart: e.StartAddress, ChannelEnd: uint16(min(end, 65535)),
			})
		}
	}

	// --- overlap: pairwise, within the same universe, both footprint > 0,
	// both a valid start address (invalid-address entries were `continue`d
	// above and never reach here via the slice below). ---
	type ranged struct {
		idx        int
		start, end int
	}
	byUniverse := map[uint16][]ranged{}
	for i, e := range p.Entries {
		if e.Footprint == 0 || e.StartAddress == 0 || e.StartAddress > 512 {
			continue
		}
		byUniverse[e.Universe] = append(byUniverse[e.Universe], ranged{idx: i, start: int(e.StartAddress), end: e.EndAddress()})
	}
	for universe, ranges := range byUniverse {
		for i := 0; i < len(ranges); i++ {
			for j := i + 1; j < len(ranges); j++ {
				a, b := ranges[i], ranges[j]
				// Standard interval-overlap test: two closed intervals
				// [a.start,a.end] and [b.start,b.end] overlap iff
				// a.start <= b.end && b.start <= a.end. This correctly
				// covers adjacent-but-not-overlapping (a.end == b.start-1,
				// fails the test), full containment (b inside a, passes),
				// and identical ranges (passes).
				if a.start <= b.end && b.start <= a.end {
					lo, hi := max(a.start, b.start), min(a.end, b.end)
					ea, eb := p.Entries[a.idx], p.Entries[b.idx]
					findings = append(findings, Finding{
						Kind: KindOverlap, Severity: SeverityError,
						Message: fmt.Sprintf("channels %d-%d overlap between %q and %q in universe %d",
							lo, hi, EntryLabel(ea), EntryLabel(eb), universe),
						EntryIDs: []string{ea.ID, eb.ID}, Universe: universe,
						ChannelStart: uint16(lo), ChannelEnd: uint16(hi),
					})
				}
			}
		}
	}

	// --- duplicate fixture numbers (non-empty only) -----------------------
	byFixtureNumber := map[string][]int{}
	for i, e := range p.Entries {
		if e.FixtureNumber == "" {
			continue
		}
		byFixtureNumber[e.FixtureNumber] = append(byFixtureNumber[e.FixtureNumber], i)
	}
	for num, idxs := range byFixtureNumber {
		if len(idxs) < 2 {
			continue
		}
		ids := make([]string, len(idxs))
		for i, idx := range idxs {
			ids[i] = p.Entries[idx].ID
		}
		findings = append(findings, Finding{
			Kind: KindDuplicateFixtureNumber, Severity: SeverityError,
			Message:  fmt.Sprintf("fixture number %q is used by %d entries", num, len(idxs)),
			EntryIDs: ids,
		})
	}

	return findings
}

// EntryLabel returns a human-readable label for e (Name if set, else
// FixtureType, else its bare ID) — the single place every UI-facing message
// that needs to name an entry (collision messages here, rig-check state and
// reconcile exports in internal/web) resolves that fallback chain, so it
// never drifts between call sites.
func EntryLabel(e Entry) string {
	if e.Name != "" {
		return e.Name
	}
	if e.FixtureType != "" {
		return e.FixtureType
	}
	return e.ID
}
