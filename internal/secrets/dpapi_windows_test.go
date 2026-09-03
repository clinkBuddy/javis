//go:build windows

package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func TestProtectRoundTrip(t *testing.T) {
	cases := map[string][]byte{
		"ascii":    []byte("hunter2"),
		"utf8":     []byte("한글 비밀번호 §±"),
		"binary":   {0x00, 0x01, 0xff, 0x7f, 0x00},
		"one byte": {0x42},
		"large":    bytes.Repeat([]byte("k"), 64*1024),
	}

	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			sealed, err := Protect(plaintext)
			if err != nil {
				t.Fatalf("Protect: %v", err)
			}
			// Short plaintexts turn up inside a few hundred bytes of
			// ciphertext by coincidence, so only assert this where the match
			// would be meaningful.
			if len(plaintext) >= 4 && bytes.Contains(sealed, plaintext) {
				t.Error("ciphertext contains the plaintext verbatim")
			}

			got, err := Unprotect(sealed)
			if err != nil {
				t.Fatalf("Unprotect: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Errorf("round trip mismatch: got %d bytes, want %d", len(got), len(plaintext))
			}
		})
	}
}

func TestProtectStringRoundTrip(t *testing.T) {
	const want = "https://hooks.example.com/services/T000/B000/xxxx"

	sealed, err := ProtectString(want)
	if err != nil {
		t.Fatalf("ProtectString: %v", err)
	}
	if strings.Contains(sealed, "hooks.example.com") {
		t.Error("base64 blob leaks the plaintext")
	}

	got, err := UnprotectString(sealed)
	if err != nil {
		t.Fatalf("UnprotectString: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnprotectRejectsGarbage(t *testing.T) {
	if _, err := Unprotect([]byte("not a DPAPI blob")); err == nil {
		t.Error("Unprotect accepted garbage input")
	}
	if _, err := UnprotectString("!!! not base64 !!!"); err == nil {
		t.Error("UnprotectString accepted invalid base64")
	}
}

func TestEmptyInputIsRejected(t *testing.T) {
	if _, err := Protect(nil); err == nil {
		t.Error("Protect(nil) should fail rather than dereference an empty slice")
	}
	if _, err := Unprotect([]byte{}); err == nil {
		t.Error("Unprotect(empty) should fail rather than dereference an empty slice")
	}
}
