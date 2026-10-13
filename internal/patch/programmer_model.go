// This file is the Console-lite programmer's PARAMETER MODEL (chunk C4a):
// for one patch entry, the list of controllable parameters the C1 channel
// detail describes. Pure and stdlib-only, like resolve.go. programmer.go
// holds the state (selection, values) and drives the output engine.
//
// RULES, each one decided here and stated where it is implemented:
//
//   - A PARAMETER is one GDTF <DMXChannel>: its coarse offset and every byte
//     after it (ChannelFunction.ByteCount/ByteIndex, DIN SPEC 15800 Table 58:
//     "Relative addresses of the current DMX channel from highest to least
//     significant"), the logical-channel attribute, its taxonomy group, its
//     geometry instance, every function with its DMX and physical range,
//     its default, wheel link, channel sets and mode master. Values are at
//     the channel's FULL resolution (0..255, 0..65535, ...), the same
//     convention FunctionRange uses.
//   - DEFAULT (the base state). The first ChannelFunction's Default, which
//     already carries the GDTF 1.0 <DMXChannel Default> fallback (C1b). DIN
//     SPEC 15800 Table 58 makes the first function of the first logical
//     channel the InitialFunction unless the file names another; the parser
//     does not read InitialFunction, so a file naming a different initial
//     function would rest at the first function's default — none of the
//     vendor files in testdata does (Paladin names its first function).
//     No default stated = unknown: the base writes 0 and says so.
//   - CELLS. A cell (sub-fixture) is a geometry instance whose set of
//     attributes is identical to at least one other instance of the same
//     fixture: the Paladin Cube's "Beam 1/2/3" (RGBW each), a pixel bar's
//     referenced pixels. Instances with a unique attribute set (a mover's
//     Yoke/Head/Beam, a master dimmer above the cells) belong to the fixture
//     itself. GeometryCellEstimate (phasecount.go) counts the largest number
//     of instances sharing ONE attribute; the two agree except for a master
//     channel sharing an attribute with its cells, which the estimate counts
//     as one more cell.
//   - FALLBACK. An offset without full channel detail (pre-v6 data, an
//     RDM-inferred slot) is offered from its first-function fields, marked
//     Detail "first-function" (GDTF) or "attribute-only" (RDM). Its byte
//     count is the stated DefaultByteCount when there is one, otherwise each
//     offset is its own 8-bit channel — two offsets of one attribute are
//     never assumed to be coarse+fine.
//   - Offsets with no channel function at all are RAW offsets: 8-bit, no
//     attribute, offered for raw DMX testing only.
//   - VIRTUAL DIMMER (G1, owner decision 2026-10-08: "for all fixtures
//     without a dimmer, but with RGB mixing [there] should [be] a 'virtual
//     dimmer'"). A SCOPE is the fixture's own (non-cell) channels, or one
//     cell's. A scope gets one virtual Dimmer parameter when it has red,
//     green and blue additive emitters (ColorAdd_R/G/B, or
//     ColorRGB_Red/Green/Blue — the GDTF attribute names the taxonomy
//     groups as colour) and no "Dimmer" channel of its own. I2d4 (owner
//     2026-10-09): a cell gets one even when the fixture has a master
//     Dimmer — "if a fixture or subfixture doesn't have a dimmer channel,
//     we'll need a virtual dimmer channel". The cell's output is its
//     colour x its virtual level; the master, a real channel, stays on top.
//     J1b (owner 2026-10-12 20:11, refining I2d4): a cell gets one only
//     when its PART (PARTS below) has no real Dimmer of its own. The GLP
//     JDC1's Plate Module dimmer masters its plate pixels on the real
//     fixture, so the plate pixels have none and the Plate is driven by
//     that dimmer; the Rayzor 1960's RGBW pixels (the head dimmer is the
//     separate master part), the Paladin Cube's beams and a batten's
//     pixels keep theirs.
//     J1b (owner 2026-10-12 20:11, superseding I2d4's "a whole-fixture
//     intensity control is the master only"): a WHOLE-fixture intensity
//     control (Dimmer set, fan, the type's Master fader, a group-fader
//     member, Lowlight, a dimmer test) drives EVERY part — the real master
//     channel if there is one AND every other Dimmer, the cells' virtual
//     dimmers included ("the Master overrides the subs"). Where a real
//     master physically masters its cells too, the two levels multiply on
//     the fixture (the owner's call). A cell target is still its own
//     channels. Highlight and presets take the whole look, as before.
//     It scales EVERY additive channel of the scope: every ColorAdd_*
//     (W, WW/CW, amber, lime, UV, ...) and ColorRGB_* channel.
//     UNVERIFIED: GDTF describes ColorRGB_* as "emitter or subtractive
//     filter"; none of the vendor files here uses it. The parameter is
//     8-bit (0-255, Detail "virtual", Virtual true), default 0 (J1: dark at
//     Arm, below; G1-I2g had untouched = full). It has no DMX bytes (Offsets
//     empty); its synthetic Offset is VirtualDimmerOffset + the cell index
//     (0 for the fixture's own scope), above any 512-slot footprint, so it
//     is keyed, set, cleared, stored in presets and faned like any other
//     channel. The engine applies it (session.VirtualDimmer).
//   - PARTS (J1, owner 2026-10-12: sub-fixture faders "grouped by GDTF
//     geometry name"). Every intensity parameter (a real Dimmer or a
//     virtual dimmer) belongs to one PART, keyed by the first word of its
//     geometry name once the instance index (":0") and any all-digit word
//     are stripped, case-folded: "Plate Module" and "Plate Pixel" are part
//     "plate", "Beam Module" and "Beam Pixel" "beam", "Beam 1".."Beam 3"
//     "beam", "Head_Extended" "head", "Spark LED Strobe Module" "spark". A
//     channel with no geometry is part "". The stored profile keeps only
//     the geometry instance name, not the GDTF geometry tree, so names are
//     all there is; two unrelated geometries that share a first word
//     ("LED Ring", "LED Strip") are one part. A part's name is the words
//     every one of its geometry names starts with ("Plate"; a single
//     geometry keeps its whole name), or "Fixture" for part "".
//     The MASTER part is the one part made only of real Dimmer channels
//     that are not cells' — the only such part, or, when there are
//     several, the only one whose key is a body geometry (base, body,
//     head, yoke, main) or "". A part that also holds cells (any cell
//     whose geometry is in the part, J1b) is a part dimmer, never the
//     master. Rayzor 1960: head is the master; RGBW
//     pixels and SparkLED are parts. JDC1: no master; Beam and Plate are
//     parts. A batten with one Dimmer and RGB cells: that Dimmer.
//   - MASTER MULTIPLIER (J1c, owner 2026-10-13 09:31, superseding J1b's
//     "the Master sets every part"): "Master should be an overall dimmer.
//     If master is at 0 but the sub-fixtures are at full: no output. If
//     master is at full, SparkLED at full and pixels at 0: we see only the
//     SparkLED." A fixture with more than one intensity part gets one more
//     parameter, its virtual MASTER (Master, VirtualMasterOffset, resting
//     at 0 like every intensity). The fixture's master level is set on
//     the real master channel (the master part's Dimmer, if any) and the
//     virtual master together — whole-fixture Dimmer in the Console, the
//     type's Master fader, a stored group's whole-fixture member, Lowlight
//     and dimmer tests of the whole fixture (inScope). The parts keep
//     their own levels (part faders, cells). Effective part output =
//     master x part, WITHOUT double dimming: a part whose geometry lies
//     under the master channel's geometry (the GDTF tree, Entry.
//     GeometryParents) is dimmed by that channel on the fixture and is
//     left alone in software (Governed); every other part is multiplied
//     in software by the virtual master — a real part dimmer scaled as a
//     whole value, a virtual part dimmer's colour scaled (session.
//     VirtualDimmer.Multiplier: no fill, applied last). With no master
//     channel (GLP JDC1) every part is multiplied in software. A profile
//     without its tree (imported before J1c) counts every part as governed
//     by a master channel — the fader says so ("re-import to refine").
//   - DARK AT ARM (J1, owner 2026-10-12, Dom's bench test: "when armed with
//     an empty programmer, every intensity starts at 0, whatever the GDTF
//     default says"). An INTENSITY parameter rests at 0, not at its stated
//     default: every virtual dimmer, and every real channel whose attribute
//     is "Dimmer" (IsIntensityAttribute). HasDefault is true and Default 0
//     for them, DarkAtArm true; the profile's own default stays in its
//     functions (ProgFunction.Default). Colour, position, shutter and
//     everything else keep their profile defaults, so an RGB-only fixture
//     needs a dimmer level before its colour shows (accepted by the owner).
package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Parameter detail levels.
const (
	DetailFull          = "full"
	DetailFirstFunction = "first-function"
	DetailAttributeOnly = "attribute-only"
	DetailVirtual       = "virtual"
)

