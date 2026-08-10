package ws

import (
	"bufio"
	"bytes"
	"net/http"
	"testing"
)

// TestAcceptKeyRFC6455Example uses RFC 6455 §1.3's own worked example:
// key "dGhlIHNhbXBsZSBub25jZQ==" must produce accept
// "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=".
func TestAcceptKeyRFC6455Example(t *testing.T) {
	got := AcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestReadFrameUnmaskedSingleTextFrame uses RFC 6455 §5.7 example 1: a
// single-frame unmasked text message "Hello" = 0x81 0x05 48 65 6c 6c 6f.
// (This is a from-server example in the RFC; used here purely to exercise
// the unmasked decode path, which readFrame supports even though real
// client frames are always masked.)
func TestReadFrameUnmaskedSingleTextFrame(t *testing.T) {
	raw := []byte{0x81, 0x05, 'H', 'e', 'l', 'l', 'o'}
	f, err := readFrame(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !f.fin || f.opcode != opText || string(f.payload) != "Hello" {
		t.Errorf("got %+v", f)
	}
}

// TestReadFrameMaskedSingleTextFrame uses RFC 6455 §5.7 example 2: a
// single-frame masked text message "Hello" =
// 0x81 0x85 37 fa 21 3d 7f 9f 4d 51 58.
func TestReadFrameMaskedSingleTextFrame(t *testing.T) {
	raw := []byte{0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58}
	f, err := readFrame(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !f.fin || f.opcode != opText || string(f.payload) != "Hello" {
		t.Errorf("got fin=%v opcode=%x payload=%q", f.fin, f.opcode, f.payload)
	}
}

// TestReadFrameFragmentedTextMessage uses RFC 6455 §5.7 example 3: a
// fragmented unmasked text message "Hello" sent as "Hel" + "lo" across two
// frames (0x01 0x03 H e l, then 0x80 0x02 l o).
func TestReadFrameFragmentedTextMessage(t *testing.T) {
	raw := []byte{0x01, 0x03, 'H', 'e', 'l', 0x80, 0x02, 'l', 'o'}
	br := bufio.NewReader(bytes.NewReader(raw))
	f1, err := readFrame(br)
	if err != nil {
		t.Fatalf("frame1: %v", err)
	}
	if f1.fin || f1.opcode != opText || string(f1.payload) != "Hel" {
		t.Errorf("frame1: got fin=%v opcode=%x payload=%q", f1.fin, f1.opcode, f1.payload)
	}
	f2, err := readFrame(br)
	if err != nil {
		t.Fatalf("frame2: %v", err)
	}
	if !f2.fin || f2.opcode != opContinuation || string(f2.payload) != "lo" {
		t.Errorf("frame2: got fin=%v opcode=%x payload=%q", f2.fin, f2.opcode, f2.payload)
	}
}

// TestReadFramePingPong uses RFC 6455 §5.7 examples 4/5: an unmasked ping
// with payload "Hello" (0x89 0x05 ...), and a masked pong with the same
// payload (0x8a 0x85 ...).
func TestReadFramePingPong(t *testing.T) {
	ping := []byte{0x89, 0x05, 'H', 'e', 'l', 'l', 'o'}
	f, err := readFrame(bufio.NewReader(bytes.NewReader(ping)))
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if f.opcode != opPing || string(f.payload) != "Hello" {
		t.Errorf("ping: got opcode=%x payload=%q", f.opcode, f.payload)
	}

	pong := []byte{0x8a, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58}
	f2, err := readFrame(bufio.NewReader(bytes.NewReader(pong)))
	if err != nil {
		t.Fatalf("pong: %v", err)
	}
	if f2.opcode != opPong || string(f2.payload) != "Hello" {
		t.Errorf("pong: got opcode=%x payload=%q", f2.opcode, f2.payload)
	}
}

// TestReadFrame256ByteBinaryFrame uses RFC 6455 §5.7 example 6: an unmasked
// 256-byte binary message frame header is 0x82 0x7E then a 2-byte big-endian
// length of 256.
func TestReadFrame256ByteBinaryFrame(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 256)
	raw := append([]byte{0x82, 0x7E, 0x01, 0x00}, payload...)
	f, err := readFrame(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if f.opcode != opBinary || len(f.payload) != 256 {
		t.Errorf("got opcode=%x len=%d", f.opcode, len(f.payload))
	}
}

// TestWriteFrameRoundTrip writes a frame with writeFrame and reads it back
// with readFrame (server frames are unmasked, per RFC 6455 §5.1).
func TestWriteFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := writeFrame(w, opText, []byte("round trip test payload")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	f, err := readFrame(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !f.fin || f.opcode != opText || string(f.payload) != "round trip test payload" {
		t.Errorf("got %+v", f)
	}
}

// TestWriteFrameLongPayloadRoundTrip exercises the 16-bit and 64-bit
// extended length encodings.
func TestWriteFrameLongPayloadRoundTrip(t *testing.T) {
	for _, n := range []int{125, 126, 65535, 65536, 70000} {
		var buf bytes.Buffer
		w := bufio.NewWriter(&buf)
		payload := bytes.Repeat([]byte{0x5A}, n)
		if err := writeFrame(w, opBinary, payload); err != nil {
			t.Fatalf("n=%d: writeFrame: %v", n, err)
		}
		f, err := readFrame(bufio.NewReader(&buf))
		if err != nil {
			t.Fatalf("n=%d: readFrame: %v", n, err)
		}
		if len(f.payload) != n {
			t.Errorf("n=%d: got len %d", n, len(f.payload))
		}
	}
}

func TestReadFrameOversizeRejected(t *testing.T) {
	// Claim a 64-bit length far beyond MaxFrameSize; must be rejected
	// without attempting to allocate/read that much.
	head := []byte{0x82, 0x7F, 0, 0, 0, 0xFF, 0, 0, 0, 0}
	_, err := readFrame(bufio.NewReader(bytes.NewReader(head)))
	if err != ErrFrameTooLarge {
		t.Errorf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestIsUpgradeRequest(t *testing.T) {
	tests := []struct {
		conn, upgrade string
		want          bool
	}{
		{"Upgrade", "websocket", true},
		{"keep-alive, Upgrade", "websocket", true},
		{"upgrade", "WebSocket", true},
		{"keep-alive", "websocket", false},
		{"Upgrade", "h2c", false},
	}
	for _, tt := range tests {
		r := &http.Request{Header: http.Header{}}
		r.Header.Set("Connection", tt.conn)
		r.Header.Set("Upgrade", tt.upgrade)
		if got := isUpgradeRequest(r); got != tt.want {
			t.Errorf("conn=%q upgrade=%q: got %v want %v", tt.conn, tt.upgrade, got, tt.want)
		}
	}
}
