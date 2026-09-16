package sacn

import (
	"errors"
	"strings"
	"testing"
)

// TestArtnetPortAddressToSACNUniverseHandWorked checks the show-universe
// mapping against values worked out BY HAND from the two settings, not
// recomputed from the same expression the function evaluates. Every `want`
// below is a literal.
//
//	showUniverse = raw - artnetStart + 1
//	sacnUniverse = sacnStart + showUniverse - 1
func TestArtnetPortAddressToSACNUniverseHandWorked(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         uint16
		artnetStart int
		sacnStart   int
		want        uint16
	}{
		{"default install: Art-Net 0 is show 1 is sACN 1", 0, 0, 1, 1},
		{"default install: Art-Net 1 is show 2 is sACN 2", 1, 0, 1, 2},
		{"default install: Art-Net 7 is show 8 is sACN 8", 7, 0, 1, 8},
		{"artnet-start 1: Art-Net 1 is show 1 is sACN 1", 1, 1, 1, 1},
		{"artnet-start 1: Art-Net 4 is show 4 is sACN 4", 4, 1, 1, 4},
		{"house block: Art-Net 100 is show 1 is sACN 201", 100, 100, 201, 201},
		{"house block: Art-Net 103 is show 4 is sACN 204", 103, 100, 201, 204},
		{"sacn-start 1000: Art-Net 0 is show 1 is sACN 1000", 0, 0, 1000, 1000},
		{"sacn-start 1000: Art-Net 5 is show 6 is sACN 1005", 5, 0, 1000, 1005},
		{"top of range: Art-Net 32767 is show 32768 is sACN 32768", 32767, 0, 1, 32768},
		{"top of range exactly: sACN 63999", 32767, 0, 31232, 63999},
		{"artnet start above raw: Art-Net 2 with start 5 is show -2 is sACN 8", 2, 5, 11, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ArtnetPortAddressToSACNUniverse(tc.raw, tc.artnetStart, tc.sacnStart)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ArtnetPortAddressToSACNUniverse(%d, %d, %d) = %d, want %d",
					tc.raw, tc.artnetStart, tc.sacnStart, got, tc.want)
			}
		})
	}
}

// TestArtnetPortAddressToSACNUniverseRefusesRatherThanClamps is the important
// half. sACN universe 0 does not exist, and Art-Net Port-Address 0 is both
// legal and this application's default show universe 1 — so the single most
// likely misconfiguration in the whole feature produces a universe that
// cannot be transmitted. It must be an error naming the universe, never a
// clamp to 1 and never a silent shift.
func TestArtnetPortAddressToSACNUniverseRefusesRatherThanClamps(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         uint16
		artnetStart int
		sacnStart   int
		mustMention []string
	}{
		{
			name: "sACN start 0 puts show universe 1 on sACN 0", raw: 0, artnetStart: 0, sacnStart: 0,
			mustMention: []string{"show universe 1", "Art-Net Port-Address 0", "sACN universe 0"},
		},
		{
			name: "negative: show universe 1 with sACN start -4 lands on -4", raw: 0, artnetStart: 0, sacnStart: -4,
			mustMention: []string{"show universe 1", "sACN universe -4"},
		},
		{
			name: "one past the top: 63999 + 1", raw: 32768, artnetStart: 0, sacnStart: 31232,
			mustMention: []string{"show universe 32769", "Art-Net Port-Address 32768", "sACN universe 64000"},
		},
		{
			name: "artnet start above the raw address drives it under 1", raw: 0, artnetStart: 100, sacnStart: 1,
			mustMention: []string{"show universe -99", "sACN universe -99"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ArtnetPortAddressToSACNUniverse(tc.raw, tc.artnetStart, tc.sacnStart)
			if err == nil {
				t.Fatalf("ArtnetPortAddressToSACNUniverse(%d, %d, %d) = %d with no error; it must refuse, not clamp",
					tc.raw, tc.artnetStart, tc.sacnStart, got)
			}
			if got != 0 {
				t.Fatalf("a refusing call returned universe %d as well as an error; callers must not be handed a usable-looking number", got)
			}
			if !errors.Is(err, ErrInvalidUniverse) {
				t.Fatalf("error %v does not wrap ErrInvalidUniverse, so the HTTP layer cannot classify it", err)
			}
			for _, want := range tc.mustMention {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name %q — a rig tech cannot tell which universe failed", err.Error(), want)
				}
			}
		})
	}
}
