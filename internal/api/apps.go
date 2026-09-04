package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/artifact"
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

	// Live view, joined in so the dashboard needs one request rather than one
	// per app.
	State          string `json:"state"`
	PID            uint32 `json:"pid,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	RunningVersion string `json:"runningVersion,omitempty"`
	ActiveVersion  string `json:"activeVersion,omitempty"`
	ArtifactCount  int    `json:"artifactCount"`
}

// appSelect joins the single instance row and the promoted artifact onto each
// app. state falls back to STOPPED because an app that has never been started
// has no instance row at all.
const appSelect = `
	SELECT a.id, a.name, COALESCE(a.display_name,''), COALESCE(a.description,''),
		   a.target_kind, COALESCE(a.shutdown_url,''), a.autostart, a.watchdog,
		   a.created_at, a.updated_at,
		   COALESCE(i.state, 'STOPPED'), COALESCE(i.pid, 0), COALESCE(i.started_at, ''),
		   COALESCE(run.version, ''), COALESCE(act.version, ''),
		   (SELECT COUNT(*) FROM artifacts WHERE app_id = a.id)
	FROM apps a
	LEFT JOIN instances i ON i.app_id = a.id
	LEFT JOIN artifacts run ON run.id = i.artifact_id
	LEFT JOIN artifacts act ON act.id = a.active_artifact_id`

func scanApp(sc rowScanner) (appResponse, error) {
	var a appResponse
	err := sc.Scan(&a.ID, &a.Name, &a.DisplayName, &a.Description,
		&a.TargetKind, &a.ShutdownURL, &a.Autostart, &a.Watchdog,
		&a.CreatedAt, &a.UpdatedAt,
		&a.State, &a.PID, &a.StartedAt,
		&a.RunningVersion, &a.ActiveVersion, &a.ArtifactCount)
	return a, err
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
	rows, err := s.deps.DB.QueryContext(r.Context(), appSelect+` ORDER BY a.name`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	apps := []appResponse{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// The stored state is only as fresh as the last event that touched it. A
	// process killed from Task Manager leaves the row saying RUNNING, so the
	// claim is confirmed against the OS before it reaches the dashboard.
	s.refreshStates(r, apps)
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) refreshStates(r *http.Request, apps []appResponse) {
	if s.deps.Supervisor == nil {
		return
	}
	for i := range apps {
		if apps[i].State != "RUNNING" && apps[i].State != "STARTING" {
			continue
		}
		h, err := s.deps.Supervisor.GetStatus(r.Context(), apps[i].Name)
		if err != nil {
			continue
		}
		apps[i].State = string(h.State)
	}
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req appCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	// The name becomes a directory under repo/ and apps/ and a path segment in
	// every REST call for this app, so it has to be checked here rather than
	// at first use.
	if err := artifact.ValidateName("app name", req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
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
	a, err := scanApp(s.deps.DB.QueryRowContext(r.Context(), appSelect+` WHERE a.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("app not found"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	one := []appResponse{a}
	s.refreshStates(r, one)
	writeJSON(w, http.StatusOK, one[0])
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	appID, err := s.appIDByName(r, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	// Deleting the record of a live process would orphan it: JARVIS would no
	// longer know the PID or the markers, and stopping it would become a
	// manual Task Manager job.
	if s.isAppRunning(r, appID) {
		writeErr(w, http.StatusConflict, fmt.Errorf("stop %s before deleting it", name))
		return
	}

	if _, err := s.deps.DB.ExecContext(r.Context(), `DELETE FROM apps WHERE id = ?`, appID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Uploaded jars are deliberately left on disk. They are the only copy of a
	// deployed build and an accidental app deletion should not destroy them.
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":       name,
		"artifactsKept": s.deps.Paths.RepoDir(),
	})
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

	var version, revision sql.NullString
	_ = s.deps.DB.QueryRowContext(r.Context(), `
		SELECT ar.version, p.revision
		FROM instances i
		LEFT JOIN artifacts ar ON ar.id = i.artifact_id
		LEFT JOIN jvm_profiles p ON p.id = i.profile_id
		WHERE i.app_id = (SELECT id FROM apps WHERE name = ?)`, name).
		Scan(&version, &revision)

	writeJSON(w, http.StatusOK, map[string]any{
		"app":             name,
		"state":           h.State,
		"pid":             h.PID,
		"instance":        h.InstanceID,
		"consoleLog":      h.ConsoleLog,
		"commandLine":     h.CommandLine,
		"runningVersion":  version.String,
		"runningRevision": revision.String,
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
