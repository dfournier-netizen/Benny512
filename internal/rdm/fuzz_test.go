package rdm

import "testing"

// FuzzDecode exercises Decode against arbitrary bytes, seeded with the
// golden fixtures. It must never panic; error returns are fine.
func FuzzDecode(f *testing.F) {
	f.Add(hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F"))
	f.Add(hexBytes("CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00 04 84"))
	f.Add(hexBytes("CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF 0D EE"))
	f.Add([]byte{})
	f.Add([]byte{0xCC})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data)
	})
}

// FuzzDecodeDUBResponse exercises DecodeDUBResponse against arbitrary bytes.
func FuzzDecodeDUBResponse(f *testing.F) {
	f.Add(hexBytes("FE FE FE FE FE FE FE AA FA 7F FA 75 BA 57 BE 75 FE 57 FA 7D AB 55 FE FF"))
	f.Add([]byte{})
	f.Add([]byte{0xFE, 0xFE, 0xAA})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeDUBResponse(data)
	})
}
