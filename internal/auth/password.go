// Package auth owns credentials, sessions and role checks for the admin
// interface.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// These follow the OWASP recommendation of 19 MiB and two iterations, which
// costs roughly 50 ms per hash on a desktop CPU. That is deliberately slow:
// this hash is the only thing standing between a stolen jarvis.db and control
// of every managed process, and login happens rarely enough that the delay is
// invisible to an operator.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// MinPasswordLength is enforced on every password change. Length is the only
// property that reliably predicts resistance to guessing, so JARVIS asks for
// it rather than imposing character-class rules that push people towards
// "Passw0rd!".
const MinPasswordLength = 12

// HashPassword returns a PHC-format argon2id string.
//
// The parameters are encoded in the hash itself, so raising the cost later
// does not invalidate existing passwords: an old hash still verifies with the
// settings it was created under.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	}
	return hashPasswordUnchecked(password)
}

// hashPasswordUnchecked is only for the well-known first-install credential.
// Every other path goes through HashPassword and its length check.
func hashPasswordUnchecked(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// ErrBadCredentials is returned for both an unknown user and a wrong
// password. Distinguishing them would let anyone enumerate valid usernames.
var ErrBadCredentials = errors.New("auth: incorrect username or password")

// VerifyPassword checks a password against a stored hash.
func VerifyPassword(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}

	got := argon2.IDKey([]byte(password), salt,
		params.time, params.memory, params.threads, uint32(len(want)))

	// Constant-time: a length-independent comparison would leak how many
	// leading bytes were correct through its timing.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrBadCredentials
	}
	return nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errors.New("auth: password hash is not in argon2id format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, errors.New("auth: password hash has no version")
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, fmt.Errorf(
			"auth: password hash uses argon2 version %d, this build supports %d",
			version, argon2.Version)
	}

	params, err := parseParams(parts[3])
	if err != nil {
		return argonParams{}, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, errors.New("auth: password hash has an unreadable salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, errors.New("auth: password hash has an unreadable digest")
	}
	if len(key) == 0 {
		return argonParams{}, nil, nil, errors.New("auth: password hash has an empty digest")
	}
	return params, salt, key, nil
}

func parseParams(s string) (argonParams, error) {
	var p argonParams
	for _, field := range strings.Split(s, ",") {
		key, value, found := strings.Cut(field, "=")
		if !found {
			return argonParams{}, errors.New("auth: password hash has malformed parameters")
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return argonParams{}, fmt.Errorf("auth: parameter %q is not a number", key)
		}
		switch key {
		case "m":
			p.memory = uint32(n)
		case "t":
			p.time = uint32(n)
		case "p":
			p.threads = uint8(n)
		}
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return argonParams{}, errors.New("auth: password hash is missing m, t or p")
	}
	return p, nil
}

// GeneratePassword returns a random password for the bootstrap admin account.
//
// The alphabet omits characters that are easy to confuse when the password is
// read off a screen and typed by hand (0/O, 1/l/I), because that is exactly
// how this one gets used.
func GeneratePassword(length int) (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	if length < MinPasswordLength {
		length = MinPasswordLength
	}

	out := make([]byte, length)
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate password: %w", err)
	}
	// len(alphabet) is 57, which does not divide 256, so taking the modulus
	// biases the result very slightly. For a 24-character password from a CSPRNG
	// the effect on guessing difficulty is far below what matters here.
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}
