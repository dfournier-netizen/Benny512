package library

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// SourceFile preserves the original archive. Data is base64 in JSON and is
// never unpacked or executed by the server. SHA256 identifies duplicate files.
type SourceFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"data,omitempty"`
	// UploaderLabel is set when the file was imported from the GDTF library
	// (internal/gdtfshare): "Manufacturer" or "User" (who uploaded it to GDTF
	// Share), or "Unknown" when GDTF Share did not say. "" means not
	// recorded: a file imported from disk, or by a build before G3b. It is
	// never guessed (owner decision, round 2 #2: label every imported
	// fixture Manufacturer or User).
	UploaderLabel string `json:"uploaderLabel"`
}

// MaxSourceFileBytes is the largest GDTF the Fixture Library keeps: 20 MB
// (owner decision, round 5 #1, 2026-11-18; was 8 MB). Each kept archive
// lives base64 inside the one benny512-library.json, so this bounds how
// fast that file grows.
const MaxSourceFileBytes = 20 * 1024 * 1024

func validUploaderLabel(l string) bool {
	switch l {
	case "", "Manufacturer", "User", "Unknown":
		return true
	}
	return false
}

func validateSourceFiles(files []SourceFile) error {
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".gdtf") || strings.ContainsAny(f.Name, "/\\\r\n") {
			return fmt.Errorf("source must have a plain .gdtf filename")
		}
		if len(f.Data) == 0 || len(f.Data) > MaxSourceFileBytes {
			return fmt.Errorf("GDTF source files must be between 1 byte and 20 MB")
		}
		if !validUploaderLabel(f.UploaderLabel) {
			return fmt.Errorf("source file %s: uploader label must be Manufacturer, User, Unknown or empty, got %q", f.Name, f.UploaderLabel)
		}
		if f.SHA256 != "" && f.SHA256 != fmt.Sprintf("%x", sha256.Sum256(f.Data)) {
			return fmt.Errorf("source file checksum mismatch: %s", f.Name)
		}
	}
	return nil
}

func mergeSourceFiles(dst, src []SourceFile) []SourceFile {
	out := append(make([]SourceFile, 0, len(dst)+len(src)), dst...)
	for _, f := range src {
		f.SHA256 = fmt.Sprintf("%x", sha256.Sum256(f.Data))
		found := false
		for i, old := range out {
			if old.SHA256 == f.SHA256 {
				// The same bytes again: keep the stored entry, but learn the
				// GDTF-library label if it had none. A label is never
				// cleared or changed by a later import.
				if old.UploaderLabel == "" && f.UploaderLabel != "" {
					out[i].UploaderLabel = f.UploaderLabel
				}
				found = true
				break
			}
		}
		if !found {
			f.Data = append([]byte(nil), f.Data...)
			out = append(out, f)
		}
	}
	return out
}

// SourceHashes returns the SHA-256 of every stored source file, without
// copying the archives (List and Get deep-copy every one).
func (st *Store) SourceHashes() map[string]bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := map[string]bool{}
	for _, r := range st.lib.Records {
		for _, f := range r.SourceFiles {
			out[f.SHA256] = true
		}
	}
	return out
}
