package bytesio

// Writer is an append-only byte-buffer builder with explicit-endianness
// fixed-width writes, mirroring Reader. See Reader's doc comment for why
// byte order is always spelled out explicitly rather than inferred from a
// single "network order" rule.
type Writer struct {
	bytes []byte
}

// NewWriter creates an empty Writer.
func NewWriter() *Writer {
	return &Writer{}
}

// Bytes returns the accumulated buffer.
func (w *Writer) Bytes() []byte { return w.bytes }

// WriteU8 appends one byte.
func (w *Writer) WriteU8(v byte) {
	w.bytes = append(w.bytes, v)
}

// WriteU16BE appends a big-endian uint16.
func (w *Writer) WriteU16BE(v uint16) {
	w.bytes = append(w.bytes, byte(v>>8), byte(v))
}

// WriteU16LE appends a little-endian uint16.
func (w *Writer) WriteU16LE(v uint16) {
	w.bytes = append(w.bytes, byte(v), byte(v>>8))
}

// WriteU32BE appends a big-endian uint32.
func (w *Writer) WriteU32BE(v uint32) {
	w.bytes = append(w.bytes, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// WriteU32LE appends a little-endian uint32.
func (w *Writer) WriteU32LE(v uint32) {
	w.bytes = append(w.bytes, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// WriteBytes appends value verbatim.
func (w *Writer) WriteBytes(value []byte) {
	w.bytes = append(w.bytes, value...)
}

// WriteFixedString writes value's UTF-8 bytes into an exactly length-byte
// field, truncating if necessary and zero-padding the remainder (which also
// guarantees NUL-termination whenever the encoded text is shorter than
// length), per Art-Net's fixed-width name/report string fields.
func (w *Writer) WriteFixedString(value string, length int) {
	b := []byte(value)
	if len(b) > length {
		b = b[:length]
	}
	w.bytes = append(w.bytes, b...)
	if len(b) < length {
		w.bytes = append(w.bytes, make([]byte, length-len(b))...)
	}
}
