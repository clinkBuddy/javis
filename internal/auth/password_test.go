package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Errorf("VerifyPassword on the right password: %v", err)
	}
	if err := VerifyPassword(hash, password+"x"); err == nil {
		t.Error("VerifyPassword accepted the wrong password")
	}
}

// A stored hash must never contain the password, and two hashes of the same
// password must differ — otherwise the salt is not doing its job and identical
// passwords would be visible as identical rows.
func TestHashPasswordIsSaltedAndOpaque(t *testing.T) {
	const password = "a-very-long-password"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}
	if strings.Contains(first, password) {
		t.Error("the hash contains the password verbatim")
	}
	if !strings.HasPrefix(first, "$argon2id$v=19$") {
		t.Errorf("hash is not in the expected PHC format: %s", first)
	}
}

func TestHashPasswordRejectsShortPasswords(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MinPasswordLength-1)); err == nil {
		t.Error("a password below the minimum length was accepted")
	}
	if _, err := HashPassword(strings.Repeat("a", MinPasswordLength)); err != nil {
		t.Errorf("a password at the minimum length was rejected: %v", err)
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	valid, err := HashPassword("a-very-long-password")
	if err != nil {
		t.Fatal(err)
	}

	bad := map[string]string{
		"empty":            "",
		"plaintext":        "a-very-long-password",
		"bcrypt":           "$2a$10$abcdefghijklmnopqrstuv",
		"wrong algorithm":  strings.Replace(valid, "argon2id", "argon2i", 1),
		"missing params":   "$argon2id$v=19$$c2FsdA$ZGlnZXN0",
		"truncated":        valid[:len(valid)/2],
		"unknown version":  strings.Replace(valid, "v=19", "v=16", 1),
		"non-numeric cost": strings.Replace(valid, "t=2", "t=x", 1),
	}
	for name, hash := range bad {
		if err := VerifyPassword(hash, "a-very-long-password"); err == nil {
			t.Errorf("%s hash was accepted", name)
		}
	}
}

// The parameters live in the hash so that raising the cost later does not lock
// existing accounts out.
func TestVerifyPasswordHonoursEmbeddedParameters(t *testing.T) {
	hash, err := HashPassword("a-very-long-password")
	if err != nil {
		t.Fatal(err)
	}

	// A hash written by a build with a lower memory cost must still verify.
	weaker := strings.Replace(hash, "m=19456", "m=8192", 1)
	if weaker == hash {
		t.Fatal("test setup: the memory parameter was not found in the hash")
	}
	// The digest no longer matches the (rewritten) parameters, so this must
	// fail cleanly rather than panic or report success.
	if err := VerifyPassword(weaker, "a-very-long-password"); err == nil {
		t.Error("a hash whose parameters were tampered with verified successfully")
	}
}

func TestGeneratePasswordIsRandomAndLongEnough(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		p, err := GeneratePassword(24)
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if len(p) != 24 {
			t.Fatalf("length = %d, want 24", len(p))
		}
		if seen[p] {
			t.Fatal("GeneratePassword returned a duplicate")
		}
		seen[p] = true

		// The alphabet omits look-alike characters because this password is
		// read off a screen and typed by hand.
		if strings.ContainsAny(p, "0O1lI") {
			t.Errorf("password %q contains an ambiguous character", p)
		}
	}
}

func TestGeneratePasswordRaisesShortRequests(t *testing.T) {
	p, err := GeneratePassword(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) < MinPasswordLength {
		t.Errorf("length = %d, want at least %d", len(p), MinPasswordLength)
	}
}

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, want Role
		ok         bool
	}{
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleOperator, true},
		{RoleAdmin, RoleViewer, true},
		{RoleOperator, RoleAdmin, false},
		{RoleOperator, RoleOperator, true},
		{RoleOperator, RoleViewer, true},
		{RoleViewer, RoleOperator, false},
		{RoleViewer, RoleViewer, true},
		// An unrecognised role must grant nothing, not fall through to the
		// lowest tier: a typo in the database should lock an account out
		// rather than quietly give it read access.
		{Role("superuser"), RoleViewer, false},
		{Role(""), RoleViewer, false},
	}
	for _, c := range cases {
		if got := c.have.AtLeast(c.want); got != c.ok {
			t.Errorf("Role(%q).AtLeast(%q) = %v, want %v", c.have, c.want, got, c.ok)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	good := []string{"admin", "sjkim", "ops.lead", "ci-bot", "user_1"}
	for _, name := range good {
		if err := ValidateUsername(name); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want nil", name, err)
		}
	}

	bad := []string{"", "a", "Admin", "with space", "sql'injection", "a" + strings.Repeat("b", 32)}
	for _, name := range bad {
		if err := ValidateUsername(name); err == nil {
			t.Errorf("ValidateUsername(%q) = nil, want an error", name)
		}
	}
}
