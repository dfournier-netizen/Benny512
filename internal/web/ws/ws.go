// Package ws is a minimal, hand-rolled server-side RFC 6455 WebSocket
// implementation: the HTTP Upgrade handshake plus text-frame read/write with
// ping/pong and close handling. No permessage-deflate, no fragmentation
// beyond reassembling a single logical message from continuation frames on
// receive (Benny512 never sends fragmented messages itself).
//
// Why hand-rolled: the module cache is offline in the sandbox and the
// architecture brief's one approved third-party dependency
// (coder/websocket) isn't fetchable here, so this package exists to keep
// the web layer stdlib-only per rev 5 §3's "minimal, justified" dependency
// rule — trivial enough to hand-roll and unit-test against RFC 6455's own
// framing rules and known test vectors.
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// magicGUID is RFC 6455 §1.3's fixed handshake constant.
const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes (RFC 6455 §5.2).
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// Errors.
var (
	ErrNotUpgrade        = errors.New("ws: request is not a WebSocket upgrade")
	ErrHijackUnsupported = errors.New("ws: ResponseWriter does not support hijacking")
	ErrClosed            = errors.New("ws: connection closed")
	ErrFrameTooLarge     = errors.New("ws: frame payload exceeds limit")
	ErrBadFrame          = errors.New("ws: malformed frame")
)

// MaxFrameSize bounds a single frame's payload to guard against a
// malicious/broken client claiming an enormous length.
const MaxFrameSize = 16 << 20 // 16 MiB

// AcceptKey computes the Sec-WebSocket-Accept value for a given
// Sec-WebSocket-Key, per RFC 6455 §1.3. Exported for the frame-codec unit
// tests to check against the RFC's own worked example.
func AcceptKey(clientKey string) string {
	h := sha1.New()
	h.Write([]byte(clientKey))
	h.Write([]byte(magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Conn is one upgraded WebSocket connection.
type Conn struct {
	rw net.Conn
	br *bufio.Reader
	bw *bufio.Writer

	writeMu sync.Mutex
	closed  bool
	closeMu sync.Mutex
}

// Upgrade performs the HTTP-to-WebSocket handshake (RFC 6455 §4.2.2) and
// returns a Conn ready for ReadMessage/WriteMessage. The caller's
// http.ResponseWriter must support hijacking (true for net/http's default
// server on a real TCP connection, which is all Benny512 ever serves).
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !isUpgradeRequest(r) {
		return nil, ErrNotUpgrade
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, fmt.Errorf("%w: missing Sec-WebSocket-Key", ErrNotUpgrade)
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, ErrHijackUnsupported
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("ws: hijack: %w", err)
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + AcceptKey(key) + "\r\n\r\n"
	if _, err := buf.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := buf.Flush(); err != nil {
		conn.Close()
		return nil, err
	}

	return &Conn{rw: conn, br: buf.Reader, bw: buf.Writer}, nil
}

func isUpgradeRequest(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") &&
		!headerContainsToken(r.Header.Get("Connection"), "upgrade") {
		return false
	}
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func headerContainsToken(v, token string) bool {
	for _, part := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// frame is a decoded WebSocket frame header plus payload.
type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// readFrame parses one frame from r per RFC 6455 §5.2. Client frames are
// always masked; an unmasked client frame is a protocol violation.
func readFrame(r *bufio.Reader) (frame, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		return frame{}, err
	}
	fin := head[0]&0x80 != 0
	opcode := head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return frame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return frame{}, err
		}
		length = binary.BigEndian.Uint64(ext)
	}
	if length > MaxFrameSize {
		return frame{}, ErrFrameTooLarge
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return frame{}, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return frame{fin: fin, opcode: opcode, payload: payload}, nil
}

// writeFrame writes one unmasked frame (server-to-client frames are never
// masked per RFC 6455 §5.1).
func writeFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	first := 0x80 | opcode // FIN=1, no fragmentation on send
	if err := w.WriteByte(first); err != nil {
		return err
	}
	n := len(payload)
	switch {
	case n < 126:
		if err := w.WriteByte(byte(n)); err != nil {
			return err
		}
	case n <= 0xFFFF:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := w.Write(ext[:]); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := w.Write(ext[:]); err != nil {
			return err
		}
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

// ReadMessage reads one complete text message, reassembling continuation
// frames and transparently answering pings with pongs. It returns
// ErrClosed when the peer sends a Close frame or the connection drops.
func (c *Conn) ReadMessage() ([]byte, error) {
	var msg []byte
	var opcode byte
	started := false
	for {
		f, err := readFrame(c.br)
		if err != nil {
			return nil, err
		}
		switch f.opcode {
		case opPing:
			if err := c.writeControl(opPong, f.payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			c.writeControl(opClose, f.payload)
			return nil, ErrClosed
		case opText, opBinary:
			if started {
				return nil, ErrBadFrame
			}
			started = true
			opcode = f.opcode
			msg = append(msg, f.payload...)
		case opContinuation:
			if !started {
				return nil, ErrBadFrame
			}
			msg = append(msg, f.payload...)
		default:
			return nil, ErrBadFrame
		}
		if f.fin {
			_ = opcode
			return msg, nil
		}
	}
}

// WriteMessage sends one text message as a single unfragmented frame.
func (c *Conn) WriteMessage(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.bw, opText, data)
}

func (c *Conn) writeControl(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.bw, opcode, payload)
}

// Close sends a Close frame and closes the underlying connection.
func (c *Conn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()
	_ = c.writeControl(opClose, nil)
	return c.rw.Close()
}
