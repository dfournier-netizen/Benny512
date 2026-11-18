package library

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// G3b1, owner round 5 #1 (docs/plans/gdtf-library-brief_2026-11-17_2101.md):
// the Fixture Library takes GDTF files up to 20 MB (was 8 MB).
func TestSourceFileCapIs20MB(t *testing.T) {
	if MaxSourceFileBytes != 20<<20 {
		t.Fatalf("MaxSourceFileBytes = %d, want %d (20 MB)", MaxSourceFileBytes, 20<<20)
	}
	rec := func(n int) Record {
		return Record{Manufacturer: "QA", Model: "Big", SourceFiles: []SourceFile{{Name: "big.gdtf", Data: bytes.Repeat([]byte{1}, n)}}}
	}
	st := NewStore("")
	if _, _, err := st.UpsertChecked(rec(11 << 20)); err != nil {
		t.Fatalf("an 11 MB GDTF was refused: %v", err)
	}
	if _, _, err := st.UpsertChecked(rec(20 << 20)); err != nil {
		t.Fatalf("a GDTF of exactly 20 MB was refused: %v", err)
	}
	_, _, err := st.UpsertChecked(rec(20<<20 + 1))
	if err == nil || !strings.Contains(err.Error(), "20 MB") {
		t.Fatalf("a GDTF over 20 MB: err = %v, want a refusal naming 20 MB", err)
	}
}

// G3b1, owner round 2 #2: a fixture imported from the GDTF library keeps
// its Manufacturer/User label. It rides on the source file it came in
// (the label belongs to that file); an old record has none and reads as
// "" (not recorded), never a guess.
func TestSourceFileKeepsItsUploaderLabel(t *testing.T) {
	st := NewStore("")
	data := []byte("archive from the GDTF library")
	if _, _, err := st.UpsertChecked(Record{Manufacturer: "Robe", Model: "Spiider",
		SourceFiles: []SourceFile{{Name: "Robe@Spiider@v1.gdtf", Data: data, UploaderLabel: "User"}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Find("Robe", "Spiider")
	if len(got.SourceFiles) != 1 || got.SourceFiles[0].UploaderLabel != "User" {
		t.Fatalf("stored source files = %+v, want the User label kept", got.SourceFiles)
	}
	b, _ := json.Marshal(got.SourceFiles[0])
	if !strings.Contains(string(b), `"uploaderLabel":"User"`) {
		t.Fatalf("source file JSON = %s", b)
	}

	// An old record (no label) reads as "", and a later import of the same
	// bytes from the GDTF library fills it in; a disk import of the same
	// bytes never clears it.
	st2 := NewStore("")
	var old Record
	if err := json.Unmarshal([]byte(`{"manufacturer":"GLP","model":"JDC1","sourceFiles":[{"name":"jdc1.gdtf","data":"`+
		"YXJjaGl2ZQ=="+`"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st2.UpsertChecked(old); err != nil {
		t.Fatal(err)
	}
	if r, _ := st2.Find("GLP", "JDC1"); r.SourceFiles[0].UploaderLabel != "" {
		t.Fatalf("an old record's label = %q, want \"\" (not recorded)", r.SourceFiles[0].UploaderLabel)
	}
	if _, _, err := st2.UpsertChecked(Record{Manufacturer: "GLP", Model: "JDC1",
		SourceFiles: []SourceFile{{Name: "GLP@JDC1@r3.gdtf", Data: []byte("archive"), UploaderLabel: "Manufacturer"}}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := st2.Find("GLP", "JDC1"); len(r.SourceFiles) != 1 || r.SourceFiles[0].UploaderLabel != "Manufacturer" {
		t.Fatalf("after the GDTF-library import = %+v", r.SourceFiles)
	}
	if _, _, err := st2.UpsertChecked(Record{Manufacturer: "GLP", Model: "JDC1",
		SourceFiles: []SourceFile{{Name: "jdc1.gdtf", Data: []byte("archive")}}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := st2.Find("GLP", "JDC1"); r.SourceFiles[0].UploaderLabel != "Manufacturer" {
		t.Fatalf("a disk import cleared the label: %+v", r.SourceFiles)
	}

	// Only the three gdtfshare words (or nothing) are accepted.
	if _, _, err := NewStore("").UpsertChecked(Record{Manufacturer: "A", Model: "B",
		SourceFiles: []SourceFile{{Name: "a.gdtf", Data: data, UploaderLabel: "Manuf."}}}); err == nil {
		t.Fatal(`label "Manuf." was accepted; want Manufacturer, User, Unknown or ""`)
	}
}

// TestSourceHashes: the checksums of every stored source file, for "already
// in your Fixture Library" without copying every archive.
func TestSourceHashes(t *testing.T) {
	st := NewStore("")
	if _, _, err := st.UpsertChecked(Record{Manufacturer: "A", Model: "B", SourceFiles: []SourceFile{{Name: "a.gdtf", Data: []byte("one")}}}); err != nil {
		t.Fatal(err)
	}
	h := st.SourceHashes()
	if len(h) != 1 || !h["7692c3ad3540bb803c020b3aee66cd8887123234ea0c6e7143c0add73ff431ed"] {
		t.Fatalf("SourceHashes = %v", h)
	}
}
