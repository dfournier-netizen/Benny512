// Package bytesio provides explicit-endianness, bounds-checked byte cursor
// types used by the rdm and artnet codecs. Every multi-byte read/write spells
// out its byte order in its name (ReadU16LE, ReadU16BE, ...) — there is
// deliberately no generic "network order" helper, because Art-Net mixes
// little- and big-endian fields within a single packet and RDM is uniformly
// big-endian; inferring order from a "usually BE" convention is exactly the
// class of bug this package exists to prevent.
package bytesio

import "fmt"

// ReadError is returned by Reader methods when a read would run past the end
// of the buffer. It is an internal-mechanics error, not a protocol-level one:
// callers that pre-validate lengths against a wire-format table should never
// see this escape into a caller-facing API.
type ReadError struct {
	Requested int
	Available int
	Offset    int
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("bytesio: short read at offset %d: requested %d, available %d", e.Offset, e.Requested, e.Available)
}

// Reader is a cursor over an immutable byte buffer with explicit-endianness
// fixed-width reads. All reads are bounds-checked and return a *ReadError
// rather than panicking; a failed read never advances the cursor.
type Reader struct {
	bytes  []byte
	offset int
}

// NewReader creates a Reader over bytes starting at offset 0.
func NewReader(b []byte) *Reader {
	return &Reader{bytes: b}
}

// NewReaderAt creates a Reader over bytes starting at the given offset.
func NewReaderAt(b []byte, offset int) *Reader {
	return &Reader{bytes: b, offset: offset}
}

// Offset returns the current read position.
func (r *Reader) Offset() int { return r.offset }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.bytes) - r.offset }

// IsAtEnd reports whether the cursor has consumed the whole buffer.
func (r *Reader) IsAtEnd() bool { return r.offset >= len(r.bytes) }

func (r *Reader) take(count int) (int, int, error) {
	if count < 0 || r.offset+count > len(r.bytes) {
		avail := len(r.bytes) - r.offset
		if avail < 0 {
			avail = 0
		}
		return 0, 0, &ReadError{Requested: count, Available: avail, Offset: r.offset}
	}
	start := r.offset
	r.offset += count
	return start, start + count, nil
}

// ReadU8 reads one byte.
func (r *Reader) ReadU8() (byte, error) {
	start, _, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return r.bytes[start], nil
}

// ReadU16BE reads a big-endian uint16.
func (r *Reader) ReadU16BE() (uint16, error) {
	start, _, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return uint16(r.bytes[start])<<8 | uint16(r.bytes[start+1]), nil
}

// ReadU16LE reads a little-endian uint16.
func (r *Reader) ReadU16LE() (uint16, error) {
	start, _, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return uint16(r.bytes[start+1])<<8 | uint16(r.bytes[start]), nil
}

// ReadU32BE reads a big-endian uint32.
func (r *Reader) ReadU32BE() (uint32, error) {
	start, _, err := r.take(4)
	if err != nil {
		return 0, err
	}
	b := r.bytes
	return uint32(b[start])<<24 | uint32(b[start+1])<<16 | uint32(b[start+2])<<8 | uint32(b[start+3]), nil
}

// ReadU32LE reads a little-endian uint32.
func (r *Reader) ReadU32LE() (uint32, error) {
	start, _, err := r.take(4)
	if err != nil {
		return 0, err
	}
	b := r.bytes
	return uint32(b[start+3])<<24 | uint32(b[start+2])<<16 | uint32(b[start+1])<<8 | uint32(b[start]), nil
}

// ReadBytes reads and returns a copy of the next count bytes.
func (r *Reader) ReadBytes(count int) ([]byte, error) {
	start, end, err := r.take(count)
	if err != nil {
		return nil, err
	}
	out := make([]byte, count)
	copy(out, r.bytes[start:end])
	return out, nil
}

// ReadFixedString reads length bytes and decodes them as a NUL-terminated
// (or NUL-padded) string: text up to the first 0x00, remaining bytes are
// padding and ignored. Per Art-Net's ShortName/LongName/NodeReport convention.
func (r *Reader) ReadFixedString(length int) (string, error) {
	raw, err := r.ReadBytes(length)
	if err != nil {
		return "", err
	}
	end := len(raw)
	for i, b := range raw {
		if b == 0 {
			end = i
			break
		}
	}
	return string(raw[:end]), nil
}

// Skip advances the cursor by count bytes without returning them.
func (r *Reader) Skip(count int) error {
	_, _, err := r.take(count)
	return err
}

// ReadRemaining consumes and returns every remaining byte.
func (r *Reader) ReadRemaining() []byte {
	out := make([]byte, len(r.bytes)-r.offset)
	copy(out, r.bytes[r.offset:])
	r.offset = len(r.bytes)
	return out
}
