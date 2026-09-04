package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/winproc"
)

type profileResponse struct {
	ID          int64             `json:"id"`
	Revision    int               `json:"revision"`
	JDKID       *int64            `json:"jdkId,omitempty"`
	JDKName     string            `json:"jdkName,omitempty"`
	HeapMin     string            `json:"heapMin,omitempty"`
	HeapMax     string            `json:"heapMax,omitempty"`
	GC          string            `json:"gc,omitempty"`
	JVMArgs     []string          `json:"jvmArgs"`
	ProgramArgs []string          `json:"programArgs"`
	Env         map[string]string `json:"env"`
	Active      bool              `json:"active"`
	Note        string            `json:"note,omitempty"`
	CreatedAt   string            `json:"createdAt"`
}

type profileUpdateRequest struct {
	JDKID       *int64            `json:"jdkId"`
	HeapMin     string            `json:"heapMin"`
	HeapMax     string            `json:"heapMax"`
	GC          string            `json:"gc"`
	JVMArgs     []string          `json:"jvmArgs"`
	ProgramArgs []string          `json:"programArgs"`
	Env         map[string]string `json:"env"`
	Note        string            `json:"note"`
}

func (s *Server) registerProfileRoutes(r chi.Router) {
	r.Get("/apps/{name}/profile", s.handleGetActiveProfile)
	r.Put("/apps/{name}/profile", s.handleUpdateProfile)
	r.Get("/apps/{name}/profiles", s.handleListProfiles)
	r.Post("/apps/{name}/profiles/{revision}/activate", s.handleActivateProfile)
	r.Get("/apps/{name}/preview", s.handlePreviewCommand)
}

