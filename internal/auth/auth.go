package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sjkim/jarvis/internal/store"
)

// Role determines what an account may do. Ordered from most to least
// privileged; see Role.AtLeast.
type Role string

const (
	RoleAdmin    Role = "admin"    // everything, including accounts and JDKs
	RoleOperator Role = "operator" // start, stop, upload, tune
	RoleViewer   Role = "viewer"   // read-only
)

var roleRank = map[Role]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// AtLeast reports whether this role includes the authority of want.
func (r Role) AtLeast(want Role) bool {
	return roleRank[r] >= roleRank[want] && roleRank[r] > 0
}

func (r Role) Valid() bool { return roleRank[r] > 0 }

// Session lifetimes. The idle window is what usually ends a session; the
// absolute one guarantees that a token cannot be kept alive indefinitely by
// polling, which the dashboard does every few seconds.
const (
	SessionIdleTimeout = 12 * time.Hour
	SessionMaxLifetime = 7 * 24 * time.Hour
)

// Login throttling. A single admin typing a password wrong a few times must
// not be locked out, but an unattended brute force must become useless.
const (
	maxFailuresPerUser   = 8
	maxFailuresPerRemote = 20
	failureWindow        = 15 * time.Minute
)

// User is an authenticated account.
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Role        Role   `json:"role"`
	Disabled    bool   `json:"disabled"`
	MustChange  bool   `json:"mustChange"`
	CreatedAt   string `json:"createdAt"`
	LastLoginAt string `json:"lastLoginAt,omitempty"`
}

// Service is the entry point for everything credential-related.
type Service struct {
	db  *store.DB
	log *slog.Logger
}

func NewService(db *store.DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log}
}

var (
	ErrNoSession      = errors.New("auth: no valid session")
	ErrAccountLocked  = errors.New("auth: too many failed attempts; try again later")
	ErrUserDisabled   = errors.New("auth: this account is disabled")
	ErrUserExists     = errors.New("auth: a user with that name already exists")
	ErrUserNotFound   = errors.New("auth: user not found")
	ErrLastAdmin      = errors.New("auth: this is the last enabled admin account")
	ErrPasswordReused = errors.New("auth: the new password must differ from the current one")
)

// First-install credentials. They exist so a freshly registered service is
// reachable from the login form without a side-channel password file.
const (
	DefaultAdminUsername = "admin"
	DefaultAdminPassword = "admin"
)

// Bootstrap creates the first admin account if no users exist.
//
// The well-known password is an exception to MinPasswordLength so the operator
// can sign in once. must_change then blocks every other API until a new
// password of at least 12 characters is set. Existing accounts are left alone
// except when they still use admin/admin — that case is forced to change too.
func (s *Service) Bootstrap(ctx context.Context, dataRoot string) error {
	_ = dataRoot

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("auth: count users: %w", err)
	}
	if count > 0 {
		s.forceChangeIfDefaultPassword(ctx)
		return nil
	}

	hash, err := hashPasswordUnchecked(DefaultAdminPassword)
	if err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, must_change)
		VALUES (?, ?, 'admin', 1)`, DefaultAdminUsername, hash); err != nil {
		return fmt.Errorf("auth: create initial admin: %w", err)
	}

	s.log.Info("created the initial administrator account",
		"username", DefaultAdminUsername)
	return nil
}

// forceChangeIfDefaultPassword marks the bootstrap admin so it cannot use the
// well-known password as a standing credential. Existing installs that still
// have admin/admin pick this up on the next start.
func (s *Service) forceChangeIfDefaultPassword(ctx context.Context) {
	var id int64
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, password_hash FROM users
		WHERE username = ? AND disabled = 0`, DefaultAdminUsername).Scan(&id, &hash)
	if err != nil {
		return
	}
	if VerifyPassword(hash, DefaultAdminPassword) != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET must_change = 1 WHERE id = ?`, id)
}

// DefaultCredentialsRemain is true while the bootstrap admin can still sign in
// with admin/admin. The login form uses this to hide the hint once that
// password has been replaced.
func (s *Service) DefaultCredentialsRemain(ctx context.Context) bool {
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT password_hash FROM users
		WHERE username = ? AND disabled = 0`, DefaultAdminUsername).Scan(&hash)
	if err != nil {
		return false
	}
	return VerifyPassword(hash, DefaultAdminPassword) == nil
}

