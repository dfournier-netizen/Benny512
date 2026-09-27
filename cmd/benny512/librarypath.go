package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"benny512/internal/library"
)

// sharedLibraryPath migrates the old exe-adjacent library once. An existing
// shared file always wins; neither copy is merged or overwritten silently.
func sharedLibraryPath(configDir, legacy string) (string, error) {
	dir := filepath.Join(configDir, "Benny512")
	path := filepath.Join(dir, "benny512-library.json")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	data, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	var doc library.Library
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("legacy library at %s is not readable: %w", legacy, err)
	}
	if _, err := library.NewStore("").Import(doc, library.ModeMerge); err != nil {
		return "", fmt.Errorf("legacy library at %s failed validation: %w", legacy, err)
	}
	// O_EXCL prevents two installations starting together from replacing one
	// another's migration. A failed copy removes only the incomplete new file.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(f, bytes.NewReader(data)); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
