package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/ban"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type meResponse struct {
	auth.User
	CSRFToken string `json:"csrfToken"`
}

// registerAuthRoutes mounts the endpoints that must be reachable without a
// session.
func (s *Server) registerAuthRoutes(r chi.Router) {
	r.Get("/auth/setup", s.handleAuthSetup)
	r.Post("/auth/login", s.handleLogin)
}

func (s *Server) registerSessionRoutes(r chi.Router) {
	r.Get("/auth/me", s.handleMe)
	r.Post("/auth/logout", s.handleLogout)
	r.Post("/auth/password", s.handleChangePassword)
	r.Post("/auth/username", s.handleChangeUsername)

	r.Get("/users", s.requireRole(auth.RoleAdmin, s.handleListUsers))
	r.Post("/users", s.requireRole(auth.RoleAdmin, s.handleCreateUser))
	r.Put("/users/{id}", s.requireRole(auth.RoleAdmin, s.handleUpdateUser))
	r.Post("/users/{id}/password", s.requireRole(auth.RoleAdmin, s.handleResetPassword))
	r.Post("/users/{id}/username", s.requireRole(auth.RoleAdmin, s.handleAdminChangeUsername))
	r.Delete("/users/{id}", s.requireRole(auth.RoleAdmin, s.handleDeleteUser))

	r.Get("/audit", s.requireRole(auth.RoleAdmin, s.handleListAudit))
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"defaultHint": s.deps.Auth != nil && s.deps.Auth.DefaultCredentialsRemain(r.Context()),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	user, token, err := s.deps.Auth.Login(
		r.Context(), req.Username, req.Password, peerIP(r), r.UserAgent())

	// Logged here rather than by the audit middleware, which only runs behind
	// authentication and so would record every failure as anonymous. The
	// attempted username is the whole point of the entry: it is what
	// distinguishes a mistyped password from someone working through a list.
	s.auditLogin(r, req.Username, err)
	s.maybeBanFailedLogin(r, err)

	switch {
	case errors.Is(err, auth.ErrAccountLocked):
		writeErrCode(w, http.StatusTooManyRequests, "throttled", err)
		return
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrUserDisabled):
		writeErrCode(w, http.StatusUnauthorized, "bad_credentials", err)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	csrf, err := s.setSessionCookies(w, r, token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{User: user, CSRFToken: csrf})
}

func (s *Server) auditLogin(r *http.Request, username string, loginErr error) {
	result, detail := "ok", ""
	if loginErr != nil {
		result, detail = "denied", loginErr.Error()
	}
	if _, err := s.deps.DB.ExecContext(r.Context(), `
		INSERT INTO audit_logs (username, remote_addr, action, target, result, detail)
		VALUES (?, ?, 'LOGIN', '/api/v1/auth/login', ?, ?)`,
		strings.ToLower(strings.TrimSpace(username)), peerIP(r), result, detail,
	); err != nil {
		s.deps.Log.Warn("could not record a login attempt", "err", err)
	}
}

