// Package supervisor orchestrates the lifecycle of managed applications. It
// owns the bridge between the database (where configuration and state live)
// and the runner (which does the OS work). On startup it reconciles the DB
// against reality by asking the runner to discover live processes, and then
// watches each one in a goroutine that restarts it if it crashes.
package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/jdk"
	"github.com/sjkim/jarvis/internal/runner"
	"github.com/sjkim/jarvis/internal/store"
)

type Supervisor struct {
	db     *store.DB
	paths  config.Paths
	runner runner.Runner
	log    *slog.Logger

	mu       sync.Mutex
	watches  map[string]context.CancelFunc // appName → cancel watcher
	shutdown bool
}

func New(db *store.DB, paths config.Paths, r runner.Runner, log *slog.Logger) *Supervisor {
	return &Supervisor{
		db:      db,
		paths:   paths,
		runner:  r,
		log:     log,
		watches: make(map[string]context.CancelFunc),
	}
}

// Reconcile synchronises the database with the actual state of the OS. It
// must be called once during startup, before the API accepts requests.
//
// For every app whose DB record says RUNNING or STARTING, it checks whether
// the process is really alive and either re-attaches or marks it stopped.
// It also discovers orphans—processes with JARVIS markers that are not in
// the DB—and logs them.
type appRow struct {
	id   int64
	name string
}

func (s *Supervisor) Reconcile(ctx context.Context) error {
	s.log.Info("reconciling managed processes with OS state")

	// 1) Get all apps from DB.
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.name FROM apps a`)
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()

	var apps []appRow
	for rows.Next() {
		var r appRow
		if err := rows.Scan(&r.id, &r.name); err != nil {
			return err
		}
		apps = append(apps, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 2) For each app that claims to be running, verify.
	for _, app := range apps {
		if err := s.reconcileApp(ctx, app.id, app.name); err != nil {
			s.log.Error("reconcile failed", "app", app.name, "err", err)
		}
	}

	// 3) Discover all JARVIS-marked processes and report orphans.
	allLive, err := s.runner.DiscoverAll(ctx)
	if err != nil {
		s.log.Warn("could not scan for orphans", "err", err)
	} else {
		s.reportOrphans(ctx, allLive, apps)
	}

	return nil
}

func (s *Supervisor) reconcileApp(ctx context.Context, appID int64, appName string) error {
	var (
		state        string
		pid          sql.NullInt64
		createTimeDB sql.NullInt64
		instanceUUID sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT state, pid, process_create_time, instance_uuid
		FROM instances WHERE app_id = ?`, appID).
		Scan(&state, &pid, &createTimeDB, &instanceUUID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // never been started
	}
	if err != nil {
		return err
	}

	if state != string(runner.StateRunning) && state != string(runner.StateStarting) {
		return nil
	}

	h := runner.Handle{
		AppName:    appName,
		InstanceID: instanceUUID.String,
		PID:        uint32(pid.Int64),
		CreateTime: createTimeDB.Int64,
	}

	alive, err := s.runner.Alive(h)
	if err != nil {
		return fmt.Errorf("check alive: %w", err)
	}

	if alive {
		s.log.Info("re-attached to running process",
			"app", appName, "pid", h.PID, "instance", h.InstanceID)
		h.State = runner.StateRunning
		s.startWatcher(appName, appID, h)
		return nil
	}

	// Try discovery by marker in case the PID was recycled.
	if instanceUUID.Valid && instanceUUID.String != "" {
		disc, err := s.runner.Discover(ctx, appName)
		if err == nil {
			for _, d := range disc {
				if d.InstanceID == instanceUUID.String {
					s.log.Info("re-discovered process by marker (PID changed)",
						"app", appName, "oldPid", h.PID, "newPid", d.PID)
					if err := s.updateInstance(ctx, appID, d); err != nil {
						return err
					}
					s.startWatcher(appName, appID, d)
					return nil
				}
			}
		}
	}

	// Process is gone.
	s.log.Warn("process was running but is no longer alive",
		"app", appName, "pid", h.PID, "instance", h.InstanceID)
	_, err = s.db.ExecContext(ctx, `
		UPDATE instances SET state = ?, stopped_at = datetime('now'), updated_at = datetime('now')
		WHERE app_id = ?`, string(runner.StateStopped), appID)
	return err
}

