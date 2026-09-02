package patch

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// This file implements the patch<->RDM reconciliation matcher (architecture
// doc §3.1, task ask item 2 — "the centerpiece"). Discovered RDM devices
// carry UIDs; patch entries never do (by design — CSV/MVR don't carry UIDs
// either, so the same matcher will serve the Phase 2b importer path
// unchanged). Deliberately pure/local: DiscoveredDevice below is a small
// struct this package defines itself, NOT registry.Fixture — importing
// internal/registry here would be harmless today (no cycle: registry
// doesn't import patch) but would still couple this package's tests to
// registry's shape and to constructing rdm.UID values: internal/web is
// responsible for adapting registry.Fixture -> DiscoveredDevice at the
// HTTP-handler boundary, keeping this package testable with plain literals.
//
// --- confidence scoring design ---
//
// score(entry, device) sums up to 4 independent signals, each contributing
// a fixed maximum:
//
//	address match (universe + start address both equal)   0.40
//	footprint match (both known, equal)                    0.20
//	manufacturer fuzzy match (token containment)            0.15
//	model fuzzy match (token containment)                   0.25
//	                                                  max = 1.00
//
// The weights are deliberately tuned so that address-only agreement (0.40)
// is NOT automatically the best possible candidate for an entry: a device
// with strong type corroboration but a different address can outscore a
// same-address device with zero corroboration (footprint+mfr+model, all
// three agreeing, sums to 0.60 > 0.40). This is what makes the matcher able
// to catch the case the whole feature exists for — two fixtures physically
// swapped, so the device sitting at entry A's patched address is actually
// entry B's fixture. See TestReconcile_SwappedPair.
//
// Every entry's device candidates are scored independently (greedy, not a
// globally-optimal bipartite assignment) — documented simplification, see
// Reconcile's doc comment.
type scoreWeights struct {
	address, footprint, manufacturer, model float64
}

var weights = scoreWeights{address: 0.40, footprint: 0.20, manufacturer: 0.15, model: 0.25}

// proposedThreshold is the minimum score for a device to even be considered
// a candidate for an entry — below this, evidence is too weak to be worth
// surfacing at all (task ask: candidates are for "unresolved/ambiguous"
// cases, not every device with a stray token in common).
const proposedThreshold = 0.25

// ambiguityMargin: when the top-scoring candidate and the runner-up are
// within this margin of each other, neither is trusted automatically — both
// (and any other candidate within the margin of the top) surface for manual
// Tier-3 confirmation instead (task ask: "the 'two identical fixtures both
// mis-addressed' ambiguity case").
const ambiguityMargin = 0.10

// DiscoveredDevice is the minimal, RDM-package-free view of a live
// discovered device the matcher needs. internal/web builds these from
// registry.Fixture.
type DiscoveredDevice struct {
	// UID is the device's RDM UID in string form (e.g. "1900:00000042").
	UID string
	// Universe is the Art-Net Port-Address (raw value) this device was
	// discovered on.
	Universe uint16
	// StartAddress is the device's live DMX_START_ADDRESS, if known.
	StartAddress uint16
	AddressKnown bool
	// Footprint is the device's live DEVICE_INFO footprint, if known.
	Footprint      uint16
	FootprintKnown bool
	// Manufacturer/Model are the device's own best-known label strings
	// (mirrors internal/web's effectiveManufacturer/effectiveModel
	// priority chain: device-reported label first, ESTA-table/hex fallback
	// second) — may be empty if nothing has been fetched for this device
	// yet ("devices with no DEVICE_INFO yet", task ask).
	Manufacturer string
	Model        string
}

// EvidenceKind identifies which signal contributed to a match score.
type EvidenceKind string

// Evidence kinds.
const (
	EvidenceAddress          EvidenceKind = "address"
	EvidenceFootprint        EvidenceKind = "footprint"
	EvidenceManufacturer     EvidenceKind = "manufacturer"
	EvidenceModel            EvidenceKind = "model"
	EvidenceConfirmedPairing EvidenceKind = "confirmed_pairing"
)

// Evidence is one field-level agreement (or the fact of a prior user
// confirmation) that contributed to a Row's or Candidate's confidence —
// task ask: "evidence that drove each decision (which fields agreed)".
type Evidence struct {
	Kind   EvidenceKind `json:"kind"`
	Detail string       `json:"detail"`
}

// RowStatus classifies one reconcile row.
type RowStatus string

// Row statuses (task ask, item 2's diff/report classification).
const (
	// StatusMatched: entry and device agree on address (the winning
	// candidate for this entry, and its live address equals the patched
	// address).
	StatusMatched RowStatus = "matched"
	// StatusAddressMismatch: entry paired to a device (confirmed, or the
	// clear winning candidate via corroboration) whose live address does
	// NOT equal the patched address.
	StatusAddressMismatch RowStatus = "address_mismatch"
	// StatusMissing: entry has no device candidate at all (confirmed UID
	// not currently discovered, or no live device scores above threshold).
	StatusMissing RowStatus = "missing"
	// StatusUnpatched: device discovered but not claimed by any entry.
	StatusUnpatched RowStatus = "unpatched"
	// StatusAmbiguous: multiple candidates within ambiguityMargin of each
	// other — needs a human (Tier 3), optionally assisted by Identify.
	StatusAmbiguous RowStatus = "ambiguous"
)

