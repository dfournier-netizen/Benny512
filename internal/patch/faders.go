// This file is Console-lite chunk G2: GROUP FADERS, server side. Owner
// decisions 2026-10-08: each fader SETS the dimmer level 0-100% of its
// fixtures (not a scaling master); it sits below the programmer; there is
// one fader per fixture type + mode automatically (two modes of one type =
// two faders) plus one per stored group. Orchestrator fill-ins, implemented
// here:
//
//   - An untouched fader claims nothing. A moved fader claims the Dimmer
//     channels of its members until it is released (one fader, or all).
//     A whole-fixture member is every Dimmer of the fixture (all its
//     cells', virtual dimmers included — J1b; I2d4 left those out under a
//     real master — FixtureModel.inScope); a cell member is that cell's
//     Dimmer only. A member without
//     a real Dimmer uses its G1 virtual dimmer(s); a member with neither is
//     listed as not controllable on that fader, never skipped silently.
//   - A channel in two moved faders follows the MOST RECENTLY MOVED one
//     (LTP, per dimmer channel); releasing it hands the channel back to the
//     next most recent moved fader claiming it, else lets it go.
//   - A real dimmer takes round(level x its full range) — a 16-bit Dimmer
//     maps over 0..65535; a virtual dimmer round(level x 255).
//   - Output goes on session.SourceGroupFaders (base < tests < group faders
//     < programmer) and its virtual dimmer levels under the same source, so
//     the master Arm gates it like everything else.
//   - State is in memory, shared by every browser, with its own revision
//     (X-Benny-Faders). A show switch clears it. Membership is derived from
//     the current patch on every apply, so a removed fixture or a changed
//     profile can never leave a level on a channel that is no longer that
//     fixture's dimmer; a fader whose type or group is gone is dropped with
//     its claim.
//
// J1 (owner 2026-10-12, Dom's bench test): SUB-FIXTURE FADERS. A fixture
// type + mode whose fixtures have more than one intensity PART
// (FixtureModel.Parts, programmer_model.go) gets, right after its type
// fader, one PART fader per part that is not the fixture's master,
// in part order, labelled with the part's GDTF-derived name. The type
// fader is then the type's MASTER and, like every whole-fixture intensity
// control, drives EVERY part (J1b, owner 2026-10-12 20:11: "the Master
// overrides the subs") — the real master channel if there is one (Rayzor
// 1960: the head) plus every other part (its RGBW pixels' virtual dimmers
// and SparkLED; GLP JDC1: Beam and Plate). A part fader claims every
// Dimmer, real or virtual, of its part in each member. A type with one part keeps its one fader,
// unchanged; stored-group faders are unchanged.
// Master and parts are ordinary faders: where both claim a channel the
// MOST RECENTLY MOVED wins (the LTP rule above), and releasing one hands
// its channels back to the other if it is still moved.
package patch

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"benny512/internal/session"
)

// ErrFadersStale: the client's X-Benny-Faders revision is not the current
// one.
var ErrFadersStale = errors.New("The faders changed in another browser since this one last looked; refresh and try again.")

// ErrFaderUnknown: no fader has that id in the current show.
type ErrFaderUnknown struct{ ID string }

func (e ErrFaderUnknown) Error() string {
	return fmt.Sprintf("There is no fader %q in this show; refresh the faders.", e.ID)
}

// FaderGroup is a stored group as the faders see it.
type FaderGroup struct {
	ID      string
	Name    string
	Members []ProgTarget
}

type faderClaim struct {
	level float64
	seq   uint64
}

// Fader kinds.
const (
	FaderKindType  = "type"
	FaderKindGroup = "group"
	// FaderKindPart (J1): one intensity part of a fixture type + mode.
	FaderKindPart = "part"
)

