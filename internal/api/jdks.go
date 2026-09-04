package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/jdk"
)

type jdkResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	JavaHome  string `json:"javaHome"`
	JavaExe   string `json:"javaExe"`
	Vendor    string `json:"vendor,omitempty"`
	Version   string `json:"version,omitempty"`
	Major     int    `json:"major"`
	IsDefault bool   `json:"isDefault"`
}

type jdkCreateRequest struct {
	Name      string `json:"name"`
	JavaExe   string `json:"javaExe"`
	IsDefault bool   `json:"isDefault"`
}

// registerJDKRoutes: the list is readable by anyone who can see a profile
// form, but registering a JDK names an executable that JARVIS will then run,
// so changes are admin-only.
func (s *Server) registerJDKRoutes(r chi.Router) {
	r.Get("/jdks", s.requireRole(auth.RoleViewer, s.handleListJDKs))
	r.Post("/jdks", s.requireRole(auth.RoleAdmin, s.handleCreateJDK))
	r.Post("/jdks/scan", s.requireRole(auth.RoleAdmin, s.handleScanJDKs))
	r.Delete("/jdks/{id}", s.requireRole(auth.RoleAdmin, s.handleDeleteJDK))
	r.Post("/jdks/{id}/default", s.requireRole(auth.RoleAdmin, s.handleSetDefaultJDK))
}

func (s *Server) handleListJDKs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.QueryContext(r.Context(), `
		SELECT id, name, java_home, java_exe, COALESCE(vendor,''), COALESCE(version,''),
			   COALESCE(major,0), is_default
		FROM jdks ORDER BY major DESC, name`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	out := []jdkResponse{}
	for rows.Next() {
		var j jdkResponse
		if err := rows.Scan(&j.ID, &j.Name, &j.JavaHome, &j.JavaExe, &j.Vendor,
			&j.Version, &j.Major, &j.IsDefault); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleScanJDKs probes the host and registers anything new it finds.
//
// Existing rows are matched on java_home and left alone, so re-scanning is
// safe and will not renumber the ids that jvm_profiles reference.
func (s *Server) handleScanJDKs(w http.ResponseWriter, r *http.Request) {
	found := jdk.Scan(r.Context())
	if len(found) == 0 {
		writeErr(w, http.StatusNotFound, errors.New(
			"no Java installation found; register one explicitly with its java.exe path"))
		return
	}

	added := []jdkResponse{}
	for _, info := range found {
		var existing int64
		err := s.deps.DB.QueryRowContext(r.Context(),
			`SELECT id FROM jdks WHERE java_home = ?`, info.JavaHome).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}

		name := s.uniqueJDKName(r, info)
		res, err := s.deps.DB.ExecContext(r.Context(), `
			INSERT INTO jdks (name, java_home, java_exe, vendor, version, major)
			VALUES (?, ?, ?, ?, ?, ?)`,
			name, info.JavaHome, info.JavaExe, info.Vendor, info.Version, info.Major)
		if err != nil {
			s.deps.Log.Warn("could not register detected jdk",
				"javaHome", info.JavaHome, "err", err)
			continue
		}
		id, _ := res.LastInsertId()
		added = append(added, jdkResponse{
			ID: id, Name: name, JavaHome: info.JavaHome, JavaExe: info.JavaExe,
			Vendor: info.Vendor, Version: info.Version, Major: info.Major,
		})
	}

	// The first JDK to be registered becomes the default; otherwise every app
	// would need an explicit choice before it could start at all.
	s.ensureDefaultJDK(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"found": len(found),
		"added": added,
	})
}

func (s *Server) handleCreateJDK(w http.ResponseWriter, r *http.Request) {
	var req jdkCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.JavaExe = strings.TrimSpace(req.JavaExe)
	if req.JavaExe == "" {
		writeErr(w, http.StatusBadRequest, errors.New("javaExe is required"))
		return
	}

	// Probing before storing means a typo is rejected here rather than at the
	// first start attempt of whatever app was pointed at it.
	info, err := jdk.Resolve(r.Context(), req.JavaExe)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = s.uniqueJDKName(r, info)
	}

	res, err := s.deps.DB.ExecContext(r.Context(), `
		INSERT INTO jdks (name, java_home, java_exe, vendor, version, major)
		VALUES (?, ?, ?, ?, ?, ?)`,
		name, info.JavaHome, info.JavaExe, info.Vendor, info.Version, info.Major)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, http.StatusConflict, fmt.Errorf("a jdk named %q is already registered", name))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	id, _ := res.LastInsertId()

	if req.IsDefault {
		if err := s.setDefaultJDK(r, id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		s.ensureDefaultJDK(r)
	}

	writeJSON(w, http.StatusCreated, jdkResponse{
		ID: id, Name: name, JavaHome: info.JavaHome, JavaExe: info.JavaExe,
		Vendor: info.Vendor, Version: info.Version, Major: info.Major,
		IsDefault: req.IsDefault,
	})
}