// VirtualDimmerOffset is the synthetic offset of a fixture's own virtual
// dimmer; a cell's is this plus the cell's index. See the file comment.
const VirtualDimmerOffset uint16 = 0xFF00

// VirtualMasterOffset (J1c) is the synthetic offset of a multi-part
// fixture's software master (see MASTER MULTIPLIER in the file comment);
// above any 512-slot footprint and below every virtual dimmer's.
const VirtualMasterOffset uint16 = 0xFE00

// ProgSet is one channel set of a function, with the wheel slot it names
// resolved against the entry's wheels.
type ProgSet struct {
	Name         string  `json:"name"`
	DMXFrom      uint32  `json:"dmxFrom"`
	DMXTo        uint32  `json:"dmxTo"`
	PhysicalFrom float64 `json:"physicalFrom"`
	PhysicalTo   float64 `json:"physicalTo"`
	HasWheelSlot bool    `json:"hasWheelSlot"`
	WheelSlot    int     `json:"wheelSlot"`
	// HasSlotDetail is true when WheelSlot names a slot that exists on the
	// function's wheel in this profile; the slot fields are then filled.
	HasSlotDetail bool   `json:"hasSlotDetail"`
	SlotName      string `json:"slotName"`
	HasColor      bool   `json:"hasColor"`
	HasSRGB       bool   `json:"hasSRGB"`
	SRGB          string `json:"srgb"`
	MediaFileName string `json:"mediaFileName"`
}

// ProgFunction is one ChannelFunction of a parameter.
type ProgFunction struct {
	Index            int     `json:"index"`
	Name             string  `json:"name"`
	Attribute        string  `json:"attribute"`
	LogicalAttribute string  `json:"logicalAttribute"`
	DMXFrom          uint32  `json:"dmxFrom"`
	DMXTo            uint32  `json:"dmxTo"`
	PhysicalFrom     float64 `json:"physicalFrom"`
	PhysicalTo       float64 `json:"physicalTo"`
	// HasPhysicalRange is false when PhysicalFrom equals PhysicalTo: no
	// physical value can be placed inside such a function.
	HasPhysicalRange bool      `json:"hasPhysicalRange"`
	HasDefault       bool      `json:"hasDefault"`
	Default          uint32    `json:"default"`
	Wheel            string    `json:"wheel"`
	ModeMaster       string    `json:"modeMaster"`
	HasMode          bool      `json:"hasMode"`
	ModeFrom         int       `json:"modeFrom"`
	ModeTo           int       `json:"modeTo"`
	Sets             []ProgSet `json:"sets"`
	// Command (I2d3): a value in this function is a one-shot fixture
	// command (IsCommandFunction) — sent by POST /api/programmer/command,
	// refused by /set. The UI reads this flag instead of guessing.
	Command bool `json:"command"`
}

