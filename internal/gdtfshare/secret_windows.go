//go:build windows

package gdtfshare

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// SecretsPersist: this build stores the password, DPAPI-encrypted.
const SecretsPersist = true

var (
	modCrypt32             = syscall.NewLazyDLL("crypt32.dll")
	modKernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modKernel32.NewProc("LocalFree")
)

// dataBlob is DATA_BLOB (wincrypt.h): DWORD cbData; BYTE *pbData.
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

// take copies DPAPI's output into Go memory and frees it with LocalFree, as
// the CryptProtectData documentation requires.
func (b *dataBlob) take() []byte {
	if b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	procLocalFree.Call(uintptr(unsafe.Pointer(b.pbData)))
	return out
}

// cryptprotectUIForbidden is CRYPTPROTECT_UI_FORBIDDEN: never show a prompt.
// CRYPTPROTECT_LOCAL_MACHINE is deliberately NOT set: current-user scope.
const cryptprotectUIForbidden = 0x1

// ProtectSecret encrypts plain with DPAPI for the current Windows user.
func ProtectSecret(plain []byte) ([]byte, error) {
	in, ent := blobOf(plain), blobOf(secretEntropy)
	desc, err := syscall.UTF16PtrFromString("Benny512 GDTF Share login")
	if err != nil {
		return nil, err
	}
	var out dataBlob
	r, _, callErr := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(in)),
		uintptr(unsafe.Pointer(desc)),
		uintptr(unsafe.Pointer(ent)),
		0, 0,
		cryptprotectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(plain)
	runtime.KeepAlive(desc)
	if r == 0 {
		return nil, fmt.Errorf("Windows could not protect the GDTF Share password (CryptProtectData): %v", callErr)
	}
	return out.take(), nil
}

// UnprotectSecret decrypts a blob made by ProtectSecret. It fails for a blob
// made by another Windows user or on another computer.
func UnprotectSecret(blob []byte) ([]byte, error) {
	in, ent := blobOf(blob), blobOf(secretEntropy)
	var out dataBlob
	r, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(in)),
		0,
		uintptr(unsafe.Pointer(ent)),
		0, 0,
		cryptprotectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(blob)
	if r == 0 {
		return nil, fmt.Errorf("the saved GDTF Share password cannot be read by this Windows account on this computer (CryptUnprotectData): %v", callErr)
	}
	return out.take(), nil
}
