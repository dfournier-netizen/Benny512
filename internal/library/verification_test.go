package library

import (
	"benny512/internal/patch"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func verificationFixture() Record {
	return Record{Manufacturer: "QA", Model: "Wash", SourceFiles: []SourceFile{{Name: "wash.gdtf", Data: []byte("original archive bytes")}}, Modes: []Mode{
		{Name: "Standard", Footprint: 16, ChannelFunctions: map[uint16]patch.ChannelFunction{1: {Attribute: "Dimmer", Source: patch.SourceGDTF}}},
		{Name: "Extended", Footprint: 24},
	}}
}

func TestVerificationPersistenceAndSourceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	st := NewStore(path)
	rec, _, err := st.UpsertChecked(verificationFixture())
	if err != nil {
		t.Fatal(err)
	}
	rec, err = st.VerifyMode(rec.Key, "Standard", "bench passed", true)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Modes[0].VerifiedHash == "" || rec.Modes[1].VerifiedHash != "" {
		t.Fatal("verification must be per mode")
	}
	st = NewStore(path)
	doc := st.Export("test", time.Now())
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var imported Library
	if err = json.Unmarshal(data, &imported); err != nil {
		t.Fatal(err)
	}
	other := NewStore("")
	if _, err = other.Import(imported, ModeMerge); err != nil {
		t.Fatal(err)
	}
	got, _ := other.GetByKey(rec.Key)
	wantJSON, _ := json.Marshal(rec.Modes)
	gotJSON, _ := json.Marshal(got.Modes)
	if string(gotJSON) != string(wantJSON) || !reflect.DeepEqual(got.SourceFiles, rec.SourceFiles) {
		t.Fatal("restart/export/import lost verification or original GDTF")
	}
	got.SourceFiles[0].Data[0] = 'X'
	again, _ := other.GetByKey(rec.Key)
	if again.SourceFiles[0].Data[0] == 'X' {
		t.Fatal("source data aliases store")
	}
}

func TestVerificationChangesAndStaleReview(t *testing.T) {
	st := NewStore("")
	r, _, _ := st.UpsertChecked(verificationFixture())
	r, _ = st.VerifyMode(r.Key, "Standard", "bench", true)
	before := r.Modes[0]
	same := verificationFixture()
	same.SourceFiles = nil
	if _, _, err := st.UpsertChecked(same); err != nil {
		t.Fatal(err)
	}
	r, _ = st.GetByKey(r.Key)
	if r.Modes[1].VerifiedHash == "" || len(r.SourceFiles) != 1 {
		t.Fatal("same data must preserve stamp and source")
	}
	same.Modes[0].Footprint = 17
	if _, _, err := st.UpsertChecked(same); err != nil {
		t.Fatal(err)
	}
	r, _ = st.GetByKey(r.Key)
	if r.Modes[1].VerifiedHash != "" || !r.Modes[1].VerifiedAt.IsZero() {
		t.Fatal("changed layout retained old verification")
	}
	if _, err := st.VerifyMode(r.Key, "Standard", "", true, &before); err == nil {
		t.Fatal("stale reviewed payload was verified")
	}
	if _, err := st.VerifyMode(r.Key, "Extended", "", true); err == nil {
		t.Fatal("empty map cannot be verified")
	}
	forged := same.Modes[0]
	forged.VerifiedHash = "wrong"
	forged.VerifiedAt = time.Now()
	if m := normalizeMode(forged); m.VerifiedHash != "" || !m.VerifiedAt.IsZero() {
		t.Fatal("invalid stamp retained")
	}
}

func TestLibrarySaveFailuresDoNotPublishChanges(t *testing.T) {
	for _, op := range []string{"upsert", "import", "verify", "delete"} {
		t.Run(op, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.json")
			st := NewStore(path)
			r, _, err := st.UpsertChecked(verificationFixture())
			if err != nil {
				t.Fatal(err)
			}
			before := st.Get()
			if err = os.WriteFile(path, []byte("damaged"), 0600); err != nil {
				t.Fatal(err)
			}
			switch op {
			case "upsert":
				_, _, err = st.UpsertChecked(Record{Model: "Other"})
			case "import":
				_, err = st.Import(Library{Format: FileFormat, Records: []Record{{Model: "Other"}}}, ModeReplace)
			case "verify":
				_, err = st.VerifyMode(r.Key, "Standard", "", true)
			case "delete":
				_, err = st.DeleteChecked(r.Key)
			}
			if err == nil {
				t.Fatal("save failure hidden")
			}
			if !reflect.DeepEqual(before, st.Get()) {
				t.Fatal("failed save changed memory")
			}
			data, _ := os.ReadFile(path)
			if string(data) != "damaged" {
				t.Fatal("damaged file overwritten")
			}
		})
	}
}

func TestSourceImportChecksumAndDedup(t *testing.T) {
	st := NewStore("")
	r := verificationFixture()
	stored, _, _ := st.UpsertChecked(r)
	r.SourceFiles = append(r.SourceFiles, stored.SourceFiles[0])
	stored, _, err := st.UpsertChecked(r)
	if err != nil || len(stored.SourceFiles) != 1 {
		t.Fatal("source dedup failed", err)
	}
	r.SourceFiles[0].SHA256 = "wrong"
	if _, err = st.Import(Library{Format: FileFormat, Records: []Record{r}}, ModeMerge); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}
