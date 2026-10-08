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
)

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
	// channelName is the GDTF DMXChannel name a ModeMaster link starts
	// with: geometry name + "_" + first logical channel attribute (DIN SPEC
	// 15800, DMX Channel: the name is derived, not stored).
	channelName string
	// variantKey identifies the channel layout (bytes, detail, functions)
	// so the read model can group identical fixture types without
	// re-encoding them on every read.
	variantKey string
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
	EntryID       string `json:"entryId"`
	Name          string `json:"name"`
	FixtureType   string `json:"fixtureType"`
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
		EntryID: e.ID, Name: e.Name, FixtureType: e.FixtureType, Mode: e.Mode, FixtureNumber: e.FixtureNumber,
		Universe: e.Universe, StartAddress: e.StartAddress, Footprint: e.Footprint,
		Profiled: len(e.ChannelFunctions) > 0, Cells: make([]ProgCell, 0),
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
	firstOnly := 0
	for i := range m.Parameters {
		p := &m.Parameters[i]
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
	}{e.Footprint, m.Parameters, m.RawOffsets, m.Cells})
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

// HasCell reports whether id is one of m's cells.
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