// FaderView is one fader in GET /api/faders.
type FaderView struct {
	// ID is "type:<fixtureType>|<mode>", "group:<groupId>" or (J1)
	// "part:<fixtureType>|<mode>|<partKey>".
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// HasParts (J1): a type fader that is its type's MASTER, followed by
	// its part faders. Parent and Part: a part fader's type fader id and
	// part key ("" otherwise; a part key may itself be "").
	HasParts bool   `json:"hasParts"`
	Parent   string `json:"parent"`
	Part     string `json:"part"`
	// ShortLabel (J1b, owner 2026-10-12 20:11) is what the bar shows: a
	// type fader's short type name (ShortTypeName), with its mode's short
	// form when the type is patched in several modes ("<n>ch" when their
	// footprints differ, else the mode name); a part fader carries its
	// type's; a group fader its label. Label stays the full name (the
	// accessible label and tooltip).
	ShortLabel string `json:"shortLabel"`
	// Count is how many members (fixtures, or cells for a group's cell
	// members) the fader has; Controllable how many of them it can dim.
	Count           int      `json:"count"`
	Controllable    int      `json:"controllable"`
	NotControllable []string `json:"notControllable"`
	// Level is 0..1, null while untouched.
	Level   *float64 `json:"level"`
	Touched bool     `json:"touched"`
	// LastMovedOrder ranks moved faders: higher = moved more recently; 0 =
	// untouched. It is the LTP order, not a clock time.
	LastMovedOrder uint64 `json:"lastMovedOrder"`
}

// FadersView is GET /api/faders.
type FadersView struct {
	Revision uint64      `json:"revision"`
	Output   ProgOutput  `json:"output"`
	Faders   []FaderView `json:"faders"`
}

type faderDef struct {
	view    FaderView
	members []ProgTarget
}

// dimmers is what fader d claims of member t (J1: a part fader its part;
// J1b: a type Master, like a group member, the whole fixture — every part).
func (d *faderDef) dimmers(m *FixtureModel, t ProgTarget) []*ProgParameter {
	if d.view.Kind == FaderKindPart {
		return partDimmers(m, d.view.Part)
	}
	return dimmersOf(m, t)
}

// partDimmers is every Dimmer (real or virtual) of m's part key.
func partDimmers(m *FixtureModel, key string) []*ProgParameter {
	out := make([]*ProgParameter, 0)
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if pi := m.partOf(p); pi >= 0 && m.Parts[pi].Key == key {
			out = append(out, p)
		}
	}
	return out
}

// dimmersOf is t's Dimmer channels (real or virtual) — see the file comment.
func dimmersOf(m *FixtureModel, t ProgTarget) []*ProgParameter {
	out := make([]*ProgParameter, 0)
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Attribute == "Dimmer" && m.inScope(t, p) {
			out = append(out, p)
		}
	}
	return out
}

func (pg *Programmer) memberLabel(t ProgTarget) string {
	m := pg.models[t.EntryID]
	name := fixtureLabel(m)
	for _, c := range m.Cells {
		if c.ID == t.Cell {
			return fmt.Sprintf("%s %s", name, c.Name)
		}
	}
	return name
}

