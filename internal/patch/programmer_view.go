package patch

// This file is the programmer's READ side for the UI (C6): the selection,
// and for it every attribute grouped by taxonomy group with its functions,
// each target channel's current value, and the mixed / touched /
// unknown-default flags.

// ProgSelected is one selected target with its display names.
type ProgSelected struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
	Name    string `json:"name"`
	// CellName/CellIndex describe Cell ("" / 0 for a whole fixture).
	CellName  string `json:"cellName"`
	CellIndex int    `json:"cellIndex"`
}

// ProgVariant is one distinct channel layout an attribute has across the
// selection (identical fixture types share one).
type ProgVariant struct {
	ByteCount int            `json:"byteCount"`
	Max       uint32         `json:"max"`
	Detail    string         `json:"detail"`
	Functions []ProgFunction `json:"functions"`
	// Virtual: a virtual dimmer (G1) — no channel of its own; it scales
	// the fixture's additive colour.
	Virtual bool `json:"virtual"`
}

// ProgChannelView is one target channel of an attribute.
type ProgChannelView struct {
	EntryID   string `json:"entryId"`
	Cell      string `json:"cell"`
	Offset    uint16 `json:"offset"`
	ByteCount int    `json:"byteCount"`
	// Value is the programmer value when Touched, else the default (0 when
	// DefaultKnown is false — then it is not a claim about the channel).
	Value        uint32 `json:"value"`
	Touched      bool   `json:"touched"`
	DefaultKnown bool   `json:"defaultKnown"`
	Default      uint32 `json:"default"`
	Variant      int    `json:"variant"`
	// Virtual: this channel is a virtual dimmer (G1).
	Virtual bool `json:"virtual"`
	// Function is the index (in its variant) of the function Value lies in,
	// -1 when that is not one function.
	Function int `json:"function"`
}

// ProgAttrView is one attribute across the selection.
type ProgAttrView struct {
	Attribute string `json:"attribute"`
	// Mixed: the channels do not all hold the same value at the same byte
	// count. Value is the shared value, null when Mixed.
	Mixed      bool    `json:"mixed"`
	Value      *uint32 `json:"value"`
	Touched    bool    `json:"touched"`
	AllTouched bool    `json:"allTouched"`
	// UnknownDefault: at least one untouched channel's default is unknown,
	// so the value shown for it (0) is not a claim.
	UnknownDefault bool `json:"unknownDefault"`
	// Missing is how many selected targets do not have this attribute.
	Missing  int               `json:"missing"`
	Variants []ProgVariant     `json:"variants"`
	Channels []ProgChannelView `json:"channels"`
}

// ProgGroupView is one attribute group.
type ProgGroupView struct {
	Group      AttributeGroup `json:"group"`
	Label      string         `json:"label"`
	Attributes []ProgAttrView `json:"attributes"`
}

// ProgRawOffsetView is one raw offset of a selected fixture.
type ProgRawOffsetView struct {
	Offset  uint16 `json:"offset"`
	Value   uint32 `json:"value"`
	Touched bool   `json:"touched"`
}

// ProgRawView is a selected fixture's raw offsets (whole-fixture targets
// only: raw offsets belong to no cell).
type ProgRawView struct {
	EntryID string              `json:"entryId"`
	Offsets []ProgRawOffsetView `json:"offsets"`
}

// ProgView is GET /api/programmer.
type ProgView struct {
	Revision  uint64          `json:"revision"`
	Output    ProgOutput      `json:"output"`
	Selection []ProgSelected  `json:"selection"`
	Groups    []ProgGroupView `json:"groups"`
	Raw       []ProgRawView   `json:"raw"`
	// Touched is how many channels the whole programmer holds.
	Touched int `json:"touched"`
	// Highlight is the Highlight/Lowlight state and coverage (C4b).
	Highlight ProgHighlightView `json:"highlight"`
	// Commands: one-shot commands in flight and the last that ended (I2d2).
	Commands ProgCommandsView `json:"commands"`
}