func (s *Supervisor) reportOrphans(ctx context.Context, live []runner.Handle, _ []appRow) {
	// Build a set of known instance IDs from the DB.
	rows, err := s.db.QueryContext(ctx, `SELECT instance_uuid FROM instances WHERE instance_uuid IS NOT NULL`)
	if err != nil {
		return
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err == nil {
			known[uuid] = true
		}
	}

	for _, h := range live {
		if h.InstanceID != "" && !known[h.InstanceID] {
			s.log.Warn("orphan process found",
				"app", h.AppName, "pid", h.PID, "instance", h.InstanceID,
				"commandLine", h.CommandLine)
		}
	}
}

// StartApp launches the application and begins watching it.
func (s *Supervisor) StartApp(ctx context.Context, appName string) (runner.Handle, error) {
	s.mu.Lock()
	if _, exists := s.watches[appName]; exists {
		s.mu.Unlock()
		return runner.Handle{}, fmt.Errorf("app %q is already running or being watched", appName)
	}
	s.mu.Unlock()

	spec, appID, err := s.buildLaunchSpec(ctx, appName)
	if err != nil {
		return runner.Handle{}, err
	}

	h, err := s.runner.Start(ctx, spec)
	if err != nil {
		return runner.Handle{}, fmt.Errorf("start %s: %w", appName, err)
	}

	if err := s.insertInstance(ctx, appID, h, spec); err != nil {
		s.log.Error("failed to record instance in DB", "app", appName, "err", err)
	}

	s.startWatcher(appName, appID, h)
	return h, nil
}

// StopApp gracefully stops the application.
func (s *Supervisor) StopApp(ctx context.Context, appName string) (runner.StopResult, error) {
	s.cancelWatcher(appName)

	var appID int64
	var h runner.Handle
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id, i.pid, i.process_create_time, i.instance_uuid
		FROM apps a JOIN instances i ON i.app_id = a.id
		WHERE a.name = ? AND i.state IN ('RUNNING','STARTING')`, appName).
		Scan(&appID, &h.PID, &h.CreateTime, &h.InstanceID)
	if err != nil {
		return runner.StopResult{}, fmt.Errorf("app %q is not running: %w", appName, err)
	}
	h.AppName = appName

	// Read stop options from the app config.
	var shutdownURL sql.NullString
	var stopTimeout int
	_ = s.db.QueryRowContext(ctx, `
		SELECT shutdown_url, stop_timeout_sec FROM apps WHERE id = ?`, appID).
		Scan(&shutdownURL, &stopTimeout)

	grace := time.Duration(stopTimeout) * time.Second
	if grace <= 0 {
		grace = 30 * time.Second
	}

	localRunner, ok := s.runner.(*runner.LocalRunner)
	var res runner.StopResult
	if ok && shutdownURL.Valid && shutdownURL.String != "" {
		res, err = localRunner.StopWithOptions(ctx, h, shutdownURL.String, nil, grace)
	} else {
		res, err = s.runner.Stop(ctx, h)
	}
	if err != nil {
		return res, err
	}

	_, _ = s.db.ExecContext(ctx, `
		UPDATE instances SET state = ?, stopped_at = datetime('now'), updated_at = datetime('now')
		WHERE app_id = ?`, string(runner.StateStopped), appID)

	s.log.Info("app stopped", "app", appName, "method", res.Method, "dur", res.Duration)
	return res, nil
}

// RestartApp stops then starts.
func (s *Supervisor) RestartApp(ctx context.Context, appName string) (runner.Handle, error) {
	if _, err := s.StopApp(ctx, appName); err != nil {
		s.log.Debug("stop before restart", "app", appName, "err", err)
	}
	time.Sleep(500 * time.Millisecond) // let the port be released
	return s.StartApp(ctx, appName)
}

// GetStatus returns the current handle for an app, or an error if not tracked.
func (s *Supervisor) GetStatus(ctx context.Context, appName string) (runner.Handle, error) {
	var h runner.Handle
	var state string
	var startedAt, stoppedAt sql.NullString
	var exitCode sql.NullInt64
	var consolePath sql.NullString
	var cmdLine sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT i.state, i.pid, i.process_create_time, i.instance_uuid,
			   i.started_at, i.stopped_at, i.exit_code, i.console_log, i.command_line
		FROM apps a JOIN instances i ON i.app_id = a.id
		WHERE a.name = ?`, appName).
		Scan(&state, &h.PID, &h.CreateTime, &h.InstanceID,
			&startedAt, &stoppedAt, &exitCode, &consolePath, &cmdLine)
	if err != nil {
		return h, err
	}
	h.AppName = appName
	h.State = runner.State(state)
	if consolePath.Valid {
		h.ConsoleLog = consolePath.String
	}
	if cmdLine.Valid {
		h.CommandLine = cmdLine.String
	}
	if exitCode.Valid {
		h.ExitCode = int(exitCode.Int64)
	}

	// Refresh liveness for running states.
	if h.State == runner.StateRunning || h.State == runner.StateStarting {
		alive, _ := s.runner.Alive(h)
		if !alive {
			h.State = runner.StateStopped
		}
	}
	return h, nil
}