// Candidate is one scored device considered for an entry (surfaced in full
// for StatusAmbiguous rows; the winning candidate for Matched/
// AddressMismatch rows is folded directly into the Row).
type Candidate struct {
	DeviceUID  string     `json:"deviceUid"`
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// Row is one line of the reconcile diff: either an entry (EntryID set), a
// bare unclaimed device (DeviceUID set, EntryID empty, StatusUnpatched), or
// both (a resolved pairing).
type Row struct {
	EntryID    string     `json:"entryId,omitempty"`
	DeviceUID  string     `json:"deviceUid,omitempty"`
	Status     RowStatus  `json:"status"`
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	// Candidates is populated for StatusAmbiguous rows: every candidate
	// within ambiguityMargin of the top score, for the UI to present as
	// pick-one-of-N (task ask: "Identify assist for ambiguous rows" — the
	// UI drives Identify per candidate UID from this list).
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Report is Reconcile's full output.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Rows        []Row     `json:"rows"`
}

// Reconcile matches entries against devices using the tiered, confidence-
// scored algorithm described in this file's doc comment, and returns one
// Row per entry plus one Row per device left unclaimed by any entry.
//
// Documented simplification: matching is greedy/per-entry, not a globally
// optimal bipartite assignment (e.g. the Hungarian algorithm) — each entry
// independently picks its best-scoring device. Two different entries can
// legitimately want the same device only when the patch itself has a data
// error (e.g. a duplicate entry); Phase 2a does not attempt to resolve that
// contention automatically, since DetectCollisions already flags duplicate
// fixture numbers as a data problem worth fixing at the source. If this
// proves too coarse in practice (Dom hits a real rig where it matters), a
// stable-assignment pass can be layered on top of scorePair without
// changing this function's signature.
func Reconcile(entries []Entry, devices []DiscoveredDevice) Report {
	now := time.Now()
	claimed := make(map[string]bool, len(devices)) // device UID -> claimed by a Matched/AddressMismatch row
	rows := make([]Row, 0, len(entries)+len(devices))

	for _, e := range entries {
		row := reconcileEntry(e, devices, claimed)
		rows = append(rows, row)
	}

	// Any device never claimed by a Matched/AddressMismatch row is
	// Unpatched — including devices that appeared only as Ambiguous
	// candidates (an ambiguous pairing is not yet a real claim).
	for _, d := range devices {
		if claimed[d.UID] {
			continue
		}
		rows = append(rows, Row{DeviceUID: d.UID, Status: StatusUnpatched})
	}

	return Report{GeneratedAt: now, Rows: rows}
}

func reconcileEntry(e Entry, devices []DiscoveredDevice, claimed map[string]bool) Row {
	if e.ConfirmedUID != "" && e.MatchState == MatchStateConfirmed {
		for _, d := range devices {
			if d.UID != e.ConfirmedUID {
				continue
			}
			claimed[d.UID] = true
			ev := []Evidence{{Kind: EvidenceConfirmedPairing, Detail: "user-confirmed pairing"}}
			if e.Universe == d.Universe && d.AddressKnown && e.StartAddress == d.StartAddress {
				return Row{EntryID: e.ID, DeviceUID: d.UID, Status: StatusMatched, Confidence: 1.0, Evidence: ev}
			}
			ev = append(ev, Evidence{Kind: EvidenceAddress, Detail: "confirmed device's live address differs from the patched address"})
			return Row{EntryID: e.ID, DeviceUID: d.UID, Status: StatusAddressMismatch, Confidence: 1.0, Evidence: ev}
		}
		// Confirmed device not currently discovered.
		return Row{EntryID: e.ID, Status: StatusMissing, Evidence: []Evidence{{Kind: EvidenceConfirmedPairing, Detail: "confirmed pairing, but device not currently discovered"}}}
	}

	type scored struct {
		device Candidate
		addr   bool // does this candidate's device sit at the entry's patched address?
	}
	var candidates []scored
	entryTokens := NormalizeTokens(e.FixtureType)
	for _, d := range devices {
		score, ev := scorePair(e, d, entryTokens)
		if score < proposedThreshold {
			continue
		}
		candidates = append(candidates, scored{
			device: Candidate{DeviceUID: d.UID, Confidence: score, Evidence: ev},
			addr:   e.Universe == d.Universe && d.AddressKnown && e.StartAddress == d.StartAddress,
		})
	}
	if len(candidates) == 0 {
		return Row{EntryID: e.ID, Status: StatusMissing}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].device.Confidence > candidates[j].device.Confidence })

	top := candidates[0]
	var tied []scored
	for _, c := range candidates {
		if top.device.Confidence-c.device.Confidence <= ambiguityMargin {
			tied = append(tied, c)
		}
	}
	if len(tied) > 1 {
		cs := make([]Candidate, len(tied))
		for i, t := range tied {
			cs[i] = t.device
		}
		return Row{EntryID: e.ID, Status: StatusAmbiguous, Confidence: top.device.Confidence, Candidates: cs}
	}

	claimed[top.device.DeviceUID] = true
	status := StatusAddressMismatch
	if top.addr {
		status = StatusMatched
	}
	return Row{EntryID: e.ID, DeviceUID: top.device.DeviceUID, Status: status, Confidence: top.device.Confidence, Evidence: top.device.Evidence}
}