// IsIntensityAttribute (J1) is THE rule for which real channels are
// intensity, so rest at 0 when armed (DARK AT ARM). GDTF's "Dimmer" feature
// group (DIN SPEC 15800 Annex A, Feature Dimmer.Dimmer) has one attribute,
// "Dimmer"; the RDM bridge maps SD_INTENSITY and SD_INTENSITY_MASTER to it
// (patchattrs.go). Exact match on purpose: "DimmerMode" and "DimmerCurve"
// are GDTF Control attributes (settings, not levels), so a prefix match
// would zero a curve or mode choice. Shutter, strobe, iris and colour are
// not intensity: they keep their defaults (the owner's rule).
func IsIntensityAttribute(attr string) bool { return attr == "Dimmer" }

// IsCommandFunction (I2d3, owner 2026-10-09) is THE rule for what a fixture
// command is, shared by /api/programmer/set (refuses), Fan (refuses) and
// the UI (sends one-shot): every function of a Control-family channel
// (taxonomy group "other": no faders, a sweep would pass through reset and
// lamp ranges), and any Reset or Lamp function on any channel (GDTF
// attributes such as FixtureGlobalReset, PositionReset, LampControl).
func IsCommandFunction(group AttributeGroup, fnAttribute string) bool {
	return group == GroupOther || strings.Contains(fnAttribute, "Reset") || strings.HasPrefix(fnAttribute, "Lamp")
}

// ProgParameter is one controllable channel of a fixture.
type ProgParameter struct {
	// Offset is the coarse offset (1-based within the footprint); Offsets
	// is every byte, coarse first.
	Offset    uint16         `json:"offset"`
	Offsets   []uint16       `json:"offsets"`
	ByteCount int            `json:"byteCount"`
	Max       uint32         `json:"max"`
	Attribute string         `json:"attribute"`
	Group     AttributeGroup `json:"group"`
	// Instance is the GDTF geometry instance; Cell is the same string when
	// that instance is a cell, "" when the channel belongs to the fixture.
	Instance   string                `json:"instance"`
	Cell       string                `json:"cell"`
	Source     ChannelFunctionSource `json:"source"`
	Detail     string                `json:"detail"`
	HasDefault bool                  `json:"hasDefault"`
	Default    uint32                `json:"default"`
	// HasHighlight/Highlight: the profile's stated highlight value at
	// channel resolution — the first function (document order) stating
	// one, which for a GDTF 1.0 file is the <DMXChannel Highlight>.
	HasHighlight bool           `json:"hasHighlight"`
	Highlight    uint32         `json:"highlight"`
	RDMSlotLabel string         `json:"rdmSlotLabel"`
	Functions    []ProgFunction `json:"functions"`
	// Virtual marks a virtual dimmer (G1, see the file comment): no DMX
	// bytes of its own; it scales the additive colour channels whose
	// coarse offsets are virtualOf.
	Virtual bool `json:"virtual"`
	// Master (J1c) marks the virtual MASTER of a fixture with several
	// intensity parts (see MASTER MULTIPLIER in the file comment): a
	// multiplier over the parts its geometry does not govern, whose coarse
	// offsets (a real part dimmer) or colour offsets (a virtual part
	// dimmer's) are masterOf.
	Master   bool `json:"master"`
	masterOf []uint16
	// DarkAtArm (J1): an intensity parameter, resting at 0 whatever the
	// profile's default (see DARK AT ARM in the file comment).
	DarkAtArm bool `json:"darkAtArm"`
	virtualOf []uint16
	// channelName is the GDTF DMXChannel name a ModeMaster link starts
	// with: geometry name + "_" + first logical channel attribute (DIN SPEC
	// 15800, DMX Channel: the name is derived, not stored).
	channelName string
	// variantKey identifies the channel layout (bytes, detail, functions)
	// so the read model can group identical fixture types without
	// re-encoding them on every read.
	variantKey string
}

// ProgPart is one intensity part of a fixture (J1; see PARTS in the file
// comment): its key, display name, whether it is the fixture's master,
// and the coarse (or synthetic virtual) offsets of its intensity
// parameters, in offset order.
type ProgPart struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Master  bool     `json:"master"`
	Dimmers []uint16 `json:"dimmers"`
	// Governed (J1c): the part sits under the master dimmer's geometry, so
	// the master channel dims it on the fixture and the software master
	// leaves it alone; false = the software master multiplies it.
	Governed bool `json:"governed"`
}

// ProgCell is one cell (sub-fixture). ID is the geometry instance string
// targets use; Index is 1-based in offset order.
type ProgCell struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// FixtureModel is one entry's parameter model.
type FixtureModel struct {
	EntryID     string `json:"entryId"`
	Name        string `json:"name"`
	FixtureType string `json:"fixtureType"`
	// ShortType (J1b) is ShortTypeName(FixtureType, the entry's GDTF
	// short name): what fader labels show.
	ShortType     string `json:"shortType"`
	Mode          string `json:"mode"`
	FixtureNumber string `json:"fixtureNumber"`
	Universe      uint16 `json:"universe"`
	StartAddress  uint16 `json:"startAddress"`
	Footprint     uint16 `json:"footprint"`
	// Profiled is false when the entry has no channel map at all: only raw
	// offsets are offered.
	Profiled   bool            `json:"profiled"`
	Cells      []ProgCell      `json:"cells"`
	Parameters []ProgParameter `json:"parameters"`
	// Parts (J1) are the fixture's intensity parts in order of their first
	// parameter; empty when it has no intensity at all.
	Parts []ProgPart `json:"parts"`
	// TreeKnown (J1c): the profile carries its GDTF geometry tree. Without
	// it every part of a fixture with a master dimmer counts as governed by
	// it (re-import the GDTF to refine).
	TreeKnown bool `json:"treeKnown"`
	// RawOffsets are footprint offsets no parameter covers, 8-bit each.
	RawOffsets []uint16 `json:"rawOffsets"`
	// Unaddressable lists footprint offsets that fall outside 1-512 at the
	// entry's start address; nothing is ever written to them.
	Unaddressable []uint16 `json:"unaddressable"`
	Notes         []string `json:"notes"`
	// fingerprint covers everything except identity and address, so values
	// survive a re-address but not a profile change.
	fingerprint string
}

