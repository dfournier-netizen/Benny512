package web

import "testing"

// TestG3b3AddFromGDTFLibraryUI runs gdtf_library_test.js: Fixture library ›
// Add from GDTF library on the literal ui.js, gdtf-common.js, mvrzip.js,
// gdtfparse.js, mvrimport.js, gdtf-library.js and library.js (approved G3a
// mockup scenes 9–11): offline search with Manufacturer/User words, import
// through the existing parse → confirm → POST /api/library/import path with
// the label kept, a GDTF Share single pull, and the 20 MB refusal.
func TestG3b3AddFromGDTFLibraryUI(t *testing.T) { runNodeTest(t, "gdtf_library_test.js") }
