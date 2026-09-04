package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/sjkim/jarvis/internal/buildinfo"
)

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	DataRoot string `json:"dataRoot,omitempty"`
	Uptime   string `json:"uptime"`
	Database string `json:"database"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:   "ok",
		Version:  buildinfo.Version,
		Uptime:   time.Since(s.deps.Started).Round(time.Second).String(),
		Database: "ok",
	}
	// Health has to answer without a session so that a down instance can be
	// told apart from a locked-out one, which means it must not describe the
	// filesystem to an anonymous caller.
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.deps.Auth.Validate(r.Context(), cookie.Value); err == nil {
			resp.DataRoot = s.deps.Paths.Root
		}
	}

	code := http.StatusOK
	if err := s.deps.DB.PingContext(r.Context()); err != nil {
		resp.Status = "degraded"
		resp.Database = err.Error()
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, resp)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":   buildinfo.Version,
		"commit":    buildinfo.Commit,
		"buildDate": buildinfo.Date,
		"goVersion": runtime.Version(),
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// writeErrCode adds a stable machine-readable code alongside the message.
//
// The UI has to react differently to a few specific failures — sign in again,
// change your password, reload for a fresh CSRF token — and matching on
// English prose would break the moment a message is reworded.
func writeErrCode(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}

// nullID keeps a zero user id out of the audit table, where it would look like
// a real foreign key.
func nullID(id int64) sql.NullInt64 {
	if id == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}
