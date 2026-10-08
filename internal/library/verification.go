package library

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"benny512/internal/patch"
)

// The stamp refers to the mode payload, not to a fixture family or its origin.
//
// A mode carrying no schema-v6 channel detail (no FunctionsKnown/Functions/
// byte position on any channel, no wheels) is hashed in its exact pre-v6
// shape, so a stamp written before full GDTF detail existed still matches
// after the upgrade. A mode WITH detail is hashed whole: re-reading a GDTF
// changes the mode's data, and the stamp clears as it would for any other
// data change (the operator re-verifies).
func modeHash(m Mode) string {
	m.Origin = Origin{}
	m.VerifiedAt = time.Time{}
	m.VerifiedHash = ""
	m.VerificationNote = ""
	var b []byte
	if legacyCF, ok := patch.PreV6ChannelFunctionsJSON(m.ChannelFunctions); ok && !m.WheelsKnown && len(m.Wheels) == 0 {
		b, _ = json.Marshal(preV6Mode{
			VerifiedAt: m.VerifiedAt, VerificationNote: m.VerificationNote, VerifiedHash: m.VerifiedHash,
			Name: m.Name, Footprint: m.Footprint, ChannelFunctions: legacyCF, Origin: m.Origin,
		})
	} else {
		b, _ = json.Marshal(m)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// preV6Mode is Mode's exact pre-schema-v6 field list, order and JSON tags
// (see modeHash). ChannelFunctions is pre-marshalled by
// patch.PreV6ChannelFunctionsJSON.
type preV6Mode struct {
	VerifiedAt       time.Time       `json:"verifiedAt"`
	VerificationNote string          `json:"verificationNote"`
	VerifiedHash     string          `json:"verifiedHash"`
	Name             string          `json:"name"`
	Footprint        uint16          `json:"footprint"`
	ChannelFunctions json.RawMessage `json:"channelFunctions"`
	Origin           Origin          `json:"origin"`
}

func (st *Store) VerifyMode(key, name, note string, verified bool, expected ...*Mode) (Record, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	i := st.indexOfKeyLocked(key)
	if i < 0 {
		return Record{}, fmt.Errorf("fixture type not found")
	}
	before := cloneLibrary(*st.lib)
	for j := range st.lib.Records[i].Modes {
		m := &st.lib.Records[i].Modes[j]
		if foldKeyPart(m.Name) != foldKeyPart(name) {
			continue
		}
		if len(expected) > 0 && expected[0] != nil && !sameModePayload(*m, normalizeMode(*expected[0])) {
			return Record{}, fmt.Errorf("mode changed; reopen the library and review it again")
		}
		if verified && (m.Footprint == 0 || len(m.ChannelFunctions) == 0) {
			return Record{}, fmt.Errorf("a verified mode needs a footprint and channel map")
		}
		m.VerifiedHash = ""
		m.VerifiedAt = time.Time{}
		m.VerificationNote = ""
		if verified {
			m.VerifiedAt = time.Now()
			m.VerifiedHash = modeHash(*m)
			m.VerificationNote = note
		}
		st.lib.Records[i].UpdatedAt = time.Now()
		st.touchLocked(time.Now())
		if st.saveErr != nil {
			st.lib = &before
			return Record{}, st.saveErr
		}
		return cloneRecord(st.lib.Records[i]), nil
	}
	return Record{}, fmt.Errorf("mode not found")
}
