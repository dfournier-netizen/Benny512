package web

import "testing"

// TestG3b1LibraryGDTFCapIs20MB (owner round 5 #1): the Fixture Library
// dialog's own size check takes a GDTF up to 20 MB, in step with
// internal/library.MaxSourceFileBytes. Runs the literal library.js.
func TestG3b1LibraryGDTFCapIs20MB(t *testing.T) { runNodeTest(t, "library_cap_test.js") }
