//go:build windows

// Package secrets encrypts values at rest with the Windows Data Protection
// API. Nothing sensitive (service account passwords, webhook URLs, and later
// SSH private keys) is ever written to disk in plaintext.
package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	crypt32                = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")

	kernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procLocalFree = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

const (
	cryptProtectUIForbidden  = 0x1
	cryptProtectLocalMachine = 0x4
)

// appEntropy is mixed into every blob. It does not add real secrecy, but it
// does mean a blob taken from JARVIS cannot be decrypted by an unrelated
// process on the same machine that simply calls CryptUnprotectData.
var appEntropy = []byte("JARVIS/v1/secret")

// Protect encrypts plaintext so that only this machine can read it back.
//
// The LOCAL_MACHINE scope is required because the core normally runs as a
// Windows service: a user-scoped blob written by an interactive admin would be
// undecryptable from session 0.
func Protect(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("secrets: nothing to encrypt")
	}
	in := newBlob(plaintext)
	entropy := newBlob(appEntropy)
	var out dataBlob

	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // szDataDescr
		uintptr(unsafe.Pointer(&entropy)),
		0, // pvReserved
		0, // pPromptStruct
		cryptProtectLocalMachine|cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("secrets: CryptProtectData: %w", err)
	}
	return copyAndFree(&out), nil
}

// Unprotect reverses Protect. It fails if the blob was produced on a different
// machine, which is the intended behaviour for a backup restored elsewhere.
func Unprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("secrets: nothing to decrypt")
	}
	in := newBlob(ciphertext)
	entropy := newBlob(appEntropy)
	var out dataBlob

	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // ppszDataDescr
		uintptr(unsafe.Pointer(&entropy)),
		0, // pvReserved
		0, // pPromptStruct
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("secrets: CryptUnprotectData: %w", err)
	}
	return copyAndFree(&out), nil
}

// ProtectString and UnprotectString wrap the byte API in base64 so the result
// can be stored in a TEXT column.
func ProtectString(s string) (string, error) {
	b, err := Protect([]byte(s))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func UnprotectString(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("secrets: decode: %w", err)
	}
	b, err := Unprotect(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func newBlob(b []byte) dataBlob {
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

// copyAndFree copies out of the LocalAlloc'd buffer CryptoAPI handed us and
// releases it, so callers get ordinary Go memory.
func copyAndFree(b *dataBlob) []byte {
	defer procLocalFree.Call(uintptr(unsafe.Pointer(b.pbData)))
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}
