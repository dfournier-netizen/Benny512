//go:build !windows

package gdtfshare

import (
	"errors"
	"testing"
)

func TestNoSecretStoreOffWindows(t *testing.T) {
	if SecretsPersist {
		t.Fatal("SecretsPersist is true on a build with no protected store")
	}
	blob, err := ProtectSecret([]byte("s3cret"))
	if !errors.Is(err, ErrNoSecretStore) || blob != nil {
		t.Fatalf("ProtectSecret = %q, %v; want nothing and ErrNoSecretStore", blob, err)
	}
}
