package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Show-file profile cache (schema v8, Console-lite C1b, owner decision
// 2026-10-07: "cache the profile per fixture type and point each entry at
// it").
//
// WHY. Full GDTF channel detail (schema v6) made one BMFL Spot entry's
// channel map ~160 KB, and every entry of the same type and mode carried its
// own identical copy — 100 BMFL entries wrote a ~36 MB show file on every
// edit. On disk each DISTINCT profile is now stored once, keyed by a content
// hash, and each entry stores that key.
//
// WHAT DOES NOT CHANGE. This is purely the file encoding. In memory an
// Entry still owns its ChannelFunctions/Wheels/WheelsKnown (each entry its
// own deep copy), and every API response marshals Patch/Entry exactly as
// before — encodeShowFile and decodeShowFile are the only two places that
// know about profiles, and savePatch/NewStore/loadPatchFile/RecoverActive
// are their only callers.
//
// IDENTITY. A profile's key is the SHA-256 of its JSON (FixtureType, Mode,
// channel map, wheels, wheelsKnown; encoding/json sorts the map's offset
// keys, so the bytes are deterministic). Two versions of the same type and
// mode — e.g. one entry re-read from a newer GDTF — simply hash differently
// and coexist. Profiles are DERIVED from the entries at every save, so a
// per-entry edit that changes a channel map produces a new key for that
// entry and cannot touch a record another entry points at, and a profile no
// entry references is never written (pruning is by construction).
//
// An entry with no channel map and no wheels (hand-entered, data devices)
// gets no profile at all.

// Profile is one stored fixture profile.
type Profile struct {
	FixtureType      string                     `json:"fixtureType"`
	Mode             string                     `json:"mode"`
	ChannelFunctions map[uint16]ChannelFunction `json:"channelFunctions"`
	Wheels           []Wheel                    `json:"wheels"`
	WheelsKnown      bool                       `json:"wheelsKnown"`
}

// diskEntry is an Entry as written to a show file: the profile fields are
// shadowed out (encoding/json drops an embedded field when a shallower field
// has the same name) and replaced by the profile key.
type diskEntry struct {
	Entry
	ChannelFunctions *struct{} `json:"channelFunctions,omitempty"`
	Wheels           *struct{} `json:"wheels,omitempty"`
	WheelsKnown      *struct{} `json:"wheelsKnown,omitempty"`
	Profile          string    `json:"profile,omitempty"`
}

// diskPatch is a show file: Patch with its entries dehydrated and the
// profile table beside them.
type diskPatch struct {
	Patch
	Entries  []diskEntry        `json:"entries,omitempty"`
	Profiles map[string]Profile `json:"profiles,omitempty"`
}

func entryHasProfile(e Entry) bool {
	return len(e.ChannelFunctions) > 0 || e.WheelsKnown || len(e.Wheels) > 0
}

// encodeShowFile is the ONE writer of the show-file format.
func encodeShowFile(p Patch) ([]byte, error) {
	dp := diskPatch{Patch: p, Entries: make([]diskEntry, len(p.Entries)), Profiles: make(map[string]Profile)}
	dp.Patch.Entries = nil
	for i, e := range p.Entries {
		de := diskEntry{Entry: e}
		if entryHasProfile(e) {
			prof := Profile{FixtureType: e.FixtureType, Mode: e.Mode, ChannelFunctions: e.ChannelFunctions, Wheels: CloneWheels(e.Wheels), WheelsKnown: e.WheelsKnown}
			b, err := json.Marshal(prof)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(b)
			key := hex.EncodeToString(sum[:])
			dp.Profiles[key] = prof
			de.Profile = key
		}
		dp.Entries[i] = de
	}
	return json.MarshalIndent(dp, "", "  ")
}

// decodeShowFile is the ONE reader of the show-file format, for every schema
// version: entries of a v7-or-older file carry their channel maps inline and
// are read as they always were; a v8 entry's profile key is resolved against
// the file's profile table and the entry gets its own deep copy. It refuses —
// rather than guesses — a file newer than this build, an entry whose profile
// is missing, and an entry that carries both a key and an inline map.
func decodeShowFile(data []byte) (Patch, error) {
	var p Patch
	// Tolerant reader (no DisallowUnknownFields): see NewStore.
	if err := json.Unmarshal(data, &p); err != nil {
		return Patch{}, err
	}
	if p.SchemaVersion > CurrentSchemaVersion {
		return Patch{}, fmt.Errorf("show file is schema %d; this build understands up to %d — open it with the build that wrote it", p.SchemaVersion, CurrentSchemaVersion)
	}
	var refs struct {
		Profiles map[string]Profile `json:"profiles"`
		Entries  []struct {
			Profile string `json:"profile"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &refs); err != nil {
		return Patch{}, err
	}
	for i := range p.Entries {
		if i >= len(refs.Entries) || refs.Entries[i].Profile == "" {
			continue
		}
		key := refs.Entries[i].Profile
		prof, ok := refs.Profiles[key]
		if !ok {
			return Patch{}, fmt.Errorf("show file entry %q points at profile %s, which the file does not contain", p.Entries[i].ID, key)
		}
		if entryHasProfile(p.Entries[i]) {
			return Patch{}, fmt.Errorf("show file entry %q has both a profile reference and an inline channel map", p.Entries[i].ID)
		}
		cfs := make(map[uint16]ChannelFunction, len(prof.ChannelFunctions))
		for off, cf := range prof.ChannelFunctions {
			cfs[off] = CloneChannelFunction(cf)
		}
		p.Entries[i].ChannelFunctions = cfs
		p.Entries[i].Wheels = CloneWheels(prof.Wheels)
		p.Entries[i].WheelsKnown = prof.WheelsKnown
	}
	migrate(&p)
	return p, nil
}