func (s *Server) maybeBanFailedLogin(r *http.Request, loginErr error) {
	if s.deps.Bans == nil || !errors.Is(loginErr, auth.ErrBadCredentials) {
		return
	}
	ip := peerIP(r)
	if n := s.deps.Bans.FailedLoginCount(r.Context(), ip); n >= ban.FailedLoginsBeforeBan {
		_ = s.deps.Bans.Ban(r.Context(), ip, "repeated failed logins", "auto")
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userOf(r)

	// The SPA reads the CSRF token from this response rather than from the
	// cookie, so a reload after a restart still gets a usable token even
	// though the cookie is JavaScript-readable by design.
	csrf := ""
	if c, err := r.Cookie(csrfCookie); err == nil {
		csrf = c.Value
	}
	if csrf == "" {
		var err error
		if csrf, err = s.rotateCSRFCookie(w, r); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, meResponse{User: user, CSRFToken: csrf})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Auth.Logout(r.Context(), tokenOf(r)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.clearSessionCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := userOf(r)

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	err := s.deps.Auth.ChangePassword(
		r.Context(), user.ID, req.CurrentPassword, req.NewPassword, tokenOf(r))
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		writeErrCode(w, http.StatusUnauthorized, "bad_credentials",
			errors.New("the current password is incorrect"))
		return
	case errors.Is(err, auth.ErrPasswordReused):
		writeErr(w, http.StatusBadRequest, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	s.deps.Log.Info("password changed", "user", user.Username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}

func (s *Server) handleChangeUsername(w http.ResponseWriter, r *http.Request) {
	user, _ := userOf(r)
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.deps.Auth.ChangeUsername(r.Context(), user.ID, req.Username)
	switch {
	case errors.Is(err, auth.ErrUserExists):
		writeErr(w, http.StatusConflict, err)
		return
	case errors.Is(err, auth.ErrUserNotFound):
		writeErr(w, http.StatusNotFound, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.deps.Log.Info("username changed", "from", user.Username, "to", req.Username)
	writeJSON(w, http.StatusOK, map[string]string{"username": strings.ToLower(strings.TrimSpace(req.Username))})
}

type createUserRequest struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.deps.Auth.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	user, err := s.deps.Auth.CreateUser(r.Context(), req.Username, req.Password, req.Role)
	switch {
	case errors.Is(err, auth.ErrUserExists):
		writeErr(w, http.StatusConflict, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

type updateUserRequest struct {
	Role     auth.Role `json:"role"`
	Disabled bool      `json:"disabled"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("user id must be a number"))
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Losing your own admin role mid-session leaves the UI in a state that
	// looks broken rather than intentional, so it is refused outright.
	if user, ok := userOf(r); ok && user.ID == id && (req.Role != auth.RoleAdmin || req.Disabled) {
		writeErr(w, http.StatusConflict,
			errors.New("you cannot remove your own admin role or disable your own account"))
		return
	}

	err = s.deps.Auth.SetUserState(r.Context(), id, req.Role, req.Disabled)
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeErr(w, http.StatusNotFound, err)
		return
	case errors.Is(err, auth.ErrLastAdmin):
		writeErr(w, http.StatusConflict, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "role": req.Role, "disabled": req.Disabled})
}

func (s *Server) handleAdminChangeUsername(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("user id must be a number"))
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err = s.deps.Auth.ChangeUsername(r.Context(), id, req.Username)
	switch {
	case errors.Is(err, auth.ErrUserExists):
		writeErr(w, http.StatusConflict, err)
		return
	case errors.Is(err, auth.ErrUserNotFound):
		writeErr(w, http.StatusNotFound, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"username": strings.ToLower(strings.TrimSpace(req.Username)),
	})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("user id must be a number"))
		return
	}

	var req struct {
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := s.deps.Auth.ResetPassword(r.Context(), id, req.NewPassword); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"mustChange":    true,
		"sessionsEnded": true,
	})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("user id must be a number"))
		return
	}
	if user, ok := userOf(r); ok && user.ID == id {
		writeErr(w, http.StatusConflict, errors.New("you cannot delete your own account"))
		return
	}

	err = s.deps.Auth.DeleteUser(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeErr(w, http.StatusNotFound, err)
		return
	case errors.Is(err, auth.ErrLastAdmin):
		writeErr(w, http.StatusConflict, err)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

type auditEntry struct {
	ID         int64  `json:"id"`
	At         string `json:"at"`
	Username   string `json:"username"`
	RemoteAddr string `json:"remoteAddr"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	Result     string `json:"result"`
	Detail     string `json:"detail"`
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	rows, err := s.deps.DB.QueryContext(r.Context(), `
		SELECT id, at, COALESCE(username,''), COALESCE(remote_addr,''),
			   action, COALESCE(target,''), result, COALESCE(detail,'')
		FROM audit_logs ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	out := []auditEntry{}
	for rows.Next() {
		var e auditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Username, &e.RemoteAddr,
			&e.Action, &e.Target, &e.Result, &e.Detail); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setSessionCookies issues the session and CSRF cookies for a fresh login.
func (s *Server) setSessionCookies(w http.ResponseWriter, r *http.Request, token string) (string, error) {
	secure := s.deps.Cfg.Server.TLS.Enabled

	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: token,
		Path:  "/",
		// HttpOnly keeps the token out of reach of any script on the page, so
		// an injected snippet cannot read it and use it elsewhere.
		HttpOnly: true,
		Secure:   secure,
		// Strict rather than Lax: the admin UI is a single-page app that never
		// needs to be entered by a cross-site navigation, so there is no
		// usability cost to refusing the cookie on one.
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SessionMaxLifetime.Seconds()),
	})

	return s.rotateCSRFCookie(w, r)
}

// rotateCSRFCookie issues a new double-submit token. This cookie is
// deliberately readable by script: the SPA has to send its value back in a
// header, which is the part a cross-origin page cannot do.
func (s *Server) rotateCSRFCookie(w http.ResponseWriter, _ *http.Request) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: false,
		Secure:   s.deps.Cfg.Server.TLS.Enabled,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SessionMaxLifetime.Seconds()),
	})
	return value, nil
}

func (s *Server) clearSessionCookies(w http.ResponseWriter, _ *http.Request) {
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == sessionCookie,
			Secure:   s.deps.Cfg.Server.TLS.Enabled,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}