// scorePair computes the confidence score and evidence list for pairing
// entry e with device d (see file doc comment for the weight table).
func scorePair(e Entry, d DiscoveredDevice, entryTokens map[string]struct{}) (float64, []Evidence) {
	var score float64
	var ev []Evidence

	if e.Universe == d.Universe && d.AddressKnown && e.StartAddress == d.StartAddress {
		score += weights.address
		ev = append(ev, Evidence{Kind: EvidenceAddress, Detail: "same universe and start address"})
	}
	if e.Footprint > 0 && d.FootprintKnown && e.Footprint == d.Footprint {
		score += weights.footprint
		ev = append(ev, Evidence{Kind: EvidenceFootprint, Detail: "footprint agrees"})
	}
	if d.Manufacturer != "" {
		mfrTokens := NormalizeTokens(d.Manufacturer)
		ratio := TokenContainment(entryTokens, mfrTokens)
		if ratio > 0 {
			score += weights.manufacturer * ratio
			ev = append(ev, Evidence{Kind: EvidenceManufacturer, Detail: "manufacturer \"" + d.Manufacturer + "\" found in patched fixture type"})
		}
	}
	if d.Model != "" {
		modelTokens := NormalizeTokens(d.Model)
		ratio := TokenContainment(entryTokens, modelTokens)
		if ratio > 0 {
			score += weights.model * ratio
			ev = append(ev, Evidence{Kind: EvidenceModel, Detail: "model \"" + d.Model + "\" found in patched fixture type"})
		}
	}
	return score, ev
}

// --- normalized fuzzy string comparison -------------------------------
//
// NormalizeTokens and TokenContainment are EXPORTED, unlike the rest of this
// file's scoring internals, for exactly one reason: internal/library needs
// this project's fixture-name normalisation verbatim for its own tolerant
// record matching (library/match.go's Record.Matches), and a second
// normaliser over there would drift from this one on precisely the inputs
// that matter — "JDC1" vs "JDC 1" vs "GLP JDC-1". They are the normaliser
// and the comparison ONLY; the weights, the proposal threshold and the
// ambiguity margin stay unexported, so retuning the matcher cannot silently
// change what the library considers the same fixture type (which is what
// happened when library/match.go reached these through patch.Reconcile and
// inferred containment from a score against proposedThreshold).

// NormalizeTokens lowercases s, treats every run of non-alphanumeric
// characters as a separator (so punctuation/whitespace differences never
// matter — task ask: "case/punctuation/whitespace-insensitive, token-
// based"), ALSO splits at every letter<->digit boundary (so "JDC1"
// tokenizes identically to "JDC 1" or "JDC-1" — same fixture-naming
// leniency the GDTF-import matcher needs, see gdtfparse-driven patch.js's
// fuzzyTokenSet, a hand-port of this exact function for the browser side
// where match.go itself isn't reachable), and returns the resulting word
// set. A run of letters or a run of digits is never split internally — only
// the letter/digit BOUNDARY is a break point, so "2x" still splits into
// "2","x" (two different characters classes) while "1960" stays one token.
func NormalizeTokens(s string) map[string]struct{} {
	var b strings.Builder
	var prevDigit, havePrev bool
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			isDigit := unicode.IsDigit(r)
			if havePrev && isDigit != prevDigit {
				b.WriteRune(' ')
			}
			b.WriteRune(r)
			prevDigit, havePrev = isDigit, true
		} else {
			b.WriteRune(' ')
			havePrev = false
		}
	}
	fields := strings.Fields(b.String())
	set := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		set[f] = struct{}{}
	}
	return set
}

// TokenContainment returns the fraction of ref's tokens that also appear in
// text — e.g. text="rogue outcast 2x wash" (from a patch FixtureType of
// "Rogue Outcast 2X Wash") against ref="chauvet rogue outcast 2x wash"
// (a device's DEVICE_MODEL_DESCRIPTION combined... no, ref here is just the
// device's own field, e.g. Model="Rogue Outcast 2X Wash") scores 1.0 since
// every ref token is present in text. Deliberately containment-based rather
// than symmetric Jaccard: the patch's free-text FixtureType commonly
// includes extra words (manufacturer name, notes) the device's single
// Manufacturer/Model field doesn't, and that asymmetry should not be
// penalized.
func TokenContainment(text, ref map[string]struct{}) float64 {
	if len(ref) == 0 {
		return 0
	}
	hit := 0
	for t := range ref {
		if _, ok := text[t]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(ref))
}
