package gdtfshare

import "errors"

// The GDTF Share password is kept per install and never travels (owner
// decision 5). On Windows it is stored encrypted with DPAPI
// (secret_windows.go): CryptProtectData in the current user's scope, so the
// stored bytes are useless to another Windows account or another computer,
// which is what "saved per install, each user enters their own" needs. On any
// other system (tests, a Linux --demo) there is no such store, so the
// password is kept in memory only and is never written anywhere.

// ErrNoSecretStore: this build has no way to store a password encrypted.
var ErrNoSecretStore = errors.New("this system has no protected store for the GDTF Share password; it is kept only until Benny512 closes")

// secretEntropy is DPAPI's optional extra input: another program running as
// the same user cannot unprotect the blob without it.
var secretEntropy = []byte("Benny512 GDTF Share login v1")
