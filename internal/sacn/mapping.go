package sacn

import "fmt"

// ArtnetPortAddressToSACNUniverse converts a stored Art-Net Port-Address --
// the canonical universe representation everywhere in this application
// (patch.Entry.Universe, artnet.PortAddress, the wire) -- into the sACN
// universe the same show universe lives on. The name states the direction on
// purpose, mirroring the formatUser/formatArtnet discipline the September 9
// universe-numbering rework established: there is no "convert a universe"
// helper here that a reader has to guess the direction of.
//
// Two independent user-visible numbering settings meet in this one line of
// arithmetic:
//
//	showUniverse = int(raw) - artnetStart + 1   // web.Settings.ArtnetStartUniverse, ui.js's artnetToUser
//	sacnUniverse = sacnStart + showUniverse - 1 // sacn.Settings.StartUniverse
//
// NOTHING STORED IS REWRITTEN by this call. patch.Entry.Universe stays the
// raw 15-bit Art-Net Port-Address it has always been; the mapping is applied
// at the output boundary only, once per universe, when a run actually starts.
//
// It REFUSES rather than clamps. Art-Net Port-Address 0 is perfectly legal
// and is this application's default show universe 1, but sACN universe 0 does
// not exist: ANSI E1.31-2025 reserves it, and codec.go rejects it
// (ErrInvalidUniverse, 1..63999). A clamp to 1 would silently light the wrong
// universe on a real rig, and a silent shift would be worse. Every caller
// that cannot map a universe must therefore refuse to start and say which
// universe it could not map -- so the returned error names it.
func ArtnetPortAddressToSACNUniverse(raw uint16, artnetStart, sacnStart int) (uint16, error) {
	show := int(raw) - artnetStart + 1
	u := sacnStart + show - 1
	if u < 1 || u > maxUniverse {
		return 0, fmt.Errorf("%w: show universe %d (Art-Net Port-Address %d) would map to sACN universe %d, with an sACN start universe of %d and an Art-Net start universe of %d",
			ErrInvalidUniverse, show, raw, u, sacnStart, artnetStart)
	}
	return uint16(u), nil
}
