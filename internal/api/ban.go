package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/ban"
)

func (s *Server) registerBanRoutes(r chi.Router) {
	r.Get("/bans", s.requireRole(auth.RoleAdmin, s.handleListBans))
	r.Post("/bans", s.requireRole(auth.RoleAdmin, s.handleCreateBan))
	r.Delete("/bans/{ip}", s.requireRole(auth.RoleAdmin, s.handleDeleteBan))
}

func (s *Server) capturePeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ban.Normalize(r.RemoteAddr)
		ctx := context.WithValue(r.Context(), ctxPeerIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// dropBanned closes the TCP connection without writing an HTTP response.
func (s *Server) dropBanned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Bans != nil && s.deps.Bans.Blocked(peerIP(r)) {
			abortSilent(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) detectProbes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Bans != nil && ban.Probe(r.URL.Path) {
			ip := peerIP(r)
			_ = s.deps.Bans.Ban(r.Context(), ip, "scanner probe "+r.URL.Path, "auto")
			if s.deps.Bans.Blocked(ip) {
				abortSilent(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func abortSilent(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

func (s *Server) handleListBans(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Bans == nil {
		writeJSON(w, http.StatusOK, []ban.Entry{})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Bans.List())
}

func (s *Server) handleCreateBan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Bans == nil {
		writeErr(w, http.StatusServiceUnavailable, errString("ban list is not available"))
		return
	}
	var req struct {
		IP     string `json:"ip"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if ban.Normalize(req.IP) == "" {
		writeErr(w, http.StatusBadRequest, errString("ip is required"))
		return
	}
	if ban.Exempt(req.IP) {
		writeErr(w, http.StatusBadRequest, errString("loopback addresses cannot be banned"))
		return
	}
	who := "admin"
	if u, ok := userOf(r); ok {
		who = u.Username
	}
	if req.Reason == "" {
		req.Reason = "manual"
	}
	if err := s.deps.Bans.Ban(r.Context(), req.IP, req.Reason, who); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"ip": ban.Normalize(req.IP)})
}

func (s *Server) handleDeleteBan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Bans == nil {
		writeErr(w, http.StatusServiceUnavailable, errString("ban list is not available"))
		return
	}
	ip := chi.URLParam(r, "ip")
	if err := s.deps.Bans.Unban(r.Context(), ip); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"unbanned": ban.Normalize(ip)})
}
