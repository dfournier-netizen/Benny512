package gdtfshare

import (
	"fmt"
	"testing"
)

// G3c, owner round 6 #1 (2026-11-18): similar-name suggestions ignore
// punctuation, spacing and case. They stay suggestions: nothing is
// followed or aliased until the user presses "Follow 'X' instead".
func TestSimilarNamesIgnorePunctuationAndSpacing(t *testing.T) {
	share := map[string]string{}
	for _, n := range []string{"Chroma-Q", "Martin Professional", "High End Systems Inc.", "Chauvet Professional", "Elation Professional", "G.L.P."} {
		share[foldName(n)] = n
	}
	for _, c := range []struct{ followed, want string }{
		{"ChromaQ", "[Chroma-Q]"},
		{"chroma q", "[Chroma-Q]"},
		{"Martin", "[Martin Professional]"},
		{"High End Systems", "[High End Systems Inc.]"},
		{"GLP", "[G.L.P.]"},
		{"ETC", "[]"},              // must NOT match: shares letters, not a run of them
		{"Chauvet Lighting", "[]"}, // must NOT match: "Chauvet Professional" contains neither
		{"--", "[]"},               // nothing left after normalising: no suggestion
	} {
		if got := fmt.Sprint(similarNames(c.followed, share)); got != c.want {
			t.Errorf("similarNames(%q) = %s, want %s", c.followed, got, c.want)
		}
	}
}

// TestChromaQIsOfferedNotFollowed: a sync reports "ChromaQ" unmatched, offers
// GDTF Share's "Chroma-Q", and downloads nothing for it.
func TestChromaQIsOfferedNotFollowed(t *testing.T) {
	f := standardShare(t)
	f.add(fakeFile{RID: 501, Fixture: "Color Force II", Manufacturer: "Chroma-Q", Revision: "1", Uploader: "Manuf.", UUID: "uuid-cf2", LastModified: 1600000000})
	s, _ := newTestService(t, f, nil) // the default list follows "ChromaQ"
	syncOK(t, s)
	for _, m := range s.Status().Manufacturers {
		if m.Name == "ChromaQ" {
			if m.Matched || fmt.Sprint(m.Similar) != "[Chroma-Q]" {
				t.Fatalf("ChromaQ = %+v, want unmatched with Chroma-Q offered", m)
			}
			for _, name := range gdtfFilesOnDisk(t, s.cfg.Root) {
				if name == "501.gdtf" {
					t.Fatal("Chroma-Q's file was downloaded for ChromaQ: an alias was followed silently")
				}
			}
			return
		}
	}
	t.Fatal("ChromaQ missing from the report")
}
