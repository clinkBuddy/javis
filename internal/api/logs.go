package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/applog"
	"github.com/sjkim/jarvis/internal/auth"
)

func (s *Server) registerLogRoutes(r chi.Router) {
	r.Get("/apps/{name}/logs", s.requireRole(auth.RoleViewer, s.handleAppLogs))
	r.Get("/apps/{name}/logs/stream", s.requireRole(auth.RoleViewer, s.handleAppLogStream))
}

func (s *Server) consoleLogPath(r *http.Request, name string) (string, error) {
	var path string
	err := s.deps.DB.QueryRowContext(r.Context(), `
		SELECT i.console_log FROM apps a JOIN instances i ON i.app_id = a.id
		WHERE a.name = ? AND i.console_log IS NOT NULL AND i.console_log <> ''`, name).
		Scan(&path)
	if err != nil {
		return "", errors.New("this app has no console log yet")
	}
	return path, nil
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	path, err := s.consoleLogPath(r, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	n := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}

	lines, size, err := applog.TailLines(path, n)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":  path,
		"size":  size,
		"lines": lines,
	})
}

// handleAppLogStream pushes new console output as server-sent events.
//
// Polling the file from the browser would either miss lines written between
// requests or re-send the whole tail every few seconds. SSE lets the server
// hold the offset and ship only what is new.
func (s *Server) handleAppLogStream(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	path, err := s.consoleLogPath(r, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming is not available"))
		return
	}

	offset := int64(-1)
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			offset = n
		}
	}
	if offset < 0 {
		if _, size, err := applog.TailLines(path, 1); err == nil {
			offset = size
		} else {
			offset = 0
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			data, next, err := applog.ReadSince(path, offset)
			if err != nil {
				writeSSE(w, "error", err.Error())
				flusher.Flush()
				return
			}
			if len(data) == 0 {
				continue
			}
			offset = next
			payload, _ := json.Marshal(map[string]any{
				"offset": next,
				"text":   string(data),
			})
			writeSSE(w, "chunk", string(payload))
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event, data string) {
	_, _ = w.Write([]byte("event: " + event + "\n"))
	_, _ = w.Write([]byte("data: " + data + "\n\n"))
}
