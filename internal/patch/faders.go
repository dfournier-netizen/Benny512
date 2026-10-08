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
//     cells'); a cell member is that cell's Dimmer only. A member without
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
)

// FaderView is one fader in GET /api/faders.
type FaderView struct {
	// ID is "type:<fixtureType>|<mode>" or "group:<groupId>".
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
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

// dimmersOf is t's Dimmer channels (real or virtual) — see the file comment.
func dimmersOf(m *FixtureModel, t ProgTarget) []*ProgParameter {
	out := make([]*ProgParameter, 0)
	for i := range m.Parameters {
		p := &m.Parameters[i]
		if p.Attribute == "Dimmer" && (t.Cell == "" || p.Cell == t.Cell) {
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
	for _, id := range pg.order {
		m := pg.models[id]
		if modes[m.FixtureType] == nil {
			modes[m.FixtureType] = map[string]bool{}
		}
		modes[m.FixtureType][m.Mode] = true
	}
	idx := map[string]int{}
	for _, id := range pg.order {
		m := pg.models[id]
		fid := "type:" + m.FixtureType + "|" + m.Mode
		i, ok := idx[fid]
		if !ok {
			label := m.FixtureType
			if label == "" {
				label = "No fixture type"
			}
			if len(modes[m.FixtureType]) > 1 {
				label += " — " + m.Mode
			}
			i = len(out)
			idx[fid] = i
			out = append(out, faderDef{view: FaderView{ID: fid, Kind: FaderKindType, Label: label}})
		}
		out[i].members = append(out[i].members, ProgTarget{EntryID: id})
	}
	for _, g := range pg.faderGroups {
		d := faderDef{view: FaderView{ID: "group:" + g.ID, Kind: FaderKindGroup, Label: g.Name}}
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
			if len(dimmersOf(pg.models[t.EntryID], t)) > 0 {
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
			for _, p := range dimmersOf(pg.models[t.EntryID], t) {
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
		fmt.Fprintf(&b, "%s\x00%s\x00%d\x00%d\x00%s\n", d.view.ID, d.view.Label, d.view.Count, d.view.Controllable, strings.Join(d.view.NotControllable, "\x01"))
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
