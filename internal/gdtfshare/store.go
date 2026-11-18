package gdtfshare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The on-disk files. A damaged index or sync file is never overwritten by a
// read: loading reports it and the sync refuses to run, leaving the bytes for
// inspection (internal/library and settingsdurable.go's rule).

const (
	indexFormat = "benny512-gdtf-index"
	syncFormat  = "benny512-gdtf-sync"
	listFormat  = "benny512-gdtf-list"
	schemaV1    = 1
	partMarker  = ".part-"
)

type indexDoc struct {
	Format        string       `json:"format"`
	SchemaVersion int          `json:"schemaVersion"`
	Files         []IndexEntry `json:"files"`
}

type syncDoc struct {
	Format        string               `json:"format"`
	SchemaVersion int                  `json:"schemaVersion"`
	Followed      []string             `json:"followed"`
	LastListFetch *time.Time           `json:"lastListFetch"`
	LastAttempt   *time.Time           `json:"lastAttempt"`
	LastResult    Result               `json:"lastResult"`
	Manufacturers []ManufacturerResult `json:"manufacturers"`
	Pending       []Pending            `json:"pending"`
}

type listDoc struct {
	Format        string    `json:"format"`
	SchemaVersion int       `json:"schemaVersion"`
	FetchedAt     time.Time `json:"fetchedAt"`
	List          []Entry   `json:"list"`
}

type store struct{ root string }

func (st store) path(name string) string { return filepath.Join(st.root, name) }
func (st store) filesDir() string        { return filepath.Join(st.root, "files") }
func (st store) relFile(rid int64) string {
	return "files/" + strconv.FormatInt(rid, 10) + ".gdtf"
}
func (st store) absFile(rid int64) string {
	return filepath.Join(st.filesDir(), strconv.FormatInt(rid, 10)+".gdtf")
}

func readDoc(path string, v any) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s could not be read (left untouched): %w", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("%s is damaged (left untouched so it can be inspected): %w", path, err)
	}
	return true, nil
}

func (st store) loadIndex() (indexDoc, error) {
	doc := indexDoc{Format: indexFormat, SchemaVersion: schemaV1, Files: make([]IndexEntry, 0)}
	if _, err := readDoc(st.path("index.json"), &doc); err != nil {
		return indexDoc{}, err
	}
	if doc.Files == nil {
		doc.Files = make([]IndexEntry, 0)
	}
	return doc, nil
}

func (st store) loadSync() (syncDoc, error) {
	doc := syncDoc{Format: syncFormat, SchemaVersion: schemaV1}
	found, err := readDoc(st.path("sync.json"), &doc)
	if err != nil {
		return syncDoc{}, err
	}
	if !found {
		// A fresh library follows the owner's starting list. Once sync.json
		// exists its list is the user's, even when empty.
		doc.Followed = append([]string(nil), DefaultManufacturers...)
	}
	if doc.Followed == nil {
		doc.Followed = make([]string, 0)
	}
	if doc.Manufacturers == nil {
		doc.Manufacturers = make([]ManufacturerResult, 0)
	}
	if doc.Pending == nil {
		doc.Pending = make([]Pending, 0)
	}
	return doc, nil
}

func (st store) loadList() (listDoc, error) {
	doc := listDoc{Format: listFormat, SchemaVersion: schemaV1, List: make([]Entry, 0)}
	if _, err := readDoc(st.path("list.json"), &doc); err != nil {
		return listDoc{}, err
	}
	return doc, nil
}

func (st store) saveJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.root, 0o700); err != nil {
		return err
	}
	return atomicWrite(st.path(name), func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// atomicWrite lands bytes through a synced sibling temporary file and a
// rename (internal/patch/durable.go's discipline). The temporary name holds
// partMarker, so a power cut mid-write leaves only a file the next sync
// recognises and removes — never a half file under the real name.
func atomicWrite(path string, fill func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+partMarker+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := fill(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// writeFile downloads into files/<rid>.gdtf via a .part name and returns the
// SHA-256 and size of what landed.
func (st store) writeFile(rid int64, fill func(io.Writer) error) (string, int64, error) {
	if err := os.MkdirAll(st.filesDir(), 0o700); err != nil {
		return "", 0, err
	}
	h := sha256.New()
	var n int64
	err := atomicWrite(st.absFile(rid), func(w io.Writer) error {
		cw := &countWriter{w: io.MultiWriter(w, h)}
		err := fill(cw)
		n = cw.n
		return err
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// hashFile reads a complete file already under its real name.
func (st store) hashFile(rid int64) (string, int64, error) {
	f, err := os.Open(st.absFile(rid))
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// cleanPartials removes .part files left by a killed process.
func (st store) cleanPartials() {
	for _, dir := range []string{st.root, st.filesDir()} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && strings.Contains(e.Name(), partMarker) {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

func (st store) removeFile(rid int64) error {
	err := os.Remove(st.absFile(rid))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// fileMatches: the file under the real name exists with the recorded size.
func (st store) fileMatches(e IndexEntry) bool {
	fi, err := os.Stat(st.absFile(e.RID))
	return err == nil && fi.Mode().IsRegular() && fi.Size() == e.LocalSize
}