func (s *Server) handleDeleteJDK(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// jvm_profiles.jdk_id has no ON DELETE clause, so removing a referenced
	// JDK would leave profiles pointing at a row that no longer exists.
	var inUse int
	if err := s.deps.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jvm_profiles WHERE jdk_id = ?`, id).Scan(&inUse); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if inUse > 0 {
		writeErr(w, http.StatusConflict, fmt.Errorf(
			"this jdk is referenced by %d jvm profile(s); point them elsewhere first", inUse))
		return
	}

	res, err := s.deps.DB.ExecContext(r.Context(), `DELETE FROM jdks WHERE id = ?`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, errors.New("jdk not found"))
		return
	}
	s.ensureDefaultJDK(r)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleSetDefaultJDK(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var numericID int64
	if err := s.deps.DB.QueryRowContext(r.Context(),
		`SELECT id FROM jdks WHERE id = ?`, id).Scan(&numericID); err != nil {
		writeErr(w, http.StatusNotFound, errors.New("jdk not found"))
		return
	}
	if err := s.setDefaultJDK(r, numericID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"default": numericID})
}

func (s *Server) setDefaultJDK(r *http.Request, id int64) error {
	return s.deps.DB.InTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), `UPDATE jdks SET is_default = 0`); err != nil {
			return err
		}
		_, err := tx.ExecContext(r.Context(), `UPDATE jdks SET is_default = 1 WHERE id = ?`, id)
		return err
	})
}

// ensureDefaultJDK promotes the newest major version when nothing is marked
// default, which happens after the first scan and after the default is deleted.
func (s *Server) ensureDefaultJDK(r *http.Request) {
	var n int
	if err := s.deps.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jdks WHERE is_default = 1`).Scan(&n); err != nil || n > 0 {
		return
	}
	var id int64
	if err := s.deps.DB.QueryRowContext(r.Context(),
		`SELECT id FROM jdks ORDER BY major DESC, id LIMIT 1`).Scan(&id); err != nil {
		return
	}
	if err := s.setDefaultJDK(r, id); err != nil {
		s.deps.Log.Warn("could not select a default jdk", "err", err)
	}
}

// uniqueJDKName derives a readable label and appends a suffix on collision,
// because two vendors shipping the same major version is common.
func (s *Server) uniqueJDKName(r *http.Request, info jdk.Info) string {
	base := fmt.Sprintf("jdk-%d", info.Major)
	if vendor := shortVendor(info.Vendor); vendor != "" {
		base = fmt.Sprintf("%s-%d", vendor, info.Major)
	}

	name := base
	for i := 2; i < 50; i++ {
		var n int
		if err := s.deps.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM jdks WHERE name = ?`, name).Scan(&n); err != nil || n == 0 {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

func shortVendor(vendor string) string {
	v := strings.ToLower(vendor)
	switch {
	case strings.Contains(v, "oracle"):
		return "oracle"
	case strings.Contains(v, "eclipse") || strings.Contains(v, "temurin") || strings.Contains(v, "adoptium"):
		return "temurin"
	case strings.Contains(v, "amazon") || strings.Contains(v, "corretto"):
		return "corretto"
	case strings.Contains(v, "azul") || strings.Contains(v, "zulu"):
		return "zulu"
	case strings.Contains(v, "microsoft"):
		return "microsoft"
	case strings.Contains(v, "bellsoft") || strings.Contains(v, "liberica"):
		return "liberica"
	case strings.Contains(v, "graal"):
		return "graalvm"
	case strings.Contains(v, "sap"):
		return "sapmachine"
	case strings.Contains(v, "ibm") || strings.Contains(v, "semeru"):
		return "semeru"
	case strings.Contains(v, "red hat") || strings.Contains(v, "redhat"):
		return "redhat"
	}
	// A vendor string like "N/A" or a build server hostname is not worth
	// putting in a name that has to stay stable.
	return ""
}
