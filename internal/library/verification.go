package library

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// The stamp refers to the mode payload, not to a fixture family or its origin.
func modeHash(m Mode) string {
	m.Origin = Origin{}
	m.VerifiedAt = time.Time{}
	m.VerifiedHash = ""
	m.VerificationNote = ""
	b, _ := json.Marshal(m)
	return fmt.Sprintf("%x", sha256.Sum256(b))
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
