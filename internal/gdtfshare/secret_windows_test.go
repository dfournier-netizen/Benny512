//go:build windows

package gdtfshare

import (
	"bytes"
	"testing"
)

// Runs only on Windows (the CI gate runs Linux; this is compiled by the
// Windows vet check and runs on the owner's machine).
func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("s3cret-Pa55word!")
	blob, err := ProtectSecret(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("the protected blob contains the password in the clear")
	}
	back, err := UnprotectSecret(blob)
	if err != nil || !bytes.Equal(back, plain) {
		t.Fatalf("round trip = %q, %v", back, err)
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := UnprotectSecret(blob); err == nil {
		t.Fatal("a damaged blob was accepted")
	}
}