// View builds the read model.
func (pg *Programmer) View() ProgView {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	v := ProgView{Revision: pg.revision, Output: pg.outputLocked(), Selection: make([]ProgSelected, 0, len(pg.selection)),
		Groups: make([]ProgGroupView, 0), Raw: make([]ProgRawView, 0), Touched: len(pg.values)}
	_, _, _, v.Highlight = pg.overlayLocked()
	v.Commands = pg.commandsLocked()
	type attrAcc struct {
		view     *ProgAttrView
		variants map[string]int
		targets  map[ProgTarget]bool
	}
	byGroup := map[AttributeGroup][]string{}
	accs := map[string]*attrAcc{}
	seen := map[progKey]bool{}
	for _, t := range pg.selection {
		m := pg.models[t.EntryID]
		if m == nil {
			continue
		}
		sel := ProgSelected{EntryID: t.EntryID, Cell: t.Cell, Name: m.Name}
		for _, c := range m.Cells {
			if c.ID == t.Cell {
				sel.CellName, sel.CellIndex = c.Name, c.Index
			}
		}
		v.Selection = append(v.Selection, sel)
		for i := range m.Parameters {
			p := &m.Parameters[i]
			if !m.inScope(t, p) {
				continue
			}
			acc := accs[p.Attribute]
			if acc == nil {
				acc = &attrAcc{view: &ProgAttrView{Attribute: p.Attribute, Variants: make([]ProgVariant, 0), Channels: make([]ProgChannelView, 0)},
					variants: map[string]int{}, targets: map[ProgTarget]bool{}}
				accs[p.Attribute] = acc
				byGroup[p.Group] = append(byGroup[p.Group], p.Attribute)
			}
			acc.targets[t] = true
			k := progKey{t.EntryID, p.Offset}
			if seen[k] {
				continue
			}
			seen[k] = true
			vi, ok := acc.variants[p.variantKey]
			if !ok {
				vi = len(acc.view.Variants)
				acc.variants[p.variantKey] = vi
				acc.view.Variants = append(acc.view.Variants, ProgVariant{ByteCount: p.ByteCount, Max: p.Max, Detail: p.Detail, Functions: p.Functions, Virtual: p.Virtual})
			}
			cv := ProgChannelView{EntryID: t.EntryID, Cell: p.Cell, Offset: p.Offset, ByteCount: p.ByteCount,
				DefaultKnown: p.HasDefault, Default: p.Default, Variant: vi, Virtual: p.Virtual}
			if val, ok := pg.values[k]; ok {
				cv.Value, cv.Touched = val, true
			} else {
				cv.Value = p.Default
			}
			cv.Function = activeFunction(p, cv.Value)
			acc.view.Channels = append(acc.view.Channels, cv)
		}
		if t.Cell == "" && len(m.RawOffsets) > 0 {
			rv := ProgRawView{EntryID: t.EntryID, Offsets: make([]ProgRawOffsetView, 0, len(m.RawOffsets))}
			for _, off := range m.RawOffsets {
				o := ProgRawOffsetView{Offset: off}
				o.Value, o.Touched = pg.values[progKey{t.EntryID, off}]
				rv.Offsets = append(rv.Offsets, o)
			}
			v.Raw = append(v.Raw, rv)
		}
	}
	for _, g := range AllGroups {
		names := byGroup[g]
		if len(names) == 0 {
			continue
		}
		gv := ProgGroupView{Group: g, Label: g.String(), Attributes: make([]ProgAttrView, 0, len(names))}
		for _, name := range names {
			acc := accs[name]
			av := acc.view
			av.Missing = len(v.Selection) - len(acc.targets)
			av.AllTouched = len(av.Channels) > 0
			for i, c := range av.Channels {
				av.Touched = av.Touched || c.Touched
				av.AllTouched = av.AllTouched && c.Touched
				av.UnknownDefault = av.UnknownDefault || (!c.Touched && !c.DefaultKnown)
				if i > 0 && (c.Value != av.Channels[0].Value || c.ByteCount != av.Channels[0].ByteCount) {
					av.Mixed = true
				}
			}
			if !av.Mixed && len(av.Channels) > 0 {
				val := av.Channels[0].Value
				av.Value = &val
			}
			gv.Attributes = append(gv.Attributes, *av)
		}
		v.Groups = append(v.Groups, gv)
	}
	return v
}

// Selection returns the current selection.
func (pg *Programmer) Selection() []ProgTarget {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return append(make([]ProgTarget, 0, len(pg.selection)), pg.selection...)
}

// Output reports whether programmer values reach the wire.
func (pg *Programmer) Output() ProgOutput {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.outputLocked()
}

// Fixtures returns every entry's parameter model in patch order.
func (pg *Programmer) Fixtures() []FixtureModel {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	out := make([]FixtureModel, 0, len(pg.order))
	for _, id := range pg.order {
		out = append(out, *pg.models[id])
	}
	return out
}
