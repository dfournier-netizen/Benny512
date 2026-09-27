package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedLibraryPathMigratesWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old-install", "benny512-library.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"format":"benny512-fixture-library","schemaVersion":1,"records":[]}`)
	if err := os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	path, err := sharedLibraryPath(filepath.Join(dir, "user"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("migrated library = %q, %v", got, err)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != string(data) {
		t.Fatalf("legacy copy changed: %q, %v", got, err)
	}
	newData := []byte(`{"format":"benny512-fixture-library","schemaVersion":1,"records":[{"manufacturer":"New","model":"Type"}]}`)
	if err := os.WriteFile(path, newData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedLibraryPath(filepath.Join(dir, "user"), legacy); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(newData) {
		t.Fatalf("existing shared library overwritten: %q, %v", got, err)
	}
}

func TestSharedLibraryPathRejectsForeignLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "benny512-library.json")
	if err := os.WriteFile(legacy, []byte(`{"entries":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := sharedLibraryPath(filepath.Join(dir, "user"), legacy)
	if err == nil || path != "" {
		t.Fatalf("foreign file migrated: path=%q error=%v", path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "user", "Benny512", "benny512-library.json")); !os.IsNotExist(err) {
		t.Fatalf("foreign file created shared library: %v", err)
	}
}