func maxForBytes(n int) uint32 {
	if n >= 4 {
		return 0xFFFFFFFF
	}
	return uint32(1)<<(8*uint(n)) - 1
}

// geometryName is the geometry part of a geometry instance ("Beam 1:0" ->
// "Beam 1"); the parser writes instances as name + ":" + offset base.
func geometryName(instance string) string {
	if i := strings.LastIndex(instance, ":"); i >= 0 {
		return instance[:i]
	}
	return instance
}

// BuildFixtureModel derives e's parameter model. See the file comment.
func BuildFixtureModel(e Entry) FixtureModel {
	m := FixtureModel{
		EntryID: e.ID, Name: e.Name, FixtureType: e.FixtureType, ShortType: ShortTypeName(e.FixtureType, e.FixtureTypeShortName), Mode: e.Mode, FixtureNumber: e.FixtureNumber,
		Universe: e.Universe, StartAddress: e.StartAddress, Footprint: e.Footprint,
		Profiled: len(e.ChannelFunctions) > 0, Cells: make([]ProgCell, 0), Parts: make([]ProgPart, 0),
		Parameters: make([]ProgParameter, 0), RawOffsets: make([]uint16, 0),
		Unaddressable: make([]uint16, 0), Notes: make([]string, 0),
	}
	offsets := make([]uint16, 0, len(e.ChannelFunctions))
	for off := range e.ChannelFunctions {
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	claimed := map[uint16]bool{}

	// Full channel detail: one parameter per coarse byte.
	for _, off := range offsets {
		cf := e.ChannelFunctions[off]
		if !cf.FunctionsKnown || cf.ByteCount < 1 || cf.ByteIndex != 0 || cf.Source == SourceAbsent {
			continue
		}
		offs := []uint16{off}
		complete := true
		for k := 1; k < cf.ByteCount; k++ {
			best, found := uint16(0), false
			for _, o := range offsets {
				c := e.ChannelFunctions[o]
				if claimed[o] || o == off || !c.FunctionsKnown || c.ByteIndex != k || c.ByteCount != cf.ByteCount ||
					c.GeometryInstance != cf.GeometryInstance || c.Attribute != cf.Attribute {
					continue
				}
				if !found || distance(o, off) < distance(best, off) {
					best, found = o, true
				}
			}
			if !found {
				complete = false
				break
			}
			offs = append(offs, best)
			claimed[best] = true
		}
		if !complete {
			for _, o := range offs[1:] {
				delete(claimed, o)
			}
			m.Notes = append(m.Notes, fmt.Sprintf("The %d-byte %s channel at offset %d is missing one of its bytes in the channel map, so it is offered as raw offsets only.", cf.ByteCount, cf.Attribute, off))
			continue
		}
		claimed[off] = true
		m.Parameters = append(m.Parameters, fullParameter(e, cf, offs))
	}

	// Fallback: offsets with a channel function but no full detail.
	type fbKey struct {
		instance, attr string
		source         ChannelFunctionSource
	}
	fbOrder := make([]fbKey, 0)
	fbOffsets := map[fbKey][]uint16{}
	for _, off := range offsets {
		cf := e.ChannelFunctions[off]
		if claimed[off] || cf.Source == SourceAbsent || (cf.FunctionsKnown && cf.ByteCount >= 1) {
			continue
		}
		k := fbKey{cf.GeometryInstance, cf.Attribute, cf.Source}
		if _, ok := fbOffsets[k]; !ok {
			fbOrder = append(fbOrder, k)
		}
		fbOffsets[k] = append(fbOffsets[k], off)
	}
	for _, k := range fbOrder {
		offs := fbOffsets[k]
		first := e.ChannelFunctions[offs[0]]
		n := 1
		if first.HasDefault && first.DefaultByteCount >= 2 {
			n = int(first.DefaultByteCount)
			if len(offs)%n != 0 {
				m.Notes = append(m.Notes, fmt.Sprintf("%s is stated as %d-byte but %d offsets carry it, so each offset is offered as its own 8-bit channel.", k.attr, n, len(offs)))
				n = 1
			}
		}
		for i := 0; i < len(offs); i += n {
			group := offs[i : i+n]
			for _, o := range group {
				claimed[o] = true
			}
			m.Parameters = append(m.Parameters, fallbackParameter(e.ChannelFunctions[group[0]], group))
		}
	}
	sort.SliceStable(m.Parameters, func(i, j int) bool { return m.Parameters[i].Offset < m.Parameters[j].Offset })

	for off := uint16(1); off >= 1 && off <= e.Footprint; off++ {
		if !claimed[off] {
			m.RawOffsets = append(m.RawOffsets, off)
		}
	}
	// Unaddressable: a byte outside the footprint (the channel map is wider
	// than the patched footprint — never written, it would land on the next
	// fixture) or outside 1-512 at the start address.
	seen := map[uint16]bool{}
	check := func(off uint16) {
		if seen[off] {
			return
		}
		seen[off] = true
		if !e.addressable(off) {
			m.Unaddressable = append(m.Unaddressable, off)
		}
	}
	for off := uint16(1); off >= 1 && off <= e.Footprint; off++ {
		check(off)
	}
	for _, p := range m.Parameters {
		for _, o := range p.Offsets {
			check(o)
		}
	}
	sort.Slice(m.Unaddressable, func(i, j int) bool { return m.Unaddressable[i] < m.Unaddressable[j] })
	if len(m.Unaddressable) > 0 {
		m.Notes = append(m.Notes, fmt.Sprintf("%d of this fixture's channel bytes fall outside its patched footprint or outside the universe at its start address; nothing is sent to them.", len(m.Unaddressable)))
	}
	if !m.Profiled {
		m.Notes = append(m.Notes, "This fixture has no channel map, so only raw DMX per offset is offered.")
	}
	assignCells(&m)
	addVirtualDimmers(&m)
	assignParts(&m)
	addVirtualMaster(&m, e.GeometryParents)
	firstOnly := 0
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Virtual || IsIntensityAttribute(p.Attribute) {
			p.HasDefault, p.Default, p.DarkAtArm = true, 0, true
		}
		for fi := range p.Functions {
			p.Functions[fi].Command = IsCommandFunction(p.Group, p.Functions[fi].Attribute)
		}
		vb, _ := json.Marshal(struct {
			B int
			D string
			F []ProgFunction
		}{p.ByteCount, p.Detail, p.Functions})
		vs := sha256.Sum256(vb)
		p.variantKey = hex.EncodeToString(vs[:])
		if p.Detail == DetailFirstFunction {
			firstOnly++
		}
	}
	if firstOnly > 0 {
		m.Notes = append(m.Notes, fmt.Sprintf("%d channels carry only their first function's detail (the profile predates full GDTF channel detail); re-import the GDTF for every function, range and channel set.", firstOnly))
	}
	b, _ := json.Marshal(struct {
		F uint16
		P []ProgParameter
		R []uint16
		C []ProgCell
		G []ProgPart // J1c: which parts the master governs
	}{e.Footprint, m.Parameters, m.RawOffsets, m.Cells, m.Parts})
	sum := sha256.Sum256(b)
	m.fingerprint = hex.EncodeToString(sum[:])
	return m
}

