package library

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Library schema 2 (C1b, owner decision 2026-10-07): records may carry full
// GDTF channel detail and wheels. A build that understands only schema 1
// would import such a document and silently drop the detail; the version
// says so up front. This build reads 1 and 2 and refuses anything newer.
func TestLibrarySchema2_AcceptedAndNewerRefused(t *testing.T) {
	st := NewStore("")
	doc := Library{Format: FileFormat, SchemaVersion: 2, Records: []Record{{Manufacturer: "Robe", Model: "BMFL",
		Modes: []Mode{{Name: "Std", Footprint: 1, WheelsKnown: true}}}}}
	if _, err := st.Import(doc, ModeMerge); err != nil {
		t.Fatalf("a schema-2 library was refused: %v", err)
	}
	if got := st.Export("test", st.Get().ModifiedAt).SchemaVersion; got != 2 {
		t.Errorf("export schemaVersion = %d, want 2", got)
	}
	doc.SchemaVersion = 3
	if _, err := st.Import(doc, ModeMerge); !errors.Is(err, ErrUnsupportedSchema) {
		t.Errorf("a schema-3 library: err = %v, want ErrUnsupportedSchema", err)
	}
}

// TestLibrarySchema2_V1FileUpgradesOnSaveKeepingVerification: an owner's
// schema-1 library (written by the pre-C1 code) opens, keeps its operator
// verification, and is stamped 2 on its next save.
func TestLibrarySchema2_V1FileUpgradesOnSaveKeepingVerification(t *testing.T) {
	src, err := os.ReadFile("testdata/legacy_v1_verified_library.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lib.json")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if _, _, err := st.UpsertChecked(Record{Manufacturer: "Other", Model: "Par", Modes: []Mode{}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var lib Library
	if err := json.Unmarshal(raw, &lib); err != nil {
		t.Fatal(err)
	}
	if lib.SchemaVersion != 2 {
		t.Errorf("saved schemaVersion = %d, want 2", lib.SchemaVersion)
	}
	rec, _ := NewStore(path).GetByKey(KeyFor("Robe lighting s.r.o.", "BMFL Spot"))
	if len(rec.Modes) != 1 || rec.Modes[0].VerifiedHash == "" {
		t.Error("operator verification lost across the schema-2 upgrade")
	}
}