// fadersLocked derives the current faders: type faders in patch order,
// then stored-group faders in stored order.
func (pg *Programmer) fadersLocked() []faderDef {
	out := make([]faderDef, 0)
	modes := map[string]map[string]bool{}
	feet := map[string]map[uint16]bool{} // J1b: the type's mode footprints
	for _, id := range pg.order {
		m := pg.models[id]
		if modes[m.FixtureType] == nil {
			modes[m.FixtureType] = map[string]bool{}
			feet[m.FixtureType] = map[uint16]bool{}
		}
		if !modes[m.FixtureType][m.Mode] {
			feet[m.FixtureType][m.Footprint] = true
		}
		modes[m.FixtureType][m.Mode] = true
	}
	idx := map[string]int{}
	for _, id := range pg.order {
		m := pg.models[id]
		fid := "type:" + m.FixtureType + "|" + m.Mode
		i, ok := idx[fid]
		if !ok {
			label, short := m.FixtureType, m.ShortType
			if label == "" {
				label, short = "No fixture type", "No fixture type"
			}
			if n := len(modes[m.FixtureType]); n > 1 {
				label += " — " + m.Mode
				if len(feet[m.FixtureType]) == n {
					short += fmt.Sprintf(" — %dch", m.Footprint)
				} else {
					short += " — " + m.Mode
				}
			}
			i = len(out)
			idx[fid] = i
			out = append(out, faderDef{view: FaderView{ID: fid, Kind: FaderKindType, Label: label, ShortLabel: short}})
		}
		out[i].members = append(out[i].members, ProgTarget{EntryID: id})
	}
	// J1: each type whose fixtures have several intensity parts is followed
	// by one fader per part that is not the master (parts of its first
	// fixture; every member has the same type + mode).
	withParts := make([]faderDef, 0, len(out))
	for _, d := range out {
		parts := pg.models[d.members[0].EntryID].Parts
		if len(parts) > 1 {
			d.view.HasParts = true
		}
		withParts = append(withParts, d)
		if !d.view.HasParts {
			continue
		}
		rest := strings.TrimPrefix(d.view.ID, "type:")
		for _, pt := range parts {
			if pt.Master {
				continue
			}
			withParts = append(withParts, faderDef{view: FaderView{ID: "part:" + rest + "|" + pt.Key, Kind: FaderKindPart,
				Label: pt.Name, Parent: d.view.ID, Part: pt.Key, ShortLabel: d.view.ShortLabel}, members: d.members})
		}
	}
	out = withParts
	for _, g := range pg.faderGroups {
		d := faderDef{view: FaderView{ID: "group:" + g.ID, Kind: FaderKindGroup, Label: g.Name, ShortLabel: g.Name}}
		for _, t := range g.Members {
			if m := pg.models[t.EntryID]; m != nil && (t.Cell == "" || m.HasCell(t.Cell)) {
				d.members = append(d.members, t)
			}
		}
		out = append(out, d)
	}
	for i := range out {
		v := &out[i].view
		v.NotControllable = make([]string, 0)
		v.Count = len(out[i].members)
		for _, t := range out[i].members {
			if len(out[i].dimmers(pg.models[t.EntryID], t)) > 0 {
				v.Controllable++
			} else {
				v.NotControllable = append(v.NotControllable, pg.memberLabel(t))
			}
		}
		if c, ok := pg.faderClaims[v.ID]; ok {
			lv := c.level
			v.Level, v.Touched, v.LastMovedOrder = &lv, true, c.seq
		}
	}
	return out
}

// applyFadersLocked writes the moved faders to the engine, LTP per channel.
func (pg *Programmer) applyFadersLocked(defs []faderDef) {
	moved := make([]faderDef, 0)
	for _, d := range defs {
		if d.view.Touched {
			moved = append(moved, d)
		}
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i].view.LastMovedOrder < moved[j].view.LastMovedOrder })
	type assign struct {
		p     *ProgParameter
		level float64
	}
	chans := map[progKey]assign{}
	keys := make([]progKey, 0)
	for _, d := range moved {
		for _, t := range d.members {
			for _, p := range d.dimmers(pg.models[t.EntryID], t) {
				k := progKey{t.EntryID, p.Offset}
				if _, seen := chans[k]; !seen {
					keys = append(keys, k)
				}
				chans[k] = assign{p, *d.view.Level}
			}
		}
	}
	frames := map[uint16]session.LayerFrame{}
	levels := map[string]byte{}
	for _, k := range keys {
		a := chans[k]
		if a.p.Virtual {
			levels[VirtualDimmerKey(k.entry, a.p.Offset)] = byte(math.Round(a.level * 255))
			continue
		}
		e := pg.entries[k.entry]
		v := uint32(math.Round(a.level * float64(a.p.Max)))
		for i, b := range a.p.bytesOf(v) {
			off := a.p.Offsets[i]
			if !e.addressable(off) {
				continue
			}
			f := frames[e.Universe]
			slot := int(e.StartAddress) + int(off) - 2
			f.Values[slot], f.Owned[slot] = b, true
			frames[e.Universe] = f
		}
	}
	_ = pg.dmx.ReplaceSource(session.SourceGroupFaders, frames)
	_ = pg.dmx.SetVirtualDimmerLevels(session.SourceGroupFaders, levels)
}

