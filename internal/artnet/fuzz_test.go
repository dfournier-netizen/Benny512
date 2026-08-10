package artnet

import "testing"

// FuzzDecode exercises the top-level Decode against arbitrary bytes, seeded
// with the golden fixtures for every packet type. Must never panic.
func FuzzDecode(f *testing.F) {
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00"))
	f.Add(hexBytes(artPollReplyHex))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 50 00 0E 01 00 00 00 00 04 FF 00 7F 01"))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01 03"))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 80 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00"))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 81 00 0E 01 01 00 00 00 00 00 00 00 00 00 00 00 01 00 01 7A 70 12 34 56 78"))
	f.Add(hexBytes(artTodControlHex))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00 CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F"))
	f.Add(hexBytes("41 72 74 2D 4E 65 74 00 00 84 00 0E 01 00 7A 70 12 34 56 78 00 30 00 F0 00 01 00 02 00 00 00 00 00 01 00 05"))
	f.Add([]byte{})
	f.Add([]byte{0x41})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data)
	})
}
