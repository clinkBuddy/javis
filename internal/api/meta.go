package api

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/sjkim/jarvis/internal/buildinfo"
)

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	DataRoot string `json:"dataRoot"`
	Uptime   string `json:"uptime"`
	Database string `json:"database"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:   "ok",
		Version:  buildinfo.Version,
		DataRoot: s.deps.Paths.Root,
		Uptime:   time.Since(s.deps.Started).Round(time.Second).String(),
		Database: "ok",
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
