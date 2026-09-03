//go:build !windows

// JARVIS targets Windows. These stubs exist only so that editors, linters and
// `go vet ./...` on a non-Windows machine can still type-check the tree.
package secrets

import "errors"

var errUnsupported = errors.New("secrets: DPAPI is only available on Windows")

func Protect([]byte) ([]byte, error)   { return nil, errUnsupported }
func Unprotect([]byte) ([]byte, error) { return nil, errUnsupported }

func ProtectString(string) (string, error)   { return "", errUnsupported }
func UnprotectString(string) (string, error) { return "", errUnsupported }
