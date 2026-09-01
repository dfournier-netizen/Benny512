// This file implements the two pieces of the function-aware Rig Check
// foundation that need BOTH internal/rdm and internal/patch — package patch
// deliberately stays RDM-free (see entry.go's package doc comment) and
// package rdm has no reason to know about patch's taxonomy, so, per the
// precedent internal/params/classification.go's own doc comment sets out
// ("params cannot import capture directly... the web layer, which already
// imports both, is where Tier and PIDName are combined"), the glue lives
// here:
//
//   - Task 3's second half: mapping a decoded RDM SLOT_INFO/SLOT_DESCRIPTION
//     read onto patch's taxonomy, producing patch.ChannelFunction values
//     tagged Source==SourceRDMInferred. BuildRDMInferredChannelFunctions is
//     the whole of this — pure, no network/session — so it's exercised
//     directly by patchattrs_test.go's synthetic SLOT_INFO data rather than
//     needing a live device; actually driving a GET SLOT_INFO/
//     SLOT_DESCRIPTION against a real fixture and calling this with the
//     result is stage 2's job (this is the foundation those calls plug
//     into, not the calling code itself — see the task brief's stage
//     split).
//   - Task 4: read-only HTTP exposure of internal/patch/resolve.go's
//     ResolveEntryGroups/SummarizeSelection over the active patch.
package web

import (
	"net/http"
	"strings"

	"benny512/internal/patch"
	"benny512/internal/rdm"
)

// --- Task 3: RDM slot-label -> taxonomy bridge ------------------------------

// rdmSlotLabelAttributes maps a CONFIRMED (see internal/rdm/slotinfo.go's
// doc comment — every value below is Table C-2, ANSI E1.20-2025 Appendix C,
// read directly) RDM Slot Label ID to this package's chosen taxonomy
// attribute-name equivalent — i.e. what GDTF attribute name this RDM
// concept is closest to, for GroupForAttribute (internal/patch/taxonomy.go)
// to resolve the same way a GDTF-derived ChannelFunction would. This
// mapping choice itself (SD_PAN -> "Pan", SD_ROTO_GOBO_WHEEL -> "Gobo", ...)
// is this package's own judgment call, same "data table the owner extends"
// shape as attributeTaxonomy and pidTiers — NOT part of the CONFIRMED E1.20
// text (the spec defines what SD_PAN means, not that it should display as
// the string "Pan"), so treat the attribute-name choices here as
// UNVERIFIED-by-design even though the SlotLabelID -> meaning association
// itself is CONFIRMED.
var rdmSlotLabelAttributes = map[rdm.SlotLabelID]string{
	rdm.SDIntensity:       "Dimmer",
	rdm.SDIntensityMaster: "Dimmer",

	rdm.SDPan:  "Pan",
	rdm.SDTilt: "Tilt",

	rdm.SDColorWheel:        "ColorWheel",
	rdm.SDColorSubCyan:      "ColorSub_C",
	rdm.SDColorSubYellow:    "ColorSub_Y",
	rdm.SDColorSubMagenta:   "ColorSub_M",
	rdm.SDColorAddRed:       "ColorAdd_R",
	rdm.SDColorAddGreen:     "ColorAdd_G",
	rdm.SDColorAddBlue:      "ColorAdd_B",
	rdm.SDColorCorrection:   "CTO",
	rdm.SDColorScroll:       "ColorScroll",
	rdm.SDColorAddLime:      "ColorAdd_Lime",
	rdm.SDColorAddIndigo:    "ColorAdd_Indigo",
	rdm.SDColorAddCyan:      "ColorAdd_C",
	rdm.SDColorAddDeepRed:   "ColorAdd_DeepRed",
	rdm.SDColorAddDeepBlue:  "ColorAdd_DeepBlue",
	rdm.SDColorAddNatWhite:  "ColorAdd_NatWhite",
	rdm.SDColorSemaphore:    "ColorSemaphore",
	rdm.SDColorAddAmber:     "ColorAdd_Amber",
	rdm.SDColorAddWhite:     "ColorAdd_W",
	rdm.SDColorAddWarmWhite: "ColorAdd_WarmWhite",
	rdm.SDColorAddCoolWhite: "ColorAdd_CoolWhite",
	rdm.SDColorSubUV:        "ColorSub_UV",
	rdm.SDColorHue:          "HSB_Hue",
	rdm.SDColorSaturation:   "HSB_Saturation",
	rdm.SDColorAddUV:        "ColorAdd_UV",

	rdm.SDStaticGoboWheel: "Gobo",
	rdm.SDRotoGoboWheel:   "Gobo",
	rdm.SDPrismWheel:      "Prism",
	rdm.SDEffectsWheel:    "Effects",

	rdm.SDBeamSizeIris:   "Iris",
	rdm.SDEdge:           "Focus",
	rdm.SDFrost:          "Frost",
	rdm.SDStrobe:         "Shutter",
	rdm.SDZoom:           "Zoom",
	rdm.SDFramingShutter: "Shaper",
	rdm.SDShutterRotate:  "ShaperRot",
	rdm.SDDouser:         "Douser",
	rdm.SDBarnDoor:       "Shaper",

	// Control-function labels (0x05xx): none of these are a Dimmer/
	// Position/Colour/Beam/Focus/Shaper function — left unmapped so
	// GroupForAttribute("") lands them in GroupOther, same as any other
	// unrecognized attribute (task rule: unmapped is surfaced, not
	// dropped). Listed explicitly here (rather than as a bare comment)
	// would just duplicate the "absent means Other" behavior the code
	// already has for free; omitted from the map on purpose.
}