func (s *Supervisor) buildLaunchSpec(ctx context.Context, appName string) (runner.LaunchSpec, int64, error) {
	var appID int64
	var shutdownURL sql.NullString
	var stopTimeout int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, shutdown_url, stop_timeout_sec FROM apps WHERE name = ?`, appName).
		Scan(&appID, &shutdownURL, &stopTimeout)
	if err != nil {
		return runner.LaunchSpec{}, 0, fmt.Errorf("app %q not found: %w", appName, err)
	}

	// Read active JVM profile.
	var heapMin, heapMax, gc sql.NullString
	var jvmArgsJSON, progArgsJSON, envJSON string
	var jdkID sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT jdk_id, heap_min, heap_max, gc, jvm_args, program_args, env
		FROM jvm_profiles WHERE app_id = ? AND active = 1
		ORDER BY revision DESC LIMIT 1`, appID).
		Scan(&jdkID, &heapMin, &heapMax, &gc, &jvmArgsJSON, &progArgsJSON, &envJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return runner.LaunchSpec{}, 0, fmt.Errorf("read jvm profile: %w", err)
	}

	// Resolve java.exe.
	javaExe := "java"
	if jdkID.Valid {
		var javaPath string
		if err := s.db.QueryRowContext(ctx, `SELECT java_exe FROM jdks WHERE id = ?`, jdkID.Int64).Scan(&javaPath); err == nil {
			javaExe = javaPath
		}
	}
	resolved, err := jdk.Resolve(ctx, javaExe)
	if err != nil {
		return runner.LaunchSpec{}, 0, fmt.Errorf("resolve java: %w", err)
	}

	// Build JVM args from the profile.
	var jvmArgs []string
	if heapMin.Valid && heapMin.String != "" {
		jvmArgs = append(jvmArgs, "-Xms"+heapMin.String)
	}
	if heapMax.Valid && heapMax.String != "" {
		jvmArgs = append(jvmArgs, "-Xmx"+heapMax.String)
	}
	if gc.Valid && gc.String != "" {
		jvmArgs = append(jvmArgs, "-XX:+Use"+gc.String+"GC")
	}
	jvmArgs = append(jvmArgs, parseJSONStringArray(jvmArgsJSON)...)
	progArgs := parseJSONStringArray(progArgsJSON)

	// Find the active artifact.
	var jarRelPath string
	err = s.db.QueryRowContext(ctx, `
		SELECT rel_path FROM artifacts
		WHERE app_id = ? ORDER BY uploaded_at DESC LIMIT 1`, appID).Scan(&jarRelPath)
	if err != nil {
		return runner.LaunchSpec{}, 0, fmt.Errorf("no artifact for %q: %w", appName, err)
	}
	jarPath := filepath.Join(s.paths.RepoDir(), jarRelPath)

	// Paths.
	_ = s.paths.EnsureApp(appName)
	workDir := s.paths.AppWorkDir(appName)
	consoleLog := filepath.Join(s.paths.AppLogDir(appName),
		fmt.Sprintf("console-%s.log", time.Now().Format("20060102-150405")))

	return runner.LaunchSpec{
		AppName:     appName,
		JavaExe:     resolved.JavaExe,
		JarPath:     jarPath,
		JVMArgs:     jvmArgs,
		ProgramArgs: progArgs,
		WorkDir:     workDir,
		ConsoleLog:  consoleLog,
		ShutdownURL: shutdownURL.String,
		StopGrace:   time.Duration(stopTimeout) * time.Second,
	}, appID, nil
}