func (s *Server) handleGetActiveProfile(w http.ResponseWriter, r *http.Request) {
	appID, err := s.appIDByName(r, chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	p, err := s.queryProfile(r, `
		WHERE p.app_id = ? AND p.active = 1 ORDER BY p.revision DESC LIMIT 1`, appID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("app has no active jvm profile"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	appID, err := s.appIDByName(r, chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	rows, err := s.deps.DB.QueryContext(r.Context(), profileSelect+`
		WHERE p.app_id = ? ORDER BY p.revision DESC`, appID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	out := []profileResponse{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateProfile writes a new revision rather than editing in place.
//
// JVM tuning is the change most likely to stop an app from booting, and the
// symptom (a process that exits on startup) gives no hint of what changed.
// Keeping every revision means the previous known-good settings can be
// restored without anyone having to remember them.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	appID, err := s.appIDByName(r, appName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	var req profileUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := validateProfile(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.JDKID != nil {
		var n int
		_ = s.deps.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM jdks WHERE id = ?`, *req.JDKID).Scan(&n)
		if n == 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("jdk %d is not registered", *req.JDKID))
			return
		}
	}

	jvmArgsJSON, _ := json.Marshal(orEmptySlice(req.JVMArgs))
	progArgsJSON, _ := json.Marshal(orEmptySlice(req.ProgramArgs))
	envJSON, _ := json.Marshal(orEmptyMap(req.Env))

	var revision int
	err = s.deps.DB.InTx(r.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(r.Context(),
			`SELECT COALESCE(MAX(revision), 0) + 1 FROM jvm_profiles WHERE app_id = ?`, appID).
			Scan(&revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE jvm_profiles SET active = 0 WHERE app_id = ?`, appID); err != nil {
			return err
		}
		_, err := tx.ExecContext(r.Context(), `
			INSERT INTO jvm_profiles (app_id, revision, jdk_id, heap_min, heap_max, gc,
				jvm_args, program_args, env, active, note)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			appID, revision, nullInt(req.JDKID), nullStr(req.HeapMin), nullStr(req.HeapMax),
			nullStr(req.GC), string(jvmArgsJSON), string(progArgsJSON), string(envJSON),
			nullStr(req.Note))
		return err
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	s.deps.Log.Info("jvm profile revised", "app", appName, "revision", revision)
	writeJSON(w, http.StatusOK, map[string]any{
		"app":           appName,
		"revision":      revision,
		"appliesOn":     "next start",
		"restartNeeded": s.isAppRunning(r, appID),
	})
}

func (s *Server) handleActivateProfile(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	revision := chi.URLParam(r, "revision")

	appID, err := s.appIDByName(r, appName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	err = s.deps.DB.InTx(r.Context(), func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM jvm_profiles WHERE app_id = ? AND revision = ?`, appID, revision).
			Scan(&id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE jvm_profiles SET active = 0 WHERE app_id = ?`, appID); err != nil {
			return err
		}
		_, err := tx.ExecContext(r.Context(),
			`UPDATE jvm_profiles SET active = 1 WHERE id = ?`, id)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound,
			fmt.Errorf("app %q has no profile revision %s", appName, revision))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"app":           appName,
		"revision":      revision,
		"appliesOn":     "next start",
		"restartNeeded": s.isAppRunning(r, appID),
	})
}

// handlePreviewCommand renders the exact command line a start would use.
//
// The UI shows this next to the JVM options form so an operator can see the
// resolved java.exe, the promoted jar and the full argument order before
// committing to a restart.
func (s *Server) handlePreviewCommand(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "name")
	if s.deps.Supervisor == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("supervisor not available"))
		return
	}

	spec, err := s.deps.Supervisor.PreviewLaunch(r.Context(), appName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	args := append([]string{}, spec.JVMArgs...)
	args = append(args, "-jar", spec.JarPath)
	args = append(args, spec.ProgramArgs...)

	writeJSON(w, http.StatusOK, map[string]any{
		"app":         appName,
		"javaExe":     spec.JavaExe,
		"args":        args,
		"workDir":     spec.WorkDir,
		"commandLine": quoteCommandLine(append([]string{spec.JavaExe}, args...)),
		"env":         spec.Env,
	})
}

const profileSelect = `
	SELECT p.id, p.revision, p.jdk_id, COALESCE(j.name, ''),
		   COALESCE(p.heap_min,''), COALESCE(p.heap_max,''), COALESCE(p.gc,''),
		   p.jvm_args, p.program_args, p.env, p.active, COALESCE(p.note,''), p.created_at
	FROM jvm_profiles p LEFT JOIN jdks j ON j.id = p.jdk_id`

func (s *Server) queryProfile(r *http.Request, where string, args ...any) (profileResponse, error) {
	return scanProfile(s.deps.DB.QueryRowContext(r.Context(), profileSelect+where, args...))
}

// rowScanner covers both *sql.Row and *sql.Rows so one scan helper serves the
// single-profile and list endpoints.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProfile(sc rowScanner) (profileResponse, error) {
	var (
		p                                 profileResponse
		jdkID                             sql.NullInt64
		jvmArgsJSON, progArgsJSON, envRaw string
	)
	if err := sc.Scan(&p.ID, &p.Revision, &jdkID, &p.JDKName, &p.HeapMin, &p.HeapMax,
		&p.GC, &jvmArgsJSON, &progArgsJSON, &envRaw, &p.Active, &p.Note, &p.CreatedAt); err != nil {
		return profileResponse{}, err
	}
	if jdkID.Valid {
		p.JDKID = &jdkID.Int64
	}
	p.JVMArgs = decodeStringSlice(jvmArgsJSON)
	p.ProgramArgs = decodeStringSlice(progArgsJSON)
	p.Env = decodeStringMap(envRaw)
	return p, nil
}

// heapSize matches the JVM's own -Xms/-Xmx grammar: digits with an optional
// k/m/g suffix.
var heapSize = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)

// knownGCs are the collector names JARVIS will turn into -XX:+Use<name>GC.
// Accepting free text here would build a flag that silently kills the JVM at
// startup, so the list is closed.
var knownGCs = map[string]bool{
	"G1": true, "Parallel": true, "Serial": true, "Z": true, "Shenandoah": true,
}

func validateProfile(req *profileUpdateRequest) error {
	req.HeapMin = strings.TrimSpace(req.HeapMin)
	req.HeapMax = strings.TrimSpace(req.HeapMax)
	req.GC = strings.TrimSpace(req.GC)

	for label, value := range map[string]string{"heapMin": req.HeapMin, "heapMax": req.HeapMax} {
		if value == "" {
			continue
		}
		if !heapSize.MatchString(value) {
			return fmt.Errorf("%s %q is invalid; use a size like 512m or 2g", label, value)
		}
	}
	if req.GC != "" && !knownGCs[req.GC] {
		return fmt.Errorf("gc %q is not supported; choose one of %s",
			req.GC, strings.Join(sortedKeys(knownGCs), ", "))
	}

	for _, a := range req.JVMArgs {
		if err := validateArg("jvmArgs", a); err != nil {
			return err
		}
		// These properties are how JARVIS finds its processes again after a
		// restart. A user-supplied copy would make two processes look like the
		// same instance, so the launcher owns them exclusively.
		if strings.HasPrefix(a, "-D"+winproc.AppProperty+"=") ||
			strings.HasPrefix(a, "-D"+winproc.InstanceProperty+"=") {
			return fmt.Errorf("jvmArgs may not set %q; JARVIS manages this property itself", a)
		}
		if a == "-jar" || strings.HasPrefix(a, "-jar=") {
			return errors.New("jvmArgs may not contain -jar; the promoted artifact supplies it")
		}
	}
	for _, a := range req.ProgramArgs {
		if err := validateArg("programArgs", a); err != nil {
			return err
		}
	}
	for k, v := range req.Env {
		if k == "" || strings.ContainsAny(k, "=\x00\r\n") {
			return fmt.Errorf("env key %q is invalid", k)
		}
		if strings.ContainsAny(v, "\x00") {
			return fmt.Errorf("env value for %q contains a null byte", k)
		}
	}
	return nil
}

func validateArg(field, arg string) error {
	if strings.TrimSpace(arg) == "" {
		return fmt.Errorf("%s contains an empty entry", field)
	}
	// A newline or null in an argument cannot survive the round trip through
	// CreateProcess intact, so it is a typo or a paste accident either way.
	if strings.ContainsAny(arg, "\x00\r\n") {
		return fmt.Errorf("%s entry %q contains a line break or null byte", field, arg)
	}
	return nil
}

// quoteCommandLine renders argv the way a shell would display it. It is for
// human consumption only; the launcher passes argv to CreateProcess directly
// and never parses this string back.
func quoteCommandLine(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if strings.ContainsAny(a, " \t\"") {
			parts = append(parts, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func decodeStringSlice(raw string) []string {
	out := []string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func decodeStringMap(raw string) map[string]string {
	out := map[string]string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func orEmptySlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyMap(v map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return v
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nullInt(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
