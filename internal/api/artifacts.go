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
	"github.com/sjkim/jarvis/internal/auth"
)

type artifactResponse struct {
	ID                    int64  `json:"id"`
	Version               string `json:"version"`
	FileName              string `json:"fileName"`
	RelPath               string `json:"relPath"`
	SizeBytes             int64  `json:"sizeBytes"`
	SHA256                string `json:"sha256"`
	StartClass            string `json:"startClass,omitempty"`
	SpringBootVersion     string `json:"springBootVersion,omitempty"`
	ImplementationVersion string `json:"implementationVersion,omitempty"`
	BuildJDK              string `json:"buildJdk,omitempty"`
	Notes                 string `json:"notes,omitempty"`
	UploadedAt            string `json:"uploadedAt"`
	Active                bool   `json:"active"`
}

func (s *Server) registerArtifactRoutes(r chi.Router) {
	r.Get("/apps/{name}/artifacts", s.requireRole(auth.RoleViewer, s.handleListArtifacts))
	r.Post("/apps/{name}/artifacts", s.requireRole(auth.RoleOperator, s.handleUploadArtifact))
	r.Post("/apps/{name}/artifacts/{version}/activate",
		s.requireRole(auth.RoleOperator, s.handleActivateArtifact))
	// Deleting an artifact destroys the only copy of a deployed build, so it
	// sits a level above promoting one.
	r.Delete("/apps/{name}/artifacts/{version}",
		s.requireRole(auth.RoleAdmin, s.handleDeleteArtifact))
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	appID, err := s.appIDByName(r, chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	rows, err := s.deps.DB.QueryContext(r.Context(), `
		SELECT ar.id, ar.version, ar.file_name, ar.rel_path, ar.size_bytes, ar.sha256,
			   COALESCE(ar.start_class,''), COALESCE(ar.spring_boot_version,''),
			   COALESCE(ar.implementation_version,''), COALESCE(ar.build_jdk,''),
			   COALESCE(ar.notes,''), ar.uploaded_at,
			   (a.active_artifact_id = ar.id) AS active
		FROM artifacts ar JOIN apps a ON a.id = ar.app_id
		WHERE ar.app_id = ?
		ORDER BY ar.uploaded_at DESC`, appID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	out := []artifactResponse{}
	for rows.Next() {
		var a artifactResponse
		// active is NULL when no artifact is promoted yet, so it cannot scan
		// straight into a bool.
		var active sql.NullBool
		if err := rows.Scan(&a.ID, &a.Version, &a.FileName, &a.RelPath, &a.SizeBytes,
			&a.SHA256, &a.StartClass, &a.SpringBootVersion, &a.ImplementationVersion,
			&a.BuildJDK, &a.Notes, &a.UploadedAt, &active); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		a.Active = active.Valid && active.Bool
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUploadArtifact receives a jar as multipart/form-data.
//
// Fields: file (required), version (required), notes, activate ("true" to
// promote immediately).
func (s *Server) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	appID, err := s.appIDByName(r, appName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	// The jar is streamed to disk by the repository, so only the small text
	// fields are allowed to sit in memory.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid multipart form: %w", err))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	version := strings.TrimSpace(r.FormValue("version"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	activate := r.FormValue("activate") == "true"

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("a 'file' part containing the jar is required"))
		return
	}
	defer file.Close()

	repo := artifact.NewRepository(s.deps.Paths.RepoDir())
	stored, err := repo.Save(appName, version, header.Filename, file)
	switch {
	case errors.Is(err, artifact.ErrAlreadyExists):
		writeErr(w, http.StatusConflict, err)
		return
	case errors.Is(err, artifact.ErrNotAJar):
		writeErr(w, http.StatusUnsupportedMediaType, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	manifestJSON, _ := json.Marshal(stored.Manifest)
	res, err := s.deps.DB.ExecContext(r.Context(), `
		INSERT INTO artifacts (app_id, version, file_name, rel_path, size_bytes, sha256,
			manifest_json, start_class, spring_boot_version, implementation_version,
			build_jdk, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		appID, stored.Version, stored.FileName, stored.RelPath, stored.SizeBytes,
		stored.SHA256, string(manifestJSON),
		nullStr(stored.Manifest.StartClass),
		nullStr(stored.Manifest.SpringBootVersion),
		nullStr(stored.Manifest.ImplementationVersion),
		nullStr(stored.Manifest.BuildJDKSpec),
		nullStr(notes))
	if err != nil {
		// The row is the record of truth; a jar on disk that no row points to
		// would be invisible and unremovable through the UI.
		_ = repo.Remove(appName, stored.Version)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	artifactID, _ := res.LastInsertId()

	if activate {
		if _, err := s.deps.DB.ExecContext(r.Context(), `
			UPDATE apps SET active_artifact_id = ?, updated_at = datetime('now')
			WHERE id = ?`, artifactID, appID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	s.deps.Log.Info("artifact uploaded",
		"app", appName, "version", stored.Version, "size", stored.SizeBytes,
		"sha256", stored.SHA256, "activated", activate)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         artifactID,
		"app":        appName,
		"version":    stored.Version,
		"fileName":   stored.FileName,
		"sizeBytes":  stored.SizeBytes,
		"sha256":     stored.SHA256,
		"manifest":   stored.Manifest,
		"springBoot": stored.Manifest.IsSpringBootJar(),
		"active":     activate,
	})
}

func (s *Server) handleActivateArtifact(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	version := chi.URLParam(r, "version")

	appID, err := s.appIDByName(r, appName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	var artifactID int64
	err = s.deps.DB.QueryRowContext(r.Context(),
		`SELECT id FROM artifacts WHERE app_id = ? AND version = ?`, appID, version).
		Scan(&artifactID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("app %q has no version %q", appName, version))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if _, err := s.deps.DB.ExecContext(r.Context(), `
		UPDATE apps SET active_artifact_id = ?, updated_at = datetime('now')
		WHERE id = ?`, artifactID, appID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Promotion changes what the next start will run, not what is running now.
	// Saying so explicitly avoids the assumption that this restarted the app.
	writeJSON(w, http.StatusOK, map[string]any{
		"app":           appName,
		"version":       version,
		"appliesOn":     "next start",
		"restartNeeded": s.isAppRunning(r, appID),
	})
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	version := chi.URLParam(r, "version")

	appID, err := s.appIDByName(r, appName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	var artifactID int64
	var isActive bool
	err = s.deps.DB.QueryRowContext(r.Context(), `
		SELECT ar.id, COALESCE(a.active_artifact_id = ar.id, 0)
		FROM artifacts ar JOIN apps a ON a.id = ar.app_id
		WHERE ar.app_id = ? AND ar.version = ?`, appID, version).
		Scan(&artifactID, &isActive)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("app %q has no version %q", appName, version))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Deleting the promoted jar would leave the app unable to start with no
	// obvious reason why, so require an explicit promotion elsewhere first.
	if isActive {
		writeErr(w, http.StatusConflict, fmt.Errorf(
			"version %q is currently promoted; activate another version before deleting it", version))
		return
	}
	if running := s.instanceUsesArtifact(r, appID, artifactID); running {
		writeErr(w, http.StatusConflict, fmt.Errorf(
			"version %q is running right now; stop %s before deleting it", version, appName))
		return
	}

	if _, err := s.deps.DB.ExecContext(r.Context(),
		`DELETE FROM artifacts WHERE id = ?`, artifactID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	repo := artifact.NewRepository(s.deps.Paths.RepoDir())
	if err := repo.Remove(appName, version); err != nil {
		s.deps.Log.Warn("artifact row deleted but files remain",
			"app", appName, "version", version, "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"deleted": appName + "/" + version})
}

func (s *Server) appIDByName(r *http.Request, name string) (int64, error) {
	var id int64
	err := s.deps.DB.QueryRowContext(r.Context(), `SELECT id FROM apps WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("app %q not found", name)
	}
	return id, err
}

func (s *Server) isAppRunning(r *http.Request, appID int64) bool {
	var state string
	err := s.deps.DB.QueryRowContext(r.Context(),
		`SELECT state FROM instances WHERE app_id = ?`, appID).Scan(&state)
	return err == nil && (state == "RUNNING" || state == "STARTING")
}

func (s *Server) instanceUsesArtifact(r *http.Request, appID, artifactID int64) bool {
	var n int
	err := s.deps.DB.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM instances
		WHERE app_id = ? AND artifact_id = ? AND state IN ('RUNNING','STARTING')`,
		appID, artifactID).Scan(&n)
	return err == nil && n > 0
}
