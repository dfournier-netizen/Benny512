package gdtfshare

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoginGoodAndBad(t *testing.T) {
	f := standardShare(t)
	c, err := NewClient(f.baseURL(), nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(context.Background(), "dom", "wrong"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("bad login err = %v, want ErrBadLogin", err)
	}
	// Not logged in: the list is refused with 401.
	if _, _, err := c.List(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("list without login err = %v, want ErrUnauthorized", err)
	}
	if err := c.Login(context.Background(), f.user, f.pass); err != nil {
		t.Fatalf("good login: %v", err)
	}
	// The session cookie (whatever it is called) is carried by later calls.
	list, skipped, err := c.List(context.Background())
	if err != nil || skipped != 0 || len(list) != 4 {
		t.Fatalf("list after login = %d entries, %d skipped, %v", len(list), skipped, err)
	}
	var buf bytes.Buffer
	n, err := c.Download(context.Background(), 101, &buf, 1<<20)
	if err != nil || n == 0 || !bytes.HasPrefix(buf.Bytes(), []byte("PK\x03\x04")) {
		t.Fatalf("download = %d bytes, %v", n, err)
	}
	// 404's JSON error is reported, not saved as a file.
	buf.Reset()
	if _, err := c.Download(context.Background(), 999999, &buf, 1<<20); err == nil || !strings.Contains(err.Error(), "File does not exist") || buf.Len() != 0 {
		t.Fatalf("missing rid: err %v, %d bytes written", err, buf.Len())
	}
	// The login body is the documented JSON and carries no other fields.
	f.mu.Lock()
	body := f.bodies[len(f.bodies)-1]
	f.mu.Unlock()
	if body != `{"user":"dom","password":"s3cret-Pa55word!"}` {
		t.Fatalf("login body = %s", body)
	}
}

func TestParseListDocumentedShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/getlist_documented_shape.json")
	if err != nil {
		t.Fatal(err)
	}
	list, skipped, err := parseList(raw)
	if err != nil || skipped != 0 || len(list) != 2 {
		t.Fatalf("parseList = %d entries, %d skipped, %v", len(list), skipped, err)
	}
	a := list[0]
	if a.RID != 12345 || a.Fixture != "Spiider" || a.Manufacturer != "Robe" || a.Revision != "Mode names from manual" ||
		a.CreationDate != 1570494396 || a.LastModified != 1570494396 || a.Uploader != "Manuf." ||
		a.UploaderLabel != LabelManufacturer || a.Rating == nil || *a.Rating != 4.1 || a.Version != "1.1" ||
		a.Creator != "robe_lighting" || a.UUID != "6C3E3F5A-0D1B-4A40-9C66-2C0D2F3A1B7E" || a.FileSize != 2311680 {
		t.Fatalf("entry 0 = %+v", a)
	}
	if len(a.Modes) != 2 || a.Modes[0].Name != "Mode 1" || a.Modes[0].DMXFootprint == nil || *a.Modes[0].DMXFootprint != 49 {
		t.Fatalf("modes = %+v", a.Modes)
	}
	b := list[1]
	// Numbers as numbers parse too; a rating of 0 is kept as 0, not absent;
	// a quoted filesize parses.
	if b.CreationDate != 1600000000 || b.LastModified != 1600000500 || b.UploaderLabel != LabelUser ||
		b.Rating == nil || *b.Rating != 0 || b.FileSize != 1024 || b.Modes == nil || len(b.Modes) != 0 {
		t.Fatalf("entry 1 = %+v", b)
	}
}

func TestParseListToleratesOneBadEntry(t *testing.T) {
	raw := []byte(`{"result":true,"list":[{"rid":"x"},{"rid":7,"fixture":"A","manufacturer":"B","uploader":"Weird","rating":""}]}`)
	list, skipped, err := parseList(raw)
	if err != nil || skipped != 1 || len(list) != 1 {
		t.Fatalf("parseList = %+v, %d skipped, %v", list, skipped, err)
	}
	if list[0].UploaderLabel != LabelUnknown || list[0].Rating != nil {
		t.Fatalf("entry = %+v; an undocumented uploader is Unknown and an empty rating is absent", list[0])
	}
	if _, _, err := parseList([]byte(`{"result":false,"error":"Unauthorized."}`)); err == nil {
		t.Fatal("result:false parsed as an empty list")
	}
}
