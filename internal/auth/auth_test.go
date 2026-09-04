package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sjkim/jarvis/internal/logging"
	"github.com/sjkim/jarvis/internal/store"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	root := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(root, "test.db"), logging.Discard())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewService(db, logging.Discard()), root
}

// bootstrapped returns a service with the admin account's password already
// changed, which is the state every other test needs.
func bootstrapped(t *testing.T) (*Service, string) {
	t.Helper()

	svc, root := newTestService(t)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx, root); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	_, token, err := svc.Login(ctx, DefaultAdminUsername, DefaultAdminPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("login with the initial password: %v", err)
	}
	if err := svc.ChangePassword(ctx, 1, DefaultAdminPassword, adminPassword, token); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	return svc, root
}

const adminPassword = "a-long-enough-admin-password"

func TestBootstrapCreatesAdminWithKnownPassword(t *testing.T) {
	svc, root := newTestService(t)
	ctx := context.Background()

	if err := svc.Bootstrap(ctx, root); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	user, _, err := svc.Login(ctx, DefaultAdminUsername, DefaultAdminPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("login with admin/admin: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", user.Role)
	}
	if user.MustChange {
		t.Error("mustChange = true; first login should reach the UI immediately")
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	svc, root := newTestService(t)
	ctx := context.Background()

	if err := svc.Bootstrap(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := svc.Bootstrap(ctx, root); err != nil {
		t.Fatal(err)
	}

	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Errorf("%d users after two bootstraps, want 1", len(users))
	}
	if _, _, err := svc.Login(ctx, DefaultAdminUsername, DefaultAdminPassword, "127.0.0.1", "test"); err != nil {
		t.Errorf("second bootstrap invalidated admin/admin: %v", err)
	}
}

func TestLoginRejectsWrongPasswordAndUnknownUser(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	if _, _, err := svc.Login(ctx, "admin", "wrong-password-here", "127.0.0.1", "t"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("wrong password: err = %v, want ErrBadCredentials", err)
	}
	// The same error for an unknown user, so account names cannot be
	// enumerated through the login form.
	if _, _, err := svc.Login(ctx, "nobody", adminPassword, "127.0.0.1", "t"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("unknown user: err = %v, want ErrBadCredentials", err)
	}
}

func TestValidateResolvesSessionAndRejectsGarbage(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	_, token, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	user, err := svc.Validate(ctx, token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if user.Username != "admin" || user.Role != RoleAdmin {
		t.Errorf("got %+v", user)
	}

	for _, bad := range []string{"", "not-a-token", token + "x"} {
		if _, err := svc.Validate(ctx, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Validate(%q) = %v, want ErrNoSession", bad, err)
		}
	}
}

// Only the hash of the token is stored, so a leaked database cannot be turned
// back into a working session.
func TestSessionTokenIsNotStoredInTheClear(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	_, token, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := svc.db.QueryRowContext(ctx, `SELECT id FROM sessions LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the session row holds the cookie value verbatim")
	}
	if stored != hashToken(token) {
		t.Error("the session row is not the token's hash")
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	_, token, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Validate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session still validates after logout: %v", err)
	}
}

// Changing a password is usually a response to it having been seen by someone
// else, so every other session must end.
func TestChangePasswordEndsOtherSessionsButKeepsThisOne(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	_, other, err := svc.Login(ctx, "admin", adminPassword, "10.0.0.5", "other browser")
	if err != nil {
		t.Fatal(err)
	}
	_, mine, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "this browser")
	if err != nil {
		t.Fatal(err)
	}

	const next = "an-even-longer-new-password"
	if err := svc.ChangePassword(ctx, 1, adminPassword, next, mine); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := svc.Validate(ctx, other); !errors.Is(err, ErrNoSession) {
		t.Error("the other session survived the password change")
	}
	if _, err := svc.Validate(ctx, mine); err != nil {
		t.Errorf("the session that made the change was ended too: %v", err)
	}
	if _, _, err := svc.Login(ctx, "admin", next, "127.0.0.1", "t"); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
}

func TestChangePasswordRequiresTheCurrentOne(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	err := svc.ChangePassword(ctx, 1, "not-the-current-one", "a-brand-new-password", "")
	if !errors.Is(err, ErrBadCredentials) {
		t.Errorf("err = %v, want ErrBadCredentials", err)
	}
	if err := svc.ChangePassword(ctx, 1, adminPassword, adminPassword, ""); !errors.Is(err, ErrPasswordReused) {
		t.Errorf("reusing the password: err = %v, want ErrPasswordReused", err)
	}
}

func TestLoginThrottleLocksOutAfterRepeatedFailures(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	for range maxFailuresPerUser {
		_, _, err := svc.Login(ctx, "admin", "wrong-password-here", "127.0.0.1", "t")
		if !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("err = %v, want ErrBadCredentials", err)
		}
	}

	// Now even the correct password must be refused, or the throttle would be
	// trivially bypassed by guessing right on the next attempt.
	if _, _, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t"); !errors.Is(err, ErrAccountLocked) {
		t.Errorf("err = %v, want ErrAccountLocked", err)
	}
}

func TestSuccessfulLoginClearsTheThrottle(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	for range maxFailuresPerUser - 1 {
		_, _, _ = svc.Login(ctx, "admin", "wrong-password-here", "127.0.0.1", "t")
	}
	if _, _, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t"); err != nil {
		t.Fatalf("login just below the limit: %v", err)
	}

	// A fresh budget after a correct password means a mistyped password
	// followed by a correct one does not accumulate towards a lockout.
	for range maxFailuresPerUser - 1 {
		if _, _, err := svc.Login(ctx, "admin", "wrong-password-here", "127.0.0.1", "t"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("err = %v, want ErrBadCredentials", err)
		}
	}
}

func TestDisabledAccountCannotLogInAndLosesItsSession(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	operator, err := svc.CreateUser(ctx, "ops", "an-operator-password", RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Login(ctx, "ops", "an-operator-password", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.SetUserState(ctx, operator.ID, RoleOperator, true); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}

	// Access must end immediately, not when the session happens to expire.
	if _, err := svc.Validate(ctx, token); err == nil {
		t.Error("a disabled account's existing session still validates")
	}
	if _, _, err := svc.Login(ctx, "ops", "an-operator-password", "127.0.0.1", "t"); !errors.Is(err, ErrUserDisabled) {
		t.Errorf("err = %v, want ErrUserDisabled", err)
	}
}

// An installation with no reachable admin can only be repaired by editing the
// database by hand, so the last one is protected.
func TestCannotRemoveTheLastAdmin(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	if err := svc.SetUserState(ctx, 1, RoleViewer, false); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the last admin: err = %v, want ErrLastAdmin", err)
	}
	if err := svc.SetUserState(ctx, 1, RoleAdmin, true); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disabling the last admin: err = %v, want ErrLastAdmin", err)
	}
	if err := svc.DeleteUser(ctx, 1); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the last admin: err = %v, want ErrLastAdmin", err)
	}

	// With a second admin in place, demoting the first is fine.
	second, err := svc.CreateUser(ctx, "admin2", "another-admin-password", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserState(ctx, 1, RoleViewer, false); err != nil {
		t.Errorf("demoting an admin while another exists: %v", err)
	}
	// That demotion makes the second account the last admin, so the guard
	// moves with it rather than being tied to a particular id.
	if err := svc.DeleteUser(ctx, second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the now-only admin: err = %v, want ErrLastAdmin", err)
	}
}

func TestCreateUserRejectsDuplicatesAndBadRoles(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	if _, err := svc.CreateUser(ctx, "ops", "an-operator-password", RoleOperator); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, "ops", "an-operator-password", RoleOperator); !errors.Is(err, ErrUserExists) {
		t.Errorf("err = %v, want ErrUserExists", err)
	}
	if _, err := svc.CreateUser(ctx, "root", "a-long-enough-password", Role("superuser")); err == nil {
		t.Error("an unknown role was accepted")
	}
	if _, err := svc.CreateUser(ctx, "shorty", "short", RoleViewer); err == nil {
		t.Error("a password below the minimum length was accepted")
	}
}

func TestResetPasswordForcesAChangeAndEndsSessions(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	operator, err := svc.CreateUser(ctx, "ops", "an-operator-password", RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Login(ctx, "ops", "an-operator-password", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	const reset = "a-reset-password-value"
	if err := svc.ResetPassword(ctx, operator.ID, reset); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	if _, err := svc.Validate(ctx, token); err == nil {
		t.Error("the session survived an admin password reset")
	}
	user, _, err := svc.Login(ctx, "ops", reset, "127.0.0.1", "t")
	if err != nil {
		t.Fatalf("login with the reset password: %v", err)
	}
	// The admin knows this password, so the account holder has to replace it.
	if !user.MustChange {
		t.Error("mustChange = false after an admin reset")
	}
}

func TestCleanupRemovesExpiredSessions(t *testing.T) {
	svc, _ := bootstrapped(t)
	ctx := context.Background()

	if _, _, err := svc.Login(ctx, "admin", adminPassword, "127.0.0.1", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = datetime('now', '-1 hour')`); err != nil {
		t.Fatal(err)
	}

	svc.Cleanup(ctx)

	var n int
	if err := svc.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d expired session(s) survived cleanup", n)
	}
}
