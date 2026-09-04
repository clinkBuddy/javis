package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/sjkim/jarvis/internal/auth"
)

const (
	sessionCookie = "jarvis_session"
	csrfCookie    = "jarvis_csrf"
	csrfHeader    = "X-JARVIS-CSRF"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxToken
	ctxPeerIP
)

// userOf returns the authenticated account, if the handler is behind
// requireAuth.
func userOf(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(ctxUser).(auth.User)
	return u, ok
}

func tokenOf(r *http.Request) string {
	t, _ := r.Context().Value(ctxToken).(string)
	return t
}

func peerIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ctxPeerIP).(string); ok && ip != "" {
		return ip
	}
	return clientIP(r)
}

// requireAuth rejects unauthenticated requests.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeErrCode(w, http.StatusUnauthorized, "unauthenticated", errors.New("sign in to continue"))
			return
		}

		user, err := s.deps.Auth.Validate(r.Context(), cookie.Value)
		if err != nil {
			// Clear the cookie so the browser stops sending a token that will
			// never work again.
			s.clearSessionCookies(w, r)
			writeErrCode(w, http.StatusUnauthorized, "unauthenticated", err)
			return
		}

		// A password that must be changed grants nothing except the endpoints
		// needed to change it. Otherwise the forced change would be advisory,
		// and the generated bootstrap password would stay usable indefinitely.
		if user.MustChange && !isPasswordChangeRoute(r) {
			writeErrCode(w, http.StatusForbidden, "password_change_required",
				errors.New("you must set a new password before continuing"))
			return
		}

		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxToken, cookie.Value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isPasswordChangeRoute(r *http.Request) bool {
	return r.URL.Path == "/api/v1/auth/password" ||
		r.URL.Path == "/api/v1/auth/username" ||
		r.URL.Path == "/api/v1/auth/me" ||
		r.URL.Path == "/api/v1/auth/logout"
}

// requireRole wraps a handler so only accounts at or above want may call it.
func (s *Server) requireRole(want auth.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := userOf(r)
		if !ok {
			writeErrCode(w, http.StatusUnauthorized, "unauthenticated", errors.New("sign in to continue"))
			return
		}
		if !user.Role.AtLeast(want) {
			writeErrCode(w, http.StatusForbidden, "forbidden", fmt.Errorf(
				"this action requires the %s role; your account is %s", want, user.Role))
			return
		}
		next(w, r)
	}
}

// requireOrigin is the CSRF defence for endpoints that run before a session
// exists — in practice, login.
//
// The double-submit token cannot apply here: a browser arriving at the login
// form has no CSRF cookie yet, so demanding one would make signing in
// impossible. The origin check still covers the attack that matters, login
// CSRF, because a browser always sends Origin on a cross-origin POST.
func (s *Server) requireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if err := s.checkOrigin(r); err != nil {
			writeErrCode(w, http.StatusForbidden, "csrf", err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCSRF blocks state-changing requests that another site triggered.
//
// The session cookie alone is not enough. A page on any origin can make the
// browser send it, so without this check a link in an email could stop a
// production application. Two independent signals are used because each has a
// gap: the double-submit token requires script access that a cross-origin page
// does not have, and the origin check catches clients that do not send the
// header at all.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if err := s.checkOrigin(r); err != nil {
			writeErrCode(w, http.StatusForbidden, "csrf", err)
			return
		}

		cookie, err := r.Cookie(csrfCookie)
		if err != nil || cookie.Value == "" {
			writeErrCode(w, http.StatusForbidden, "csrf",
				errors.New("this request is missing its CSRF cookie; reload the page"))
			return
		}
		// Compared in constant time: the token is a bearer value, so an
		// attacker who can measure the comparison could otherwise recover it
		// one byte at a time.
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(cookie.Value)) != 1 {
			writeErrCode(w, http.StatusForbidden, "csrf",
				errors.New("CSRF token mismatch; reload the page"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// checkOrigin verifies that a mutating request came from the admin interface
// itself. Origin is preferred; Referer is the fallback for the few clients
// that omit Origin on same-origin requests.
func (s *Server) checkOrigin(r *http.Request) error {
	raw := r.Header.Get("Origin")
	if raw == "" {
		raw = r.Header.Get("Referer")
	}
	if raw == "" {
		// Non-browser clients (curl, PowerShell, the CLI) send neither. They
		// are not subject to ambient cookie authority, so the CSRF token check
		// that follows is the real gate for them.
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("this request has an unreadable Origin header")
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return fmt.Errorf("this request came from %s, which is not the admin interface", u.Host)
	}
	return nil
}

// audit records who did what.
//
// Only mutations are logged. Recording reads as well would bury the handful of
// entries that matter — a stop, a promotion, a password reset — under the
// dashboard's polling.
func (s *Server) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		user, _ := userOf(r)
		result := "ok"
		if rec.status >= 400 {
			result = "denied"
		}

		if _, err := s.deps.DB.ExecContext(r.Context(), `
			INSERT INTO audit_logs (user_id, username, remote_addr, action, target, result, detail)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			nullID(user.ID), user.Username, clientIP(r),
			r.Method, r.URL.Path, result, fmt.Sprintf("status=%d", rec.status),
		); err != nil {
			s.deps.Log.Warn("could not write an audit entry",
				"action", r.Method, "target", r.URL.Path, "err", err)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(b)
}

// Flush lets streaming handlers (log tailing) keep working through the
// wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// clientIP strips the port so the throttle counts an address rather than a
// connection.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.HasSuffix(addr, "]") {
		return addr[:i]
	}
	return addr
}

// securityHeaders locks the page down to its own origin.
//
// The admin UI loads nothing from anywhere else, so a restrictive policy costs
// nothing and turns any future injected markup into an inert string rather
// than a way to issue authenticated API calls.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