// addressable reports whether offset off of e can be written: inside the
// patched footprint, and inside 1-512 at the start address.
func (e Entry) addressable(off uint16) bool {
	abs := int(e.StartAddress) + int(off) - 1
	return off >= 1 && off <= e.Footprint && e.StartAddress >= 1 && abs <= 512
}

func distance(a, b uint16) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func fullParameter(e Entry, cf ChannelFunction, offs []uint16) ProgParameter {
	p := ProgParameter{
		Offset: offs[0], Offsets: offs, ByteCount: cf.ByteCount, Max: maxForBytes(cf.ByteCount),
		Attribute: cf.Attribute, Group: GroupForAttribute(cf.Attribute), Instance: cf.GeometryInstance,
		Source: cf.Source, Detail: DetailFull, Functions: make([]ProgFunction, 0, len(cf.Functions)),
	}
	logical := cf.Attribute
	if len(cf.Functions) > 0 {
		logical = cf.Functions[0].LogicalAttribute
		p.HasDefault, p.Default = cf.Functions[0].HasDefault, cf.Functions[0].Default
	}
	p.channelName = geometryName(cf.GeometryInstance) + "_" + logical
	for _, f := range cf.Functions {
		if f.HasHighlight && f.Highlight <= p.Max {
			p.HasHighlight, p.Highlight = true, f.Highlight
			break
		}
	}
	for i, f := range cf.Functions {
		pf := ProgFunction{
			Index: i, Name: f.Name, Attribute: f.Attribute, LogicalAttribute: f.LogicalAttribute,
			DMXFrom: f.DMXFrom, DMXTo: f.DMXTo, PhysicalFrom: f.PhysicalFrom, PhysicalTo: f.PhysicalTo,
			HasPhysicalRange: f.PhysicalFrom != f.PhysicalTo, HasDefault: f.HasDefault, Default: f.Default,
			Wheel: f.Wheel, ModeMaster: f.ModeMaster, HasMode: f.HasMode, ModeFrom: f.ModeFrom, ModeTo: f.ModeTo,
			Sets: make([]ProgSet, 0, len(f.Sets)),
		}
		for _, s := range f.Sets {
			ps := ProgSet{Name: s.Name, DMXFrom: s.DMXFrom, DMXTo: s.DMXTo, PhysicalFrom: s.PhysicalFrom, PhysicalTo: s.PhysicalTo,
				HasWheelSlot: s.HasWheelSlot, WheelSlot: s.WheelSlot}
			if s.HasWheelSlot && f.Wheel != "" {
				fillSlot(&ps, e.Wheels, f.Wheel, s.WheelSlot)
			}
			pf.Sets = append(pf.Sets, ps)
		}
		p.Functions = append(p.Functions, pf)
	}
	return p
}

// fillSlot resolves a 1-based WheelSlotIndex (Table 61) on the named wheel.
func fillSlot(ps *ProgSet, wheels []Wheel, wheel string, slot int) {
	for _, w := range wheels {
		if w.Name != wheel {
			continue
		}
		if slot < 1 || slot > len(w.Slots) {
			return
		}
		s := w.Slots[slot-1]
		ps.HasSlotDetail, ps.SlotName, ps.HasColor = true, s.Name, s.HasColor
		ps.HasSRGB, ps.SRGB, ps.MediaFileName = s.HasSRGB, s.SRGB, s.MediaFileName
		return
	}
}

