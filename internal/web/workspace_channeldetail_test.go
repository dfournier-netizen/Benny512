package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"benny512/internal/patch"
)

// TestBaselineProfileDigest_StableAcrossChannelDetailUpgrade: a Rig Baseline
// taken before full GDTF channel detail existed stored a digest of each
// entry's marshalled channel map. After the upgrade the same, unchanged
// profile must produce the same digest, or every saved baseline reports
// every fixture's profile as changed.
//
// The pre-C1 bytes come from ../library/testdata/legacy_v1_verified_library.json,
// which the PRE-C1 code wrote with json.MarshalIndent; compacting it gives
// exactly what that build's json.Marshal produced for the same channel map.
func TestBaselineProfileDigest_StableAcrossChannelDetailUpgrade(t *testing.T) {
	raw, err := os.ReadFile("../library/testdata/legacy_v1_verified_library.json")
	if err != nil {
		t.Fatal(err)
	}
	var lib struct {
		Records []struct {
			Modes []struct {
				ChannelFunctions json.RawMessage `json:"channelFunctions"`
			} `json:"modes"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &lib); err != nil {
		t.Fatal(err)
	}
	legacyBytes := lib.Records[0].Modes[0].ChannelFunctions
	var compact bytes.Buffer
	if err := json.Compact(&compact, legacyBytes); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))

	// The entry as the upgraded store hands it out: loaded, then normalized.
	var cfs map[uint16]patch.ChannelFunction
	if err := json.Unmarshal(legacyBytes, &cfs); err != nil {
		t.Fatal(err)
	}
	st := patch.NewStore("")
	st.Replace(patch.Patch{Entries: []patch.Entry{{ID: "a", ChannelFunctions: cfs}}})
	p, _ := st.Get()
	if got := profileDigest(p.Entries[0]); got != want {
		t.Errorf("baseline profile digest of an unchanged pre-C1 profile moved: got %s, want %s", got, want)
	}
}
