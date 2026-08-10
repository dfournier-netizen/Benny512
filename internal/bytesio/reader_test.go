package bytesio

import (
	"bytes"
	"errors"
	"testing"
)

func TestReadU8(t *testing.T) {
	r := NewReader([]byte{0x12, 0x34})
	v, err := r.ReadU8()
	if err != nil || v != 0x12 {
		t.Fatalf("got %v, %v", v, err)
	}
	v, err = r.ReadU8()
	if err != nil || v != 0x34 {
		t.Fatalf("got %v, %v", v, err)
	}
	if _, err := r.ReadU8(); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadU16BE(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02})
	v, err := r.ReadU16BE()
	if err != nil || v != 0x0102 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadU16LE(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02})
	v, err := r.ReadU16LE()
	if err != nil || v != 0x0201 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadU32BE(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02, 0x03, 0x04})
	v, err := r.ReadU32BE()
	if err != nil || v != 0x01020304 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadU32LE(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02, 0x03, 0x04})
	v, err := r.ReadU32LE()
	if err != nil || v != 0x04030201 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadBytesOutOfBoundsThrowsAndDoesNotConsume(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02})
	if _, err := r.ReadBytes(3); err == nil {
		t.Fatal("expected error")
	}
	if r.Remaining() != 2 {
		t.Fatalf("expected cursor not advanced, remaining=%d", r.Remaining())
	}
	v, err := r.ReadU16BE()
	if err != nil || v != 0x0102 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadFixedStringStopsAtNull(t *testing.T) {
	r := NewReader([]byte("hi\x00\x00\x00"))
	s, err := r.ReadFixedString(5)
	if err != nil || s != "hi" {
		t.Fatalf("got %q, %v", s, err)
	}
}

func TestReadFixedStringNoNull(t *testing.T) {
	r := NewReader([]byte("hello"))
	s, err := r.ReadFixedString(5)
	if err != nil || s != "hello" {
		t.Fatalf("got %q, %v", s, err)
	}
}

func TestRemainingAndIsAtEnd(t *testing.T) {
	r := NewReader([]byte{1, 2, 3})
	if r.Remaining() != 3 || r.IsAtEnd() {
		t.Fatal("unexpected initial state")
	}
	if _, err := r.ReadBytes(3); err != nil {
		t.Fatal(err)
	}
	if r.Remaining() != 0 || !r.IsAtEnd() {
		t.Fatal("expected at end")
	}
}

func TestSkip(t *testing.T) {
	r := NewReader([]byte{1, 2, 3, 4})
	if err := r.Skip(2); err != nil {
		t.Fatal(err)
	}
	v, err := r.ReadU8()
	if err != nil || v != 3 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestReadRemaining(t *testing.T) {
	r := NewReader([]byte{1, 2, 3, 4})
	_ = r.Skip(1)
	got := r.ReadRemaining()
	if !bytes.Equal(got, []byte{2, 3, 4}) {
		t.Fatalf("got %v", got)
	}
	if !r.IsAtEnd() {
		t.Fatal("expected at end")
	}
}

func TestWriterRoundTripBE(t *testing.T) {
	w := NewWriter()
	w.WriteU16BE(0xABCD)
	w.WriteU32BE(0x11223344)
	r := NewReader(w.Bytes())
	v16, err := r.ReadU16BE()
	if err != nil || v16 != 0xABCD {
		t.Fatalf("got %v, %v", v16, err)
	}
	v32, err := r.ReadU32BE()
	if err != nil || v32 != 0x11223344 {
		t.Fatalf("got %v, %v", v32, err)
	}
}

func TestWriterRoundTripLE(t *testing.T) {
	w := NewWriter()
	w.WriteU16LE(0xABCD)
	w.WriteU32LE(0x11223344)
	r := NewReader(w.Bytes())
	v16, err := r.ReadU16LE()
	if err != nil || v16 != 0xABCD {
		t.Fatalf("got %v, %v", v16, err)
	}
	v32, err := r.ReadU32LE()
	if err != nil || v32 != 0x11223344 {
		t.Fatalf("got %v, %v", v32, err)
	}
}

func TestWriteFixedStringTruncates(t *testing.T) {
	w := NewWriter()
	w.WriteFixedString("HelloWorld", 5)
	if !bytes.Equal(w.Bytes(), []byte("Hello")) {
		t.Fatalf("got %v", w.Bytes())
	}
}

func TestWriteFixedStringPadsWithZero(t *testing.T) {
	w := NewWriter()
	w.WriteFixedString("Hi", 5)
	want := append([]byte("Hi"), 0, 0, 0)
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("got %v", w.Bytes())
	}
}

func TestNoCrashOnManyOutOfBoundsAttempts(t *testing.T) {
	for length := 0; length <= 8; length++ {
		buf := bytes.Repeat([]byte{0xAB}, length)
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panic at length %d: %v", length, p)
				}
			}()
			r := NewReader(buf)
			_, _ = r.ReadU8()
			_, _ = r.ReadU16BE()
			_, _ = r.ReadU16LE()
			_, _ = r.ReadU32BE()
			_, _ = r.ReadU32LE()
			_, _ = r.ReadBytes(100)
			_, _ = r.ReadFixedString(20)
		}()
	}
}

func TestReadErrorIsError(t *testing.T) {
	r := NewReader([]byte{1})
	_, err := r.ReadU16BE()
	var re *ReadError
	if !errors.As(err, &re) {
		t.Fatalf("expected *ReadError, got %T", err)
	}
}