// BuildRDMInferredChannelFunctions converts decoded SLOT_INFO entries (plus
// optional slot-description text, keyed by slot offset, from separate
// SLOT_DESCRIPTION GETs) into patch.Entry.ChannelFunctions — every value
// tagged Source==SourceRDMInferred, per decision (3)'s hard constraint
// (task brief) that this can never be confused with GDTF-derived data. A
// secondary-type slot (fine byte, control modifier, etc.) is resolved to
// its PRIMARY slot's attribute (mirroring how a GDTF 16-bit function's
// coarse+fine offsets both resolve to the same attribute — see
// gdtfparse.js's channelFunctions rule) so the taxonomy sees one function
// spanning both offsets either way; a secondary slot whose primary offset
// isn't present in slots at all (malformed/partial SLOT_INFO) is skipped
// rather than guessing.
func BuildRDMInferredChannelFunctions(slots []rdm.SlotInfoEntry, descriptions map[uint16]string) map[uint16]patch.ChannelFunction {
	out := make(map[uint16]patch.ChannelFunction, len(slots))

	// First pass: primary slots, so secondary slots (second pass) can look
	// up the attribute their primary slot resolved to.
	primaryAttr := make(map[uint16]string, len(slots))
	for _, s := range slots {
		if s.Type.IsSecondary() {
			continue
		}
		labelID, _ := s.LabelID()
		attr := rdmSlotLabelAttributes[labelID] // "" if unmapped -> GroupOther
		primaryAttr[s.Offset] = attr
		// RDMSlotLabel carries the strongest raw evidence available: the
		// SLOT_DESCRIPTION free-text label when the caller supplied one
		// (a separate GET per offset — often not fetched for every slot),
		// else the symbolic Slot Label ID name (still real evidence, just
		// less specific than a device's own text).
		label := labelID.String()
		if desc, ok := descriptions[s.Offset]; ok && desc != "" {
			label = desc
		}
		out[s.Offset] = patch.ChannelFunction{
			Source:       patch.SourceRDMInferred,
			Attribute:    attr,
			ChannelSets:  make([]patch.ChannelSet, 0),
			RDMSlotType:  s.Type.String(),
			RDMSlotLabel: label,
		}
	}

	// Second pass: secondary slots, resolved via their primary's attribute.
	for _, s := range slots {
		if !s.Type.IsSecondary() {
			continue
		}
		primaryOffset, ok := s.PrimaryOffset()
		if !ok {
			continue
		}
		attr, ok := primaryAttr[primaryOffset]
		if !ok {
			// Primary slot this secondary refers to wasn't in slots at
			// all — malformed/partial data. Skip rather than invent an
			// attribute with no basis; the offset simply stays absent.
			continue
		}
		out[s.Offset] = patch.ChannelFunction{
			Source:      patch.SourceRDMInferred,
			Attribute:   attr,
			ChannelSets: make([]patch.ChannelSet, 0),
			RDMSlotType: s.Type.String(),
			// Secondary slots carry no Slot Label ID at all (see
			// slotinfo.go) — only a SLOT_DESCRIPTION text, if fetched.
			RDMSlotLabel: descriptions[s.Offset],
		}
	}

	return out
}