// Login verifies credentials and opens a session, returning the cookie value.
func (s *Service) Login(ctx context.Context, username, password, remoteAddr, userAgent string) (User, string, error) {
	username = strings.TrimSpace(strings.ToLower(username))

	if err := s.checkThrottle(ctx, username, remoteAddr); err != nil {
		return User{}, "", err
	}

	var (
		id       int64
		hash     string
		role     string
		disabled bool
		must     bool
		created  string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, password_hash, role, disabled, must_change, created_at
		FROM users WHERE username = ?`, username).
		Scan(&id, &hash, &role, &disabled, &must, &created)

	if errors.Is(err, sql.ErrNoRows) {
		// Hash a throwaway password anyway so that an unknown username takes
		// the same time as a known one; otherwise the response time reveals
		// which accounts exist.
		_ = VerifyPassword(dummyHash, password)
		s.recordFailure(ctx, username, remoteAddr)
		return User{}, "", ErrBadCredentials
	}
	if err != nil {
		return User{}, "", err
	}

	if err := VerifyPassword(hash, password); err != nil {
		s.recordFailure(ctx, username, remoteAddr)
		return User{}, "", ErrBadCredentials
	}
	// Checked after the password so that a disabled account cannot be
	// distinguished from a wrong password without knowing the password.
	if disabled {
		s.recordFailure(ctx, username, remoteAddr)
		return User{}, "", ErrUserDisabled
	}

	token, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, expires_at, remote_addr, user_agent, last_seen_at)
		VALUES (?, ?, datetime('now', ?), ?, ?, datetime('now'))`,
		hashToken(token), id,
		fmt.Sprintf("+%d seconds", int(SessionMaxLifetime.Seconds())),
		remoteAddr, userAgent); err != nil {
		return User{}, "", fmt.Errorf("auth: create session: %w", err)
	}

	_, _ = s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = datetime('now') WHERE id = ?`, id)
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM login_failures WHERE username = ?`, username)

	s.log.Info("login", "user", username, "role", role, "remote", remoteAddr)
	return User{
		ID: id, Username: username, Role: Role(role),
		MustChange: must, CreatedAt: created,
	}, token, nil
}

// dummyHash exists purely so the unknown-user path performs the same argon2
// work as the known-user path. It is built at startup from a random password
// so no real account can ever share it.
var dummyHash = func() string {
	password, err := GeneratePassword(32)
	if err != nil {
		// Without a CSPRNG nothing else in this package works either.
		panic(fmt.Sprintf("auth: %v", err))
	}
	hash, err := HashPassword(password)
	if err != nil {
		panic(fmt.Sprintf("auth: %v", err))
	}
	return hash
}()

// Validate resolves a cookie value to its user and refreshes the idle window.
func (s *Service) Validate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	id := hashToken(token)

	var (
		u        User
		role     string
		lastSeen sql.NullString
		lastIn   sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.disabled, u.must_change, u.created_at,
			   u.last_login_at, s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > datetime('now')`, id).
		Scan(&u.ID, &u.Username, &role, &u.Disabled, &u.MustChange, &u.CreatedAt,
			&lastIn, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, err
	}

	// An account disabled while its session is open must lose access at once,
	// not when the session happens to expire.
	if u.Disabled {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
		return User{}, ErrUserDisabled
	}

	if lastSeen.Valid {
		if seen, err := time.Parse("2006-01-02 15:04:05", lastSeen.String); err == nil {
			if time.Since(seen) > SessionIdleTimeout {
				_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
				return User{}, ErrNoSession
			}
		}
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = datetime('now') WHERE id = ?`, id)

	u.Role = Role(role)
	u.LastLoginAt = lastIn.String
	return u, nil
}

// Logout deletes one session.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, hashToken(token))
	return err
}

// ChangePassword replaces a user's password and invalidates every session
// except the one making the change.
//
// Ending the other sessions is the point: a password is usually changed
// because it may have been seen by someone else, and leaving their session
// open would defeat the change entirely.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next, keepToken string) error {
	var hash string
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return ErrUserNotFound
	}
	if err := VerifyPassword(hash, current); err != nil {
		return ErrBadCredentials
	}
	if current == next {
		return ErrPasswordReused
	}

	newHash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET password_hash = ?, must_change = 0 WHERE id = ?`,
			newHash, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, hashToken(keepToken))
		return err
	})
}

// ResetPassword lets an admin set another account's password. The target must
// change it at next login, so the value the admin chose is single-use.
func (s *Service) ResetPassword(ctx context.Context, userID int64, next string) error {
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET password_hash = ?, must_change = 1 WHERE id = ?`, hash, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
		return err
	})
}

