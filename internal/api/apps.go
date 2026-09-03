package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type appCreateRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	ShutdownURL string `json:"shutdownUrl,omitempty"`
}

type appResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	TargetKind  string `json:"targetKind"`
	ShutdownURL string `json:"shutdownUrl,omitempty"`
	Autostart   bool   `json:"autostart"`
	Watchdog    bool   `json:"watchdog"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

func (s *Server) registerAppRoutes(r chi.Router) {
	r.Get("/apps", s.handleListApps)
	r.Post("/apps", s.handleCreateApp)
	r.Get("/apps/{name}", s.handleGetApp)
	r.Delete("/apps/{name}", s.handleDeleteApp)
	r.Post("/apps/{name}/start", s.handleStartApp)
	r.Post("/apps/{name}/stop", s.handleStopApp)
	r.Post("/apps/{name}/restart", s.handleRestartApp)
	r.Get("/apps/{name}/status", s.handleAppStatus)
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.QueryContext(r.Context(), `
		SELECT id, name, COALESCE(display_name,''), COALESCE(description,''),
			   target_kind, COALESCE(shutdown_url,''), autostart, watchdog,
			   created_at, updated_at
		FROM apps ORDER BY name`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var apps []appResponse
	for rows.Next() {
		var a appResponse
		if err := rows.Scan(&a.ID, &a.Name, &a.DisplayName, &a.Description,
			&a.TargetKind, &a.ShutdownURL, &a.Autostart, &a.Watchdog,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		apps = append(apps, a)
	}
	if apps == nil {
		apps = []appResponse{}
	}
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req appCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	res, err := s.deps.DB.ExecContext(r.Context(), `
		INSERT INTO apps (name, display_name, description, shutdown_url)
		VALUES (?, ?, ?, ?)`,
		req.Name, nullStr(req.DisplayName), nullStr(req.Description), nullStr(req.ShutdownURL))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, http.StatusConflict, errors.New("app already exists"))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	id, _ := res.LastInsertId()

	// Create default JVM profile.
	_, _ = s.deps.DB.ExecContext(r.Context(), `
		INSERT INTO jvm_profiles (app_id, revision, active) VALUES (?, 1, 1)`, id)

	// Create directory tree.
	_ = s.deps.Paths.EnsureApp(req.Name)

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": req.Name})
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var a appResponse
	err := s.deps.DB.QueryRowContext(r.Context(), `
		SELECT id, name, COALESCE(display_name,''), COALESCE(description,''),
			   target_kind, COALESCE(shutdown_url,''), autostart, watchdog,
			   created_at, updated_at
		FROM apps WHERE name = ?`, name).
		Scan(&a.ID, &a.Name, &a.DisplayName, &a.Description,
			&a.TargetKind, &a.ShutdownURL, &a.Autostart, &a.Watchdog,
			&a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("app not found"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res, err := s.deps.DB.ExecContext(r.Context(), `DELETE FROM apps WHERE name = ?`, name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, errors.New("app not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

func (s *Server) handleStartApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.deps.Supervisor == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("supervisor not available"))
		return
	}
	h, err := s.deps.Supervisor.StartApp(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":      name,
		"pid":      h.PID,
		"instance": h.InstanceID,
		"state":    h.State,
	})
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.deps.Supervisor == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("supervisor not available"))
		return
	}
	res, err := s.deps.Supervisor.StopApp(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":      name,
		"method":   res.Method,
		"duration": res.Duration.String(),
	})
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.deps.Supervisor == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("supervisor not available"))
		return
	}
	h, err := s.deps.Supervisor.RestartApp(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":      name,
		"pid":      h.PID,
		"instance": h.InstanceID,
		"state":    h.State,
	})
}

func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.deps.Supervisor == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("supervisor not available"))
		return
	}
	h, err := s.deps.Supervisor.GetStatus(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":        name,
		"state":      h.State,
		"pid":        h.PID,
		"instance":   h.InstanceID,
		"consoleLog": h.ConsoleLog,
	})
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