// --- Task 4: read-only attribute-resolution API -----------------------------

type resolvedFunctionJSON struct {
	Attribute string   `json:"attribute"`
	Source    string   `json:"source"`
	Offsets   []uint16 `json:"offsets"`
}

type groupAttributesJSON struct {
	Group     string                 `json:"group"`
	Functions []resolvedFunctionJSON `json:"functions"`
}

func toGroupAttributesJSON(groups []patch.GroupAttributes) []groupAttributesJSON {
	out := make([]groupAttributesJSON, 0, len(groups))
	for _, g := range groups {
		fns := make([]resolvedFunctionJSON, 0, len(g.Functions))
		for _, f := range g.Functions {
			offs := f.Offsets
			if offs == nil {
				offs = make([]uint16, 0)
			}
			fns = append(fns, resolvedFunctionJSON{Attribute: f.Attribute, Source: string(f.Source), Offsets: offs})
		}
		out = append(out, groupAttributesJSON{Group: string(g.Group), Functions: fns})
	}
	return out
}

type entryAttributesJSON struct {
	EntryID string                `json:"entryId"`
	Groups  []groupAttributesJSON `json:"groups"`
}

type functionCountJSON struct {
	Attribute string `json:"attribute"`
	Count     int    `json:"count"`
}

type groupCountJSON struct {
	Group     string              `json:"group"`
	Count     int                 `json:"count"`
	Functions []functionCountJSON `json:"functions"`
}

type selectionSummaryJSON struct {
	TotalFixtures int              `json:"totalFixtures"`
	Groups        []groupCountJSON `json:"groups"`
}

func toSelectionSummaryJSON(s patch.SelectionSummary) selectionSummaryJSON {
	out := selectionSummaryJSON{TotalFixtures: s.TotalFixtures, Groups: make([]groupCountJSON, 0, len(s.Groups))}
	for _, g := range s.Groups {
		fns := make([]functionCountJSON, 0, len(g.Functions))
		for _, f := range g.Functions {
			fns = append(fns, functionCountJSON{Attribute: f.Attribute, Count: f.Count})
		}
		out.Groups = append(out.Groups, groupCountJSON{Group: string(g.Group), Count: g.Count, Functions: fns})
	}
	return out
}

type patchAttributesResponse struct {
	Entries []entryAttributesJSON `json:"entries"`
	Summary selectionSummaryJSON  `json:"summary"`
}

// handleGetPatchAttributes implements GET /api/patch/attributes — Task 4's
// "for a patch entry or a selection" surface. An optional ?ids=e1,e2,...
// query scopes both the per-entry list and the summary to that selection
// (whole rig / one universe / one position / individual fixtures — all just
// different ways stage 2's UI will build this id list; this endpoint only
// takes ids, it has no opinion on what a "universe" or "position" selection
// means). Omitting ?ids scopes to the entire active patch. An id in the
// query that doesn't match any entry is silently ignored (not a 400) —
// matches this project's general "a selection is just data, don't make the
// caller pre-validate it" pattern elsewhere in this file (handlePatchAdopt,
// reconcile).
func (s *Server) handleGetPatchAttributes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.PatchStore.Get()
	if !ok {
		writeJSON(w, http.StatusOK, patchAttributesResponse{
			Entries: make([]entryAttributesJSON, 0),
			Summary: selectionSummaryJSON{Groups: make([]groupCountJSON, 0)},
		})
		return
	}

	entries := p.Entries
	if idsParam := r.URL.Query().Get("ids"); idsParam != "" {
		want := make(map[string]bool)
		for _, id := range strings.Split(idsParam, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				want[id] = true
			}
		}
		filtered := make([]patch.Entry, 0, len(want))
		for _, e := range p.Entries {
			if want[e.ID] {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	resp := patchAttributesResponse{
		Entries: make([]entryAttributesJSON, 0, len(entries)),
		Summary: toSelectionSummaryJSON(patch.SummarizeSelection(entries)),
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, entryAttributesJSON{
			EntryID: e.ID,
			Groups:  toGroupAttributesJSON(patch.ResolveEntryGroups(e)),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
