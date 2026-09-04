package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy of a session token. 32 bytes makes guessing one
// infeasible, which matters because a valid token is full authority over every
// managed process.
const tokenBytes = 32

// newToken returns the value that goes into the cookie.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken maps a cookie value to its database key.
//
// Only the hash is stored, so a leaked jarvis.db (or a stray backup of it)
// cannot be turned into a working session. A plain SHA-256 is right here
// rather than argon2: the token is already 256 bits of uniform randomness, so
// there is no low-entropy guess for an attacker to iterate over, and session
// lookup happens on every request.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