func (s *Supervisor) insertInstance(ctx context.Context, appID int64, h runner.Handle, spec runner.LaunchSpec) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO instances (app_id, state, instance_uuid, pid, process_create_time,
			command_line, console_log, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(app_id) DO UPDATE SET
			state = excluded.state,
			instance_uuid = excluded.instance_uuid,
			pid = excluded.pid,
			process_create_time = excluded.process_create_time,
			command_line = excluded.command_line,
			console_log = excluded.console_log,
			started_at = excluded.started_at,
			stopped_at = NULL,
			exit_code = NULL,
			restart_count = 0,
			last_error = NULL,
			updated_at = datetime('now')`,
		appID, string(runner.StateRunning), h.InstanceID, h.PID, h.CreateTime,
		h.CommandLine, h.ConsoleLog)
	return err
}

func (s *Supervisor) updateInstance(ctx context.Context, appID int64, h runner.Handle) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE instances SET pid = ?, process_create_time = ?, state = ?,
			updated_at = datetime('now')
		WHERE app_id = ?`,
		h.PID, h.CreateTime, string(runner.StateRunning), appID)
	return err
}

// startWatcher launches a goroutine that waits for the process to exit and
// updates the DB. If JARVIS is shutting down, it does nothing.
func (s *Supervisor) startWatcher(appName string, appID int64, h runner.Handle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return
	}
	if cancel, exists := s.watches[appName]; exists {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.watches[appName] = cancel

	go s.watch(ctx, appName, appID, h)
}

func (s *Supervisor) cancelWatcher(appName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.watches[appName]; ok {
		cancel()
		delete(s.watches, appName)
	}
}

func (s *Supervisor) watch(ctx context.Context, appName string, appID int64, h runner.Handle) {
	defer func() {
		s.mu.Lock()
		delete(s.watches, appName)
		s.mu.Unlock()
	}()

	// Poll-wait so context cancellation is responsive.
	for {
		gone, err := s.runner.WaitExit(ctx, h, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return // JARVIS is shutting down or we were cancelled
			}
			s.log.Error("watcher error", "app", appName, "err", err)
			return
		}
		if gone {
			break
		}
	}

	s.log.Warn("process exited", "app", appName, "pid", h.PID)
	_, _ = s.db.ExecContext(context.Background(), `
		UPDATE instances SET state = ?, stopped_at = datetime('now'), updated_at = datetime('now')
		WHERE app_id = ?`, string(runner.StateStopped), appID)

	// TODO(P4): Watchdog auto-restart with exponential backoff goes here.
}

// Shutdown cancels all watchers. It does NOT stop managed java processes;
// those survive by design.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdown = true
	for name, cancel := range s.watches {
		cancel()
		delete(s.watches, name)
	}
	s.log.Info("supervisor shut down; managed processes are still running")
}

// parseJSONStringArray decodes a JSON array of strings. Our own code writes
// these, so they are always well-formed or empty.
func parseJSONStringArray(raw string) []string {
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
