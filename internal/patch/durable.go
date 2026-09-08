package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWrite flushes a sibling temporary file before replacing its destination.
// A failed write never replaces the last successful version.
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".benny-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func savePatch(path string, p Patch) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("show not saved: %w", err)
	}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		var old Patch
		if json.Unmarshal(previous, &old) != nil {
			return fmt.Errorf("show not saved: existing file is damaged; recover it or create a new show")
		}
		if err = atomicWrite(path+".bak", previous); err != nil {
			return fmt.Errorf("show backup failed: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("show not saved: %w", readErr)
	}
	if err = atomicWrite(path, data); err != nil {
		return fmt.Errorf("show not saved: %w", err)
	}
	return nil
}

// ResetActive preserves the show identity and every other saved show. The old
// active show becomes its recovery copy, just like any other successful edit.
func (st *Store) ResetActive() (Patch, error) {
	return st.Mutate(func(p *Patch) error { p.Entries = []Entry{}; p.Workspace = nil; return nil })
}

// RecoverActive explicitly restores the preceding save. No silent rollback on
// startup: the user should know when recovering older work.
func (st *Store) RecoverActive() (Patch, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.path == "" {
		return Patch{}, fmt.Errorf("recovery requires a saved show")
	}
	p, ok := loadPatchFile(st.activePath + ".bak")
	if !ok {
		return Patch{}, fmt.Errorf("no readable recovery copy for this show")
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err == nil {
		err = atomicWrite(st.activePath, data)
	}
	if err != nil {
		return Patch{}, fmt.Errorf("recovery failed: %w", err)
	}
	st.patch = &p
	return clonePatch(p), nil
}
