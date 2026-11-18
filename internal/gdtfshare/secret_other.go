//go:build !windows

package gdtfshare

// SecretsPersist: this build cannot store the password; it stays in memory.
const SecretsPersist = false

// ProtectSecret always refuses here: there is no protected store, and the
// password must never be written in the clear.
func ProtectSecret(plain []byte) ([]byte, error) { return nil, ErrNoSecretStore }

// UnprotectSecret always refuses here.
func UnprotectSecret(blob []byte) ([]byte, error) { return nil, ErrNoSecretStore }
