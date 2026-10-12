package patch

import (
	"fmt"
	"math"
	"strings"
	"time"

	"benny512/internal/session"
)

const MaxPatternFade = 30 * time.Second

var errFadeRange = fmt.Errorf("fade time must be between 0 and 30 seconds")

// Fade time belongs to the operator's session, survives Stop/show changes,
// and only affects transitions started after it is changed.
func (r *RigCheck) SetPatternFade(d time.Duration) (PatternStatus, error) {
	if d < 0 || d > MaxPatternFade {
		return PatternStatus{}, errFadeRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.patternFadeTime = d
	return r.patternStatusLocked(), nil
}

type patternUnitKey struct {
	universe     uint16
	coarse, fine int // 1-based absolute slots; fine=0 means 8-bit
}

// A source changes only on an edit, not on each waveform sample. Keeping it
// per function prevents a dimmer edit from interrupting an unrelated ballyhoo.
type patternSource struct {
	entry     string
	spec      PatternSpec
	phase     float64
	baseValue uint32
	removed   bool
}

type patternUnit struct {
	key    patternUnitKey
	source patternSource
	fade   bool
}

type patternFade struct {
	from     uint32
	start    time.Time
	duration time.Duration
}

// Only continuous functions interpolate. Sweeping through shutter, wheel
// selection or control ranges can select entirely different functions.
func continuousPatternAttribute(attr string) bool {
	if strings.Contains(attr, "Speed") || strings.Contains(attr, "Rotate") || strings.Contains(attr, "Spin") {
		return false
	}
	return strings.HasPrefix(attr, "Dimmer") || attr == "Pan" || attr == "Tilt" ||
		attr == "Focus" || attr == "Zoom" || strings.HasPrefix(attr, "Frost") ||
		strings.HasPrefix(attr, "ColorAdd_") || strings.HasPrefix(attr, "ColorSub_") ||
		strings.HasPrefix(attr, "Blade") || strings.HasPrefix(attr, "Shaper")
}

// Track the actual winning writer at each slot, including coarse/fine pairs.
// A partially overwritten pair is kept discrete rather than interpolating
// bytes that now belong to different functions.
func (c *patternComposition) noteUnit(universe, start uint16, offsets []uint16, nbytes int, source patternSource, fade bool) {
	keys := []patternUnitKey{}
	if nbytes >= 2 && len(offsets) >= 2 {
		keys = append(keys, patternUnitKey{universe, int(start) + int(offsets[0]) - 1, int(start) + int(offsets[1]) - 1})
	} else {
		for _, off := range offsets {
			keys = append(keys, patternUnitKey{universe, int(start) + int(off) - 1, 0})
		}
	}
	for _, k := range keys {
		if k.coarse < 1 || k.coarse > 512 || k.fine < 0 || k.fine > 512 || k.fine == k.coarse {
			continue
		}
		u := patternUnit{key: k, source: source, fade: fade}
		c.writers[slotKey(universe, k.coarse)] = u
		if k.fine != 0 {
			c.writers[slotKey(universe, k.fine)] = u
		}
	}
}

func (c patternComposition) units() map[patternUnitKey]patternUnit {
	units := map[patternUnitKey]patternUnit{}
	for _, u := range c.writers {
		k := u.key
		if c.writers[slotKey(k.universe, k.coarse)] != u {
			continue
		}
		if k.fine != 0 && c.writers[slotKey(k.universe, k.fine)] != u {
			continue
		}
		units[k] = u
	}
	return units
}

func unitValue(frames map[uint16][]byte, k patternUnitKey) uint32 {
	f := frames[k.universe]
	if len(f) < k.coarse || len(f) < k.fine {
		return 0
	}
	v := uint32(f[k.coarse-1])
	if k.fine != 0 {
		v = v<<8 | uint32(f[k.fine-1])
	}
	return v
}

func setUnitValue(frames map[uint16][]byte, k patternUnitKey, value uint32) {
	f := frames[k.universe]
	if k.fine != 0 {
		f[k.coarse-1], f[k.fine-1] = byte(value>>8), byte(value)
	} else {
		f[k.coarse-1] = byte(value)
	}
}

// Start/newly scoped fixtures begin at profile defaults (position falls back
// to the existing neutral centre), with dimmers at zero. This is a commanded
// starting state, not a claim to know a fixture's physical position.
func (r *RigCheck) patternStartFramesLocked() map[uint16][]byte {
	frames := map[uint16][]byte{}
	for _, bs := range r.selection.base {
		if frames[bs.universe] == nil {
			frames[bs.universe] = make([]byte, session.DMXUniverseSize)
		}
		f := frames[bs.universe]
		for _, writes := range [][]baseWrite{bs.defaults, bs.position} {
			for _, w := range writes {
				writeFuncValue(f, bs.startAddr, w.offsets, w.nbytes, w.raw)
			}
		}
		for _, w := range bs.dimmer {
			writeFuncValue(f, bs.startAddr, w.offsets, w.nbytes, 0)
		}
	}
	return frames
}

func (r *RigCheck) fadePatternLocked(comp patternComposition, now time.Time) {
	units := comp.units()
	if r.patternFades == nil {
		r.patternFades = map[patternUnitKey]patternFade{}
	}
	// Include every previously owned universe so leaving scope cannot strand
	// its old levels. Stop still zeros all of them immediately.
	for raw := range r.started {
		if comp.frames[raw] == nil {
			comp.frames[raw] = make([]byte, session.DMXUniverseSize)
		}
	}
	for k, old := range r.patternUnits {
		if _, exists := units[k]; exists {
			continue
		}
		if _, taken := comp.writers[slotKey(k.universe, k.coarse)]; taken {
			continue
		}
		if k.fine != 0 {
			if _, taken := comp.writers[slotKey(k.universe, k.fine)]; taken {
				continue
			}
		}
		_, fading := r.patternFades[k]
		if old.source.removed && !fading {
			continue
		}
		old.source = patternSource{entry: old.source.entry, removed: true}
		units[k] = old
	}
	previousEntries := map[string]bool{}
	for _, u := range r.patternUnits {
		previousEntries[u.source.entry] = true
	}
	var initial map[uint16][]byte
	for k, u := range units {
		old, exists := r.patternUnits[k]
		if !exists || old.source != u.source || old.fade != u.fade {
			delete(r.patternFades, k)
			if u.fade && r.patternFadeTime > 0 {
				from := unitValue(r.patternFrames, k)
				if !previousEntries[u.source.entry] {
					if initial == nil {
						initial = r.patternStartFramesLocked()
					}
					from = unitValue(initial, k)
				}
				r.patternFades[k] = patternFade{from: from, start: now, duration: r.patternFadeTime}
			}
		}
		if f, ok := r.patternFades[k]; ok {
			progress := float64(now.Sub(f.start)) / float64(f.duration)
			if progress >= 1 {
				delete(r.patternFades, k)
				continue
			}
			progress = math.Max(0, progress)
			to := unitValue(comp.frames, k)
			value := uint32(math.Round(float64(f.from) + (float64(to)-float64(f.from))*progress))
			setUnitValue(comp.frames, k, value)
		}
	}
	for k := range r.patternFades {
		if _, ok := units[k]; !ok {
			delete(r.patternFades, k)
		}
	}
	for _, u := range r.virtualUnits {
		previousEntries[u.source.entry] = true
	}
	r.patternUnits, r.patternFrames = units, comp.frames
	r.fadeVirtualLocked(comp, now, previousEntries)
}

// virtualFade is one virtual dimmer fade in progress (I2g): its level and
// its fill level (session.VirtualDimmer) move together from where they were.
type virtualFade struct {
	fromLevel, fromFill byte
	start               time.Time
	duration            time.Duration
}

// target is what u shows once any fade is done: (level, fill level). A
// test's level L is (L, L); a white claim is (255, 255) — no set colour is
// scaled, an unset one is white. A key no test drives any more is
// (255, 0), which is exactly untouched — a set colour unscaled, an unset
// one dark — so its release at the end of the fade changes nothing on the
// wire (I2g).
func (u virtualUnit) target() (level, fill byte) {
	if u.source.removed {
		return 255, 0
	}
	return u.level, u.level
}

// fadeVirtualLocked (I2e) applies the slot rules above to the virtual dimmer
// levels the tests set, by engine key, and leaves the result in
// r.patternVirtual and r.patternFill: a changed writer fades from what was
// showing — the last published level and fill, untouched for a key no test
// drove, or 0 for a target new to the tests (dimmers start at zero) — and a
// key no test drives any more fades back to untouched, then is released.
// I2g: untouched is (255, 0), not full: with no colour set it is dark, so
// a test's level, or a white claim's white, rises from dark and falls to
// dark over the fade time, held until the fade's end — never via full
// white (the G1 fill rule applies to any claim, so fading to level 255
// alone showed full white and then dropped to dark on release).
func (r *RigCheck) fadeVirtualLocked(comp patternComposition, now time.Time, previousEntries map[string]bool) {
	if r.virtualFades == nil {
		r.virtualFades = map[string]virtualFade{}
	}
	units := make(map[string]virtualUnit, len(comp.virtual))
	for k, u := range comp.virtual {
		units[k] = u
	}
	for k, old := range r.virtualUnits {
		if _, ok := units[k]; ok {
			continue
		}
		if _, fading := r.virtualFades[k]; old.source.removed && !fading {
			continue
		}
		units[k] = virtualUnit{level: 255, source: patternSource{entry: old.source.entry, removed: true}, fill: old.fill}
	}
	levels := make(map[string]byte, len(units))
	fills := make(map[string]byte, len(units))
	for k, u := range units {
		level, fill := u.target()
		if old, exists := r.virtualUnits[k]; !exists || old.source != u.source || old.fill != u.fill {
			delete(r.virtualFades, k)
			if r.patternFadeTime > 0 {
				fromLevel, shown := r.patternVirtual[k]
				fromFill := r.patternFill[k]
				if !shown {
					fromLevel, fromFill = 255, 0 // untouched
					if !u.fill && !previousEntries[u.source.entry] {
						fromLevel, fromFill = 0, 0
					}
				}
				r.virtualFades[k] = virtualFade{fromLevel: fromLevel, fromFill: fromFill, start: now, duration: r.patternFadeTime}
			}
		}
		if f, ok := r.virtualFades[k]; ok {
			progress := float64(now.Sub(f.start)) / float64(f.duration)
			if progress >= 1 {
				delete(r.virtualFades, k)
			} else {
				progress = math.Max(0, progress)
				level = byte(math.Round(float64(f.fromLevel) + (float64(level)-float64(f.fromLevel))*progress))
				fill = byte(math.Round(float64(f.fromFill) + (float64(fill)-float64(f.fromFill))*progress))
			}
		}
		levels[k], fills[k] = level, fill
	}
	for k := range r.virtualFades {
		if _, ok := units[k]; !ok {
			delete(r.virtualFades, k)
		}
	}
	r.virtualUnits, r.patternVirtual, r.patternFill = units, levels, fills
}

// Live scope changes must validate and start new universes before committing
// the selection; previously the Art-Net path could write an unstarted buffer
// and the sACN path silently ignored it.
func (r *RigCheck) preparePatternScopeLocked(entries []Entry) error {
	if !r.patternOutput {
		return nil
	}
	raws := scopeUniverses(entries)
	for _, raw := range raws {
		if r.started[raw] {
			continue
		}
		if err := r.out.startUniverse(raw); err != nil {
			r.stopLocked("scope change failed")
			return err
		}
		r.started[raw] = true
	}
	return nil
}
