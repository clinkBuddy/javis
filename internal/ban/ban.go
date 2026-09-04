// Package ban blocks remote addresses that have probed or brute-forced the
// admin interface. A banned address is dropped before any HTTP response is
// written, so scanners see a closed connection rather than a login form.
package ban

import (
	"context"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/sjkim/jarvis/internal/store"
)

// FailedLoginsBeforeBan is how many rejected passwords from one address
// trigger an automatic ban. A single mistype must not lock an operator out of
// their own machine, but a short burst of guesses is already an attack.
const FailedLoginsBeforeBan = 5

// Entry is one row of the ban list.
type Entry struct {
	IP        string `json:"ip"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"createdAt"`
	CreatedBy string `json:"createdBy"`
}

// Service keeps the ban list in memory for the drop-on-accept path and
// persists it so a restart does not reopen the door.
type Service struct {
	db  *store.DB
	log *slog.Logger

	mu      sync.RWMutex
	blocked map[string]Entry
}

func New(db *store.DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log, blocked: map[string]Entry{}}
}

// Load reads the table into memory. Called once at startup.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ip, reason, created_at, created_by FROM ip_bans ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	next := map[string]Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.IP, &e.Reason, &e.CreatedAt, &e.CreatedBy); err != nil {
			return err
		}
		next[e.IP] = e
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	s.blocked = next
	s.mu.Unlock()
	return nil
}

// Blocked reports whether this address must be dropped. Loopback is never
// blocked: the operator on the machine itself has to be able to unban.
func (s *Service) Blocked(ip string) bool {
	ip = Normalize(ip)
	if Exempt(ip) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.blocked[ip]
	return ok
}

// Ban records an address. Exempt addresses are ignored so a local login
// typo cannot lock the console out of the UI.
func (s *Service) Ban(ctx context.Context, ip, reason, by string) error {
	ip = Normalize(ip)
	if ip == "" || Exempt(ip) {
		return nil
	}
	if by == "" {
		by = "auto"
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ip_bans (ip, reason, created_by)
		VALUES (?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET reason = excluded.reason`,
		ip, reason, by); err != nil {
		return err
	}

	s.mu.Lock()
	s.blocked[ip] = Entry{IP: ip, Reason: reason, CreatedBy: by}
	s.mu.Unlock()

	s.log.Warn("banned remote address", "ip", ip, "reason", reason, "by", by)
	return nil
}

// Unban removes an address.
func (s *Service) Unban(ctx context.Context, ip string) error {
	ip = Normalize(ip)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ip_bans WHERE ip = ?`, ip); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.blocked, ip)
	s.mu.Unlock()
	s.log.Info("unbanned remote address", "ip", ip)
	return nil
}

// List returns the current bans, newest first.
func (s *Service) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.blocked))
	for _, e := range s.blocked {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// FailedLoginCount is how many rejected logins this address has in the
// throttle window. Used by the login handler to decide when to ban.
func (s *Service) FailedLoginCount(ctx context.Context, ip string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM login_failures
		WHERE remote = ? AND at > datetime('now', '-15 minutes')`, Normalize(ip)).Scan(&n)
	return n
}

// Normalize strips a port and maps IPv6-mapped IPv4 onto dotted form.
func Normalize(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	if v4 := net.ParseIP(ip); v4 != nil {
		if v4.To4() != nil {
			return v4.To4().String()
		}
		return v4.String()
	}
	return ip
}

// Exempt reports loopback, which must never be auto-banned.
func Exempt(ip string) bool {
	parsed := net.ParseIP(Normalize(ip))
	return parsed != nil && parsed.IsLoopback()
}

// Probe reports whether the request path looks like a scanner rather than the
// admin UI. Hash-routed pages always hit `/` or `/assets/…`.
func Probe(path string) bool {
	p := strings.ToLower(path)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}

	if p == "/" || p == "/index.html" || p == "/favicon.ico" {
		return false
	}
	if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/api/v1/") {
		return false
	}

	for _, sig := range probeSignatures {
		if strings.Contains(p, sig) {
			return true
		}
	}
	for _, ext := range []string{".php", ".asp", ".aspx", ".jsp", ".cgi", ".env"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

var probeSignatures = []string{
	"/.env", "/.git", "/.svn", "/.htaccess", "/.aws",
	"/wp-admin", "/wp-login", "/xmlrpc.php", "/wordpress",
	"/phpmyadmin", "/pma", "/adminer", "/mysql",
	"/cgi-bin", "/vendor/phpunit", "/actuator/env",
	"/manager/html", "/solr", "/console",
	"/owa/", "/webfig",
}