func fallbackParameter(cf ChannelFunction, offs []uint16) ProgParameter {
	n := len(offs)
	p := ProgParameter{
		Offset: offs[0], Offsets: append([]uint16(nil), offs...), ByteCount: n, Max: maxForBytes(n),
		Attribute: cf.Attribute, Group: GroupForAttribute(cf.Attribute), Instance: cf.GeometryInstance,
		Source: cf.Source, Detail: DetailFirstFunction, RDMSlotLabel: cf.RDMSlotLabel,
		Functions: make([]ProgFunction, 0, 1),
	}
	p.channelName = geometryName(cf.GeometryInstance) + "_" + cf.Attribute
	if cf.Source == SourceRDMInferred {
		p.Detail = DetailAttributeOnly
	}
	if cf.HasDefault {
		v := cf.Default
		if int(cf.DefaultByteCount) > n {
			// A stated 16-bit default on an offset offered 8-bit: its coarse
			// byte, as Rig Check's base state writes it.
			v >>= 8 * uint(int(cf.DefaultByteCount)-n)
		}
		if v <= p.Max {
			p.HasDefault, p.Default = true, v
		}
	}
	if cf.HasHighlight {
		v := cf.Highlight
		hb := int(cf.HighlightByteCount)
		switch {
		case hb > n:
			v >>= 8 * uint(hb-n)
		case hb >= 1 && hb < n && v <= maxForBytes(hb):
			// DIN SPEC 15800 Table 1: byte mirroring by default.
			v = uint32(math.Round(float64(v) / float64(maxForBytes(hb)) * float64(maxForBytes(n))))
		}
		if v <= p.Max {
			p.HasHighlight, p.Highlight = true, v
		}
	}
	// The first function is only offered for an 8-bit GDTF channel: the
	// legacy first-function DMXFrom/DMXTo and ChannelSet DMXFrom are coarse
	// byte readings, meaningless as a 16-bit range.
	if cf.Source == SourceGDTF && n == 1 && cf.DMXTo >= cf.DMXFrom && cf.DMXTo <= 255 {
		f := ProgFunction{Index: 0, Name: cf.FunctionName, Attribute: cf.Attribute, LogicalAttribute: cf.Attribute,
			DMXFrom: cf.DMXFrom, DMXTo: cf.DMXTo, PhysicalFrom: cf.PhysicalFrom, PhysicalTo: cf.PhysicalTo,
			HasPhysicalRange: cf.PhysicalFrom != cf.PhysicalTo, HasDefault: p.HasDefault, Default: p.Default,
			Sets: make([]ProgSet, 0, len(cf.ChannelSets))}
		for i, s := range cf.ChannelSets {
			if s.DMXFrom < f.DMXFrom || s.DMXFrom > f.DMXTo || (i > 0 && s.DMXFrom <= cf.ChannelSets[i-1].DMXFrom) {
				continue
			}
			to := f.DMXTo
			if i+1 < len(cf.ChannelSets) && cf.ChannelSets[i+1].DMXFrom > s.DMXFrom && cf.ChannelSets[i+1].DMXFrom-1 <= f.DMXTo {
				to = cf.ChannelSets[i+1].DMXFrom - 1
			}
			f.Sets = append(f.Sets, ProgSet{Name: s.Name, DMXFrom: s.DMXFrom, DMXTo: to, PhysicalFrom: s.PhysicalFrom, PhysicalTo: s.PhysicalTo})
		}
		p.Functions = append(p.Functions, f)
	}
	return p
}

// assignCells marks the instances that are cells — see the file comment.
func assignCells(m *FixtureModel) {
	attrs := map[string]map[string]bool{}
	first := map[string]uint16{}
	for _, p := range m.Parameters {
		if p.Instance == "" {
			continue
		}
		if attrs[p.Instance] == nil {
			attrs[p.Instance] = map[string]bool{}
			first[p.Instance] = p.Offset
		}
		attrs[p.Instance][p.Attribute] = true
	}
	sig := map[string]string{}
	count := map[string]int{}
	for inst, set := range attrs {
		names := make([]string, 0, len(set))
		for a := range set {
			names = append(names, a)
		}
		sort.Strings(names)
		sig[inst] = strings.Join(names, "\x00")
		count[sig[inst]]++
	}
	cells := make([]string, 0)
	for inst := range attrs {
		if count[sig[inst]] >= 2 {
			cells = append(cells, inst)
		}
	}
	sort.Slice(cells, func(i, j int) bool { return first[cells[i]] < first[cells[j]] })
	isCell := map[string]bool{}
	for i, inst := range cells {
		isCell[inst] = true
		m.Cells = append(m.Cells, ProgCell{ID: inst, Name: geometryName(inst), Index: i + 1})
	}
	for i := range m.Parameters {
		if isCell[m.Parameters[i].Instance] {
			m.Parameters[i].Cell = m.Parameters[i].Instance
		}
	}
}

// isAdditiveColour: an additive emitter a virtual dimmer scales.
func isAdditiveColour(attr string) bool {
	return strings.HasPrefix(attr, "ColorAdd_") || strings.HasPrefix(attr, "ColorRGB_")
}

// addVirtualDimmers appends the virtual Dimmer parameters — see the file
// comment's VIRTUAL DIMMER rule.
func addVirtualDimmers(m *FixtureModel) {
	type scope struct {
		dimmer           bool
		r, g, b          bool
		additive         []uint16
		source           ChannelFunctionSource
		instance, cellID string
	}
	scopes := map[string]*scope{"": {}}
	for _, c := range m.Cells {
		scopes[c.ID] = &scope{instance: c.ID, cellID: c.ID}
	}
	for _, p := range m.Parameters {
		sc := scopes[p.Cell]
		if sc == nil {
			continue
		}
		switch p.Attribute {
		case "Dimmer":
			sc.dimmer = true
		case "ColorAdd_R", "ColorRGB_Red":
			sc.r = true
		case "ColorAdd_G", "ColorRGB_Green":
			sc.g = true
		case "ColorAdd_B", "ColorRGB_Blue":
			sc.b = true
		}
		if isAdditiveColour(p.Attribute) {
			if len(sc.additive) == 0 {
				sc.source = p.Source
			}
			sc.additive = append(sc.additive, p.Offset)
		}
	}
	// J1b (owner 2026-10-12 20:11): a CELL gets a virtual dimmer only
	// when its PART (PartKey) has no real Dimmer — the JDC1's Plate Module
	// dimmer masters its plate pixels on the fixture, so they have none.
	realPart := map[string]bool{}
	for _, p := range m.Parameters {
		if p.Attribute == "Dimmer" && !p.Virtual {
			key, _ := PartKey(p.Instance)
			realPart[key] = true
		}
	}
	add := func(sc *scope, off uint16) {
		if sc.dimmer || !sc.r || !sc.g || !sc.b {
			return
		}
		if key, _ := PartKey(sc.instance); sc.cellID != "" && realPart[key] {
			return
		}
		m.Parameters = append(m.Parameters, ProgParameter{
			Offset: off, Offsets: make([]uint16, 0), ByteCount: 1, Max: 255, Attribute: "Dimmer", Group: GroupDimmer,
			Instance: sc.instance, Cell: sc.cellID, Source: sc.source, Detail: DetailVirtual, HasDefault: true, Default: 0,
			Functions: []ProgFunction{{Index: 0, Name: "Virtual dimmer", Attribute: "Dimmer", LogicalAttribute: "Dimmer",
				DMXFrom: 0, DMXTo: 255, HasDefault: true, Default: 0, Sets: make([]ProgSet, 0)}},
			Virtual: true, virtualOf: sc.additive,
		})
	}
	add(scopes[""], VirtualDimmerOffset)
	for _, c := range m.Cells {
		add(scopes[c.ID], VirtualDimmerOffset+uint16(c.Index))
	}
}

