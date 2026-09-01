// This file implements Task 4 of the function-aware Rig Check foundation:
// a read-only resolution API over a patch entry's ChannelFunctions
// (entry.go) and taxonomy (taxonomy.go) — the surface stage 2's Rig Check
// UI and attribute-tree widget will consume, plus the mixed-rig counting
// decision (2) needs ("12 of 40 fixtures have Shapers" — a property of a
// SELECTION of entries, not a single entry). internal/web/patchattrs.go
// exposes this over HTTP; this file is pure, stdlib-only, unit-testable
// without any HTTP/RDM/session plumbing, per this package's layering rule.
package patch

import "sort"

// ResolvedFunction is one taxonomy attribute this entry's ChannelFunctions
// resolve to, with every DMX offset (within the entry's footprint) that
// drives it and the provenance of that mapping. Source is carried here
// (not just on the underlying ChannelFunction) so a caller reading only
// ResolveEntryGroups's output — never touching Entry.ChannelFunctions
// directly — still cannot lose track of whether this is GDTF-authoritative
// or an RDM-inferred approximation (decision 3's hard constraint).
type ResolvedFunction struct {
	Attribute string
	Source    ChannelFunctionSource
	// Offsets are the 1-based-within-footprint DMX offsets driving this
	// attribute, ascending. A 16-bit function (e.g. 16-bit Pan) has two
	// offsets here (coarse, then fine) when the underlying ChannelFunctions
	// were both resolved to the same Attribute; the offsets are NOT
	// deduplicated or coalesced into a range, since a Rig Check test
	// pattern (stage 2) needs the individual DMX slots to write to, not a
	// display-only range string.
	Offsets []uint16
}

// GroupAttributes is one taxonomy group's resolved functions for one entry.
type GroupAttributes struct {
	Group     AttributeGroup
	Functions []ResolvedFunction
}

// ResolveEntryGroups resolves e.ChannelFunctions into taxonomy groups: every
// offset with a non-absent ChannelFunction is bucketed by
// GroupForAttribute(cf.Attribute), functions sharing the same (Attribute,
// Source) pair within a group are merged into one ResolvedFunction (e.g.
// coarse+fine offsets of the same 16-bit attribute), and only groups with at
// least one function are returned — an entry with no ChannelFunctions data
// at all (Task 1's "absent" provenance) returns an empty slice, never a
// group with zero functions padded in to make every entry look
// function-aware when it isn't (that would be exactly the "silently guess"
// failure mode decision (3) forbids, applied to the group summary rather
// than the raw data).
//
// Groups are returned in AllGroups order; within a group, functions are
// sorted by Attribute for a stable, deterministic UI render.
func ResolveEntryGroups(e Entry) []GroupAttributes {
	type key struct {
		attr   string
		source ChannelFunctionSource
	}
	byGroup := make(map[AttributeGroup]map[key][]uint16)

	offsets := make([]uint16, 0, len(e.ChannelFunctions))
	for off := range e.ChannelFunctions {
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })

	for _, off := range offsets {
		cf := e.ChannelFunctions[off]
		if cf.Source == SourceAbsent {
			// An explicitly-absent ChannelFunction (zero value) carries no
			// resolvable attribute — skip it rather than surfacing a
			// meaningless empty-string "Other" function. A genuinely
			// unrecognized but PRESENT source (e.g. an RDM slot this
			// package couldn't map) still has Source set and still shows
			// up, per GroupForAttribute's "" -> GroupOther fallback.
			continue
		}
		g := GroupForAttribute(cf.Attribute)
		if byGroup[g] == nil {
			byGroup[g] = make(map[key][]uint16)
		}
		k := key{cf.Attribute, cf.Source}
		byGroup[g][k] = append(byGroup[g][k], off)
	}

	out := make([]GroupAttributes, 0, len(byGroup))
	for _, g := range AllGroups {
		fns, ok := byGroup[g]
		if !ok {
			continue
		}
		resolved := make([]ResolvedFunction, 0, len(fns))
		for k, offs := range fns {
			resolved = append(resolved, ResolvedFunction{Attribute: k.attr, Source: k.source, Offsets: offs})
		}
		sort.Slice(resolved, func(i, j int) bool {
			if resolved[i].Attribute != resolved[j].Attribute {
				return resolved[i].Attribute < resolved[j].Attribute
			}
			return resolved[i].Source < resolved[j].Source
		})
		out = append(out, GroupAttributes{Group: g, Functions: resolved})
	}
	return out
}

// FunctionCount is one attribute's presence count within a selection.
type FunctionCount struct {
	Attribute string
	Count     int
}

// GroupCount is one taxonomy group's presence count within a selection —
// Count is fixtures with AT LEAST ONE function in this group (the union
// across Functions, not a sum — a fixture with both Pan and Tilt counts
// once toward Position's Count), matching decision (2)'s "12 of 40
// fixtures have Shapers" phrasing, which counts fixtures, not functions.
type GroupCount struct {
	Group     AttributeGroup
	Count     int
	Functions []FunctionCount
}

// SelectionSummary is the mixed-rig counting decision (2) made concrete:
// for an arbitrary selection of patch entries (whole rig, one universe, one
// position, or a hand-picked set — the caller decides what "selection"
// means, this function only counts), how many of the selected fixtures
// have each function/group present. Never blocks or filters anything —
// decision (2) is explicit that mixed rigs are reported, not gated.
type SelectionSummary struct {
	TotalFixtures int
	Groups        []GroupCount
}

// SummarizeSelection computes SelectionSummary for entries. Every entry
// counts toward TotalFixtures regardless of whether it has any resolved
// ChannelFunctions — a fixture with none is a real, correctly-reported
// zero contribution to every function's count, not an entry silently
// excluded from the denominator (which would make "N of M" lie about M).
func SummarizeSelection(entries []Entry) SelectionSummary {
	type key struct {
		group AttributeGroup
		attr  string
	}
	fnFixtures := make(map[key]map[string]bool) // key -> set of entry IDs with this function
	groupFixtures := make(map[AttributeGroup]map[string]bool)

	for _, e := range entries {
		for _, ga := range ResolveEntryGroups(e) {
			if groupFixtures[ga.Group] == nil {
				groupFixtures[ga.Group] = make(map[string]bool)
			}
			groupFixtures[ga.Group][e.ID] = true
			for _, fn := range ga.Functions {
				k := key{ga.Group, fn.Attribute}
				if fnFixtures[k] == nil {
					fnFixtures[k] = make(map[string]bool)
				}
				fnFixtures[k][e.ID] = true
			}
		}
	}

	summary := SelectionSummary{TotalFixtures: len(entries)}
	for _, g := range AllGroups {
		fixtures, ok := groupFixtures[g]
		if !ok {
			continue
		}
		gc := GroupCount{Group: g, Count: len(fixtures)}
		// Collect this group's function attribute names, sorted, for a
		// stable per-function breakdown.
		attrs := make(map[string]bool)
		for k := range fnFixtures {
			if k.group == g {
				attrs[k.attr] = true
			}
		}
		names := make([]string, 0, len(attrs))
		for a := range attrs {
			names = append(names, a)
		}
		sort.Strings(names)
		for _, a := range names {
			gc.Functions = append(gc.Functions, FunctionCount{Attribute: a, Count: len(fnFixtures[key{g, a}])})
		}
		summary.Groups = append(summary.Groups, gc)
	}
	return summary
}
