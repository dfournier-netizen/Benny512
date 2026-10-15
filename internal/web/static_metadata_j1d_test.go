package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// J1d (owner 2026-10-13 10:39): the icon sprite on main carried an embedded
// C2PA "content credentials" block (<metadata><c2pa:manifest>…) added by the
// file-transfer tool, not by this project — about 8 KB of base64 the browser
// downloads with every page and nobody asked for. Nothing the binary serves
// may carry one. The check reads embeddedUI, i.e. the exact bytes served.

var j1dMetadataRE = regexp.MustCompile(`(?i)<metadata[\s>/]`)

func TestJ1dStaticFilesCarryNoEmbeddedMetadata(t *testing.T) {
	n := 0
	err := fs.WalkDir(embeddedUI, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := embeddedUI.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		if bytes.Contains(bytes.ToLower(b), []byte("c2pa")) {
			t.Errorf("%s carries C2PA content-credentials data; serve the file without it", path)
		}
		if j1dMetadataRE.Match(b) {
			t.Errorf("%s carries a <metadata> element; serve the file without it", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded static files: %v", err)
	}
	if n == 0 {
		t.Fatal("no embedded static files were read")
	}
}