// PartKey is the part an intensity parameter on geometry instance inst
// belongs to, and the words its geometry name is made of — see PARTS in
// the file comment.
func PartKey(inst string) (key string, words []string) {
	for _, w := range strings.FieldsFunc(geometryName(inst), func(r rune) bool { return r == ' ' || r == '_' || r == '-' }) {
		digits := true
		for _, r := range w {
			digits = digits && r >= '0' && r <= '9'
		}
		if !digits {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return "", nil
	}
	return strings.ToLower(words[0]), words
}

// bodyParts are the GDTF body geometry names a master part may carry when
// a fixture has several candidates (PARTS).
var bodyParts = map[string]bool{"": true, "base": true, "body": true, "head": true, "yoke": true, "main": true}

// assignParts groups m's intensity parameters into parts and picks the
// master — see PARTS in the file comment.
func assignParts(m *FixtureModel) {
	idx := map[string]int{}
	names := map[string][]string{}
	cells := map[string]bool{}
	// J1b: a part holds cells when any cell's geometry is in it, dimmer or
	// not — the JDC1's plate pixels have no virtual dimmers now (their
	// Plate Module dimmer masters them), and the Plate is still a part.
	for _, c := range m.Cells {
		key, _ := PartKey(c.ID)
		cells[key] = true
	}
	for _, p := range m.Parameters {
		if p.Attribute != "Dimmer" {
			continue
		}
		key, words := PartKey(p.Instance)
		i, ok := idx[key]
		if !ok {
			i = len(m.Parts)
			idx[key] = i
			m.Parts = append(m.Parts, ProgPart{Key: key, Dimmers: make([]uint16, 0)})
			names[key] = words
		}
		m.Parts[i].Dimmers = append(m.Parts[i].Dimmers, p.Offset)
		// The common leading words of every geometry name in the part.
		common := names[key]
		n := 0
		for n < len(common) && n < len(words) && common[n] == words[n] {
			n++
		}
		names[key] = common[:n]
		if p.Cell != "" || p.Virtual {
			cells[key] = true
		}
	}
	// J1b: the part's name also spans its cells' geometry names (the
	// JDC1's Plate Module dimmer and its plate pixels = "Plate").
	for _, c := range m.Cells {
		key, words := PartKey(c.ID)
		if common, ok := names[key]; ok {
			n := 0
			for n < len(common) && n < len(words) && common[n] == words[n] {
				n++
			}
			names[key] = common[:n]
		}
	}
	candidates := make([]int, 0)
	for i := range m.Parts {
		pt := &m.Parts[i]
		pt.Name = strings.Join(names[pt.Key], " ")
		if pt.Name == "" {
			pt.Name = "Fixture"
		}
		if !cells[pt.Key] {
			candidates = append(candidates, i)
		}
	}
	master := -1
	if len(candidates) == 1 {
		master = candidates[0]
	} else {
		for _, i := range candidates {
			if bodyParts[m.Parts[i].Key] {
				if master >= 0 {
					master = -1
					break
				}
				master = i
			}
		}
	}
	if master >= 0 {
		m.Parts[master].Master = true
	}
}

// partOf is the index in m.Parts of the part p belongs to, -1 when p is
// not an intensity parameter.
func (m *FixtureModel) partOf(p *ProgParameter) int {
	if p.Attribute != "Dimmer" || p.Master {
		return -1
	}
	key, _ := PartKey(p.Instance)
	for i := range m.Parts {
		if m.Parts[i].Key == key {
			return i
		}
	}
	return -1
}

// hasMasterDimmer: the fixture has a MASTER part (PARTS; J1 — before J1
// any real Dimmer outside the cells counted, so the JDC1's part dimmers
// kept its plate pixels out of whole-fixture intensity) — then a
// whole-fixture intensity control drives it, not the cells' virtual
// dimmers (I2d4).
func (m *FixtureModel) hasMasterDimmer() bool {
	for _, pt := range m.Parts {
		if pt.Master {
			return true
		}
	}
	return false
}

// masterFor (J1) is the real Dimmer a cell's test opens so the cell can be
// seen: the fixture's master when it has one, else the real Dimmer outside
// the cells in the cell's own part (the JDC1's Plate Module dimmer for a
// plate pixel). nil: none.
func (m *FixtureModel) masterFor(cell string) *ProgParameter {
	want := -2
	if !m.hasMasterDimmer() {
		want = -1
		for i := range m.Parameters {
			if p := &m.Parameters[i]; p.Cell == cell && p.Instance != "" {
				key, _ := PartKey(p.Instance)
				for pi := range m.Parts {
					if m.Parts[pi].Key == key {
						want = pi
					}
				}
				break
			}
		}
		if want < 0 {
			return nil
		}
	}
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Attribute != "Dimmer" || p.Cell != "" || p.Virtual || len(p.Offsets) == 0 {
			continue
		}
		pi := m.partOf(p)
		if pi >= 0 && ((want == -2 && m.Parts[pi].Master) || pi == want) {
			return p
		}
	}
	return nil
}