// CreateUser adds an account.
func (s *Service) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, fmt.Errorf("auth: role %q is not one of admin, operator, viewer", role)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, must_change)
		VALUES (?, ?, ?, 1)`, username, hash, string(role))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrUserExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()

	s.log.Info("user created", "user", username, "role", role)
	return User{ID: id, Username: username, Role: role, MustChange: true}, nil
}

// ListUsers returns every account.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, role, disabled, must_change, created_at,
			   COALESCE(last_login_at, '')
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		var u User
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &role, &u.Disabled, &u.MustChange,
			&u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		u.Role = Role(role)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserState changes a role, a disabled flag, or both.
//
// Both guards protect against the same mistake: an installation with no
// reachable admin account cannot be recovered through the UI, only by editing
// the database by hand.
func (s *Service) SetUserState(ctx context.Context, userID int64, role Role, disabled bool) error {
	if !role.Valid() {
		return fmt.Errorf("auth: role %q is not one of admin, operator, viewer", role)
	}

	return s.db.InTx(ctx, func(tx *sql.Tx) error {
		var currentRole string
		var currentlyDisabled bool
		if err := tx.QueryRowContext(ctx,
			`SELECT role, disabled FROM users WHERE id = ?`, userID).
			Scan(&currentRole, &currentlyDisabled); err != nil {
			return ErrUserNotFound
		}

		losingAdmin := currentRole == string(RoleAdmin) &&
			!currentlyDisabled &&
			(role != RoleAdmin || disabled)
		if losingAdmin {
			var others int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM users
				WHERE role = 'admin' AND disabled = 0 AND id <> ?`, userID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return ErrLastAdmin
			}
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET role = ?, disabled = ? WHERE id = ?`,
			string(role), disabled, userID); err != nil {
			return err
		}
		if disabled {
			_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
			return err
		}
		return nil
	})
}

// DeleteUser removes an account and its sessions.
func (s *Service) DeleteUser(ctx context.Context, userID int64) error {
	return s.db.InTx(ctx, func(tx *sql.Tx) error {
		var role string
		var disabled bool
		if err := tx.QueryRowContext(ctx,
			`SELECT role, disabled FROM users WHERE id = ?`, userID).Scan(&role, &disabled); err != nil {
			return ErrUserNotFound
		}
		if role == string(RoleAdmin) && !disabled {
			var others int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM users
				WHERE role = 'admin' AND disabled = 0 AND id <> ?`, userID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return ErrLastAdmin
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
		return err
	})
}

// ChangeUsername renames an account. Sessions stay valid because they key
// off user id, not the name.
func (s *Service) ChangeUsername(ctx context.Context, userID int64, next string) error {
	next = strings.TrimSpace(strings.ToLower(next))
	if err := ValidateUsername(next); err != nil {
		return err
	}

	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, userID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if current == next {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, next, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrUserExists
		}
		return err
	}
	s.log.Info("username changed", "from", current, "to", next)
	return nil
}

// ValidateUsername keeps names readable and safe to put in a URL or a log line.
func ValidateUsername(name string) error {
	if len(name) < 2 || len(name) > 32 {
		return errors.New("auth: username must be between 2 and 32 characters")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return errors.New("auth: username may only contain lowercase letters, digits, dot, dash and underscore")
		}
	}
	return nil
}

func (s *Service) checkThrottle(ctx context.Context, username, remoteAddr string) error {
	window := fmt.Sprintf("-%d seconds", int(failureWindow.Seconds()))

	var byUser, byRemote int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM login_failures
			 WHERE username = ? AND at > datetime('now', ?)),
			(SELECT COUNT(*) FROM login_failures
			 WHERE remote = ? AND at > datetime('now', ?))`,
		username, window, remoteAddr, window).Scan(&byUser, &byRemote); err != nil {
		// A throttle that cannot be read must not block logins, or a database
		// hiccup would lock everyone out of their own machine.
		s.log.Warn("could not read the login throttle", "err", err)
		return nil
	}

	if byUser >= maxFailuresPerUser || byRemote >= maxFailuresPerRemote {
		s.log.Warn("login throttled",
			"user", username, "remote", remoteAddr,
			"userFailures", byUser, "remoteFailures", byRemote)
		return ErrAccountLocked
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, username, remoteAddr string) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO login_failures (username, remote) VALUES (?, ?)`,
		username, remoteAddr); err != nil {
		s.log.Warn("could not record a failed login", "err", err)
	}
	s.log.Warn("failed login", "user", username, "remote", remoteAddr)
}

// Cleanup removes expired sessions and stale throttle rows. Called
// periodically; neither table is read often enough for the rows to be pruned
// as a side effect of normal use.
func (s *Service) Cleanup(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= datetime('now')`); err != nil {
		s.log.Warn("could not prune expired sessions", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM login_failures WHERE at <= datetime('now', '-1 day')`); err != nil {
		s.log.Warn("could not prune login failures", "err", err)
	}
}
