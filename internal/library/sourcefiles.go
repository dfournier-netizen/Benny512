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
}

func validateSourceFiles(files []SourceFile) error {
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".gdtf") || strings.ContainsAny(f.Name, "/\\\r\n") {
			return fmt.Errorf("source must have a plain .gdtf filename")
		}
		if len(f.Data) == 0 || len(f.Data) > 8*1024*1024 {
			return fmt.Errorf("GDTF source files must be between 1 byte and 8 MB")
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
		for _, old := range out {
			if old.SHA256 == f.SHA256 {
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