// refreshFadersLocked re-derives the faders after the patch or the stored
// groups changed (clear: a show switch drops every claim), drops claims of
// faders that are gone, re-applies, and bumps the fader revision when
// anything GET /api/faders shows changed.
func (pg *Programmer) refreshFadersLocked(clear bool) {
	changed := false
	if clear && len(pg.faderClaims) > 0 {
		pg.faderClaims = map[string]faderClaim{}
		changed = true
	}
	defs := pg.fadersLocked()
	live := map[string]bool{}
	for _, d := range defs {
		live[d.view.ID] = true
	}
	for id := range pg.faderClaims {
		if !live[id] {
			delete(pg.faderClaims, id)
			changed = true
		}
	}
	if changed {
		defs = pg.fadersLocked()
	}
	var b strings.Builder
	for _, d := range defs {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%d\x00%d\x00%s\n", d.view.ID, d.view.Label, d.view.ShortLabel, d.view.Count, d.view.Controllable, strings.Join(d.view.NotControllable, "\x01"))
	}
	if b.String() != pg.faderDigest {
		pg.faderDigest = b.String()
		changed = true
	}
	pg.applyFadersLocked(defs)
	if changed {
		pg.faderRev++
	}
}

// SetFaderGroups replaces the stored groups the faders follow. Call it
// before Sync (which re-derives and applies the faders).
func (pg *Programmer) SetFaderGroups(groups []FaderGroup) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.faderGroups = groups
}

// FadersRevision is the current fader revision.
func (pg *Programmer) FadersRevision() uint64 {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.faderRev
}

// Faders is GET /api/faders.
func (pg *Programmer) Faders() FadersView {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.fadersViewLocked()
}

func (pg *Programmer) fadersViewLocked() FadersView {
	v := FadersView{Revision: pg.faderRev, Output: pg.outputLocked(), Faders: make([]FaderView, 0)}
	for _, d := range pg.fadersLocked() {
		v.Faders = append(v.Faders, d.view)
	}
	return v
}

func (pg *Programmer) checkFadersLocked(expected *uint64, id string) error {
	if expected != nil && *expected != pg.faderRev {
		return ErrFadersStale
	}
	if id == "" {
		return nil
	}
	for _, d := range pg.fadersLocked() {
		if d.view.ID == id {
			return nil
		}
	}
	return ErrFaderUnknown{ID: id}
}

// SetFader moves fader id to level (0..1): it becomes the most recently
// moved fader.
func (pg *Programmer) SetFader(id string, level float64, expected *uint64) (FadersView, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkFadersLocked(expected, id); err != nil {
		return FadersView{}, err
	}
	if math.IsNaN(level) || level < 0 || level > 1 {
		return FadersView{}, reqErr("A fader level is a fraction from 0 to 1.")
	}
	pg.faderSeq++
	pg.faderClaims[id] = faderClaim{level: level, seq: pg.faderSeq}
	pg.applyFadersLocked(pg.fadersLocked())
	pg.faderRev++
	return pg.fadersViewLocked(), nil
}

// ReleaseFader releases fader id ("" = every fader). changed is false when
// nothing was claimed (no revision bump).
func (pg *Programmer) ReleaseFader(id string, expected *uint64) (FadersView, bool, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkFadersLocked(expected, id); err != nil {
		return FadersView{}, false, err
	}
	changed := false
	if id == "" {
		changed = len(pg.faderClaims) > 0
		pg.faderClaims = map[string]faderClaim{}
	} else if _, ok := pg.faderClaims[id]; ok {
		delete(pg.faderClaims, id)
		changed = true
	}
	if changed {
		pg.applyFadersLocked(pg.fadersLocked())
		pg.faderRev++
	}
	return pg.fadersViewLocked(), changed, nil
}