// cellOpens (J1c, owner 2026-10-13: "Highlight on a single cell must open
// its fixture's master, and its part dimmer where that applies, the same
// way Tests do") is what a cell needs open to be seen besides its own
// channels: masterFor (the fixture's master channel, else its own part's
// dimmer), cellPartDimmer, and the virtual master — each once.
func (m *FixtureModel) cellOpens(cell string) map[*ProgParameter]bool {
	out := map[*ProgParameter]bool{}
	if cell == "" {
		return out
	}
	for _, p := range []*ProgParameter{m.masterFor(cell), m.cellPartDimmer(cell), m.virtualMaster()} {
		if p != nil {
			out[p] = true
		}
	}
	return out
}

// cellPartDimmer (J1b) is, for a cell with no Dimmer of its own (real or
// virtual — its part has a real dimmer, so it got no virtual one), the real
// Dimmer outside the cells in the cell's part (the JDC1's Plate Module
// dimmer for a plate pixel). nil: the cell has its own, or none is found.
func (m *FixtureModel) cellPartDimmer(cell string) *ProgParameter {
	key := "\x00"
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Cell != cell {
			continue
		}
		if p.Attribute == "Dimmer" {
			return nil
		}
		if key == "\x00" && p.Instance != "" {
			key, _ = PartKey(p.Instance)
		}
	}
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Attribute == "Dimmer" && p.Cell == "" && !p.Virtual && len(p.Offsets) > 0 {
			if k, _ := PartKey(p.Instance); k == key {
				return p
			}
		}
	}
	return nil
}

// inScope (I2d4) reports whether p is one of target t's channels for a
// control that acts by attribute (set, fan, command, the read model, group
// faders): a cell target is its own channels; a whole fixture is every
// channel, its cells' virtual dimmers included (J1b, owner 2026-10-12: the
// master drives every part; I2d4 left those out when it had a master).
//
// J1c (owner 2026-10-13: the Master multiplies the parts): for a fixture
// with a software master, a whole-fixture target's Dimmer is its MASTER —
// the real master channel and the virtual master — never the parts, which
// keep their own levels (part faders, cells).
func (m *FixtureModel) inScope(t ProgTarget, p *ProgParameter) bool {
	if t.Cell != "" {
		return p.Cell == t.Cell
	}
	if p.Attribute != "Dimmer" || !m.hasVirtualMaster() {
		return true
	}
	return p.Master || m.inMasterPart(p)
}

// hasVirtualMaster (J1c): m has several intensity parts, so a software
// master.
func (m *FixtureModel) hasVirtualMaster() bool { return m.virtualMaster() != nil }

// virtualMaster is m's virtual master parameter, nil when it has none.
func (m *FixtureModel) virtualMaster() *ProgParameter {
	for i := range m.Parameters {
		if m.Parameters[i].Master {
			return &m.Parameters[i]
		}
	}
	return nil
}

// inMasterPart: p is a real Dimmer of m's master part (its master channel).
func (m *FixtureModel) inMasterPart(p *ProgParameter) bool {
	pi := m.partOf(p)
	return pi >= 0 && m.Parts[pi].Master && !p.Virtual
}

// addVirtualMaster (J1c) gives a fixture with more than one intensity part
// its software master — see MASTER MULTIPLIER in the file comment. tree is
// the GDTF geometry tree (instance -> parent), nil when not known.
func addVirtualMaster(m *FixtureModel, tree map[string]string) {
	m.TreeKnown = tree != nil
	if len(m.Parts) < 2 {
		return
	}
	masterInst := map[string]bool{}
	hasMaster := false
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if m.inMasterPart(p) {
			hasMaster = true
			masterInst[p.Instance] = true
		}
	}
	// under: inst is (inside) a geometry holding the master channel.
	under := func(inst string) bool {
		if !hasMaster {
			return false
		}
		if tree == nil {
			return true // not known: the master governs (re-import to refine)
		}
		cur := inst
		for n := 0; n < 256 && cur != ""; n++ {
			if masterInst[cur] {
				return true
			}
			next, ok := tree[cur]
			if !ok {
				return false
			}
			cur = next
		}
		return false
	}
	var src ChannelFunctionSource
	masterOf := make([]uint16, 0)
	for pi := range m.Parts {
		pt := &m.Parts[pi]
		if pt.Master {
			pt.Governed = true
			continue
		}
		governed := true
		params := make([]*ProgParameter, 0)
		for i := range m.Parameters {
			if p := &m.Parameters[i]; m.partOf(p) == pi {
				params = append(params, p)
				if src == "" {
					src = p.Source
				}
				if !under(p.Instance) {
					governed = false
				}
			}
		}
		pt.Governed = governed
		if governed {
			continue
		}
		for _, p := range params {
			if p.Virtual {
				masterOf = append(masterOf, p.virtualOf...)
			} else {
				masterOf = append(masterOf, p.Offset)
			}
		}
	}
	m.Parameters = append(m.Parameters, ProgParameter{
		Offset: VirtualMasterOffset, Offsets: make([]uint16, 0), ByteCount: 1, Max: 255, Attribute: "Dimmer", Group: GroupDimmer,
		Source: src, Detail: DetailVirtual, HasDefault: true, Default: 0,
		Functions: []ProgFunction{{Index: 0, Name: "Master", Attribute: "Dimmer", LogicalAttribute: "Dimmer",
			DMXFrom: 0, DMXTo: 255, HasDefault: true, Default: 0, Sets: make([]ProgSet, 0)}},
		Virtual: true, Master: true, masterOf: masterOf,
	})
}

func (m FixtureModel) HasCell(id string) bool {
	for _, c := range m.Cells {
		if c.ID == id {
			return true
		}
	}
	return false
}

// bytesOf splits v into the parameter's bytes, coarse first (Table 58 byte
// order), paired with the offset each byte goes to.
func (p ProgParameter) bytesOf(v uint32) []byte {
	out := make([]byte, p.ByteCount)
	for i := 0; i < p.ByteCount; i++ {
		out[i] = byte(v >> (8 * uint(p.ByteCount-1-i)))
	}
	return out
}
