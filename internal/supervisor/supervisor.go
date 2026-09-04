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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sjkim/jarvis/internal/artifact"
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

	plan, err := s.buildLaunchSpec(ctx, appName)
	if err != nil {
		return runner.Handle{}, err
	}

	h, err := s.runner.Start(ctx, plan.spec)
	if err != nil {
		return runner.Handle{}, fmt.Errorf("start %s: %w", appName, err)
	}

	if err := s.insertInstance(ctx, plan, h, 0); err != nil {
		s.log.Error("failed to record instance in DB", "app", appName, "err", err)
	}

	s.startWatcher(appName, plan.appID, h)
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

// PreviewLaunch resolves what a start would run without running it. The UI
// uses it to show the resolved java.exe, the promoted jar and the final
// argument order before an operator commits to a restart.
func (s *Supervisor) PreviewLaunch(ctx context.Context, appName string) (runner.LaunchSpec, error) {
	plan, err := s.buildLaunchSpec(ctx, appName)
	return plan.spec, err
}

// launchPlan is what buildLaunchSpec resolved: the spec to hand the runner,
// plus the row ids that record which artifact and profile produced it.
type launchPlan struct {
	spec       runner.LaunchSpec
	appID      int64
	artifactID sql.NullInt64
	profileID  sql.NullInt64
}

func (s *Supervisor) buildLaunchSpec(ctx context.Context, appName string) (launchPlan, error) {
	var (
		plan        launchPlan
		shutdownURL sql.NullString
		stopTimeout int
		workDirCfg  sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, shutdown_url, stop_timeout_sec, work_dir, active_artifact_id
		FROM apps WHERE name = ?`, appName).
		Scan(&plan.appID, &shutdownURL, &stopTimeout, &workDirCfg, &plan.artifactID)
	if err != nil {
		return plan, fmt.Errorf("app %q not found: %w", appName, err)
	}

	profile, err := s.readActiveProfile(ctx, plan.appID)
	if err != nil {
		return plan, err
	}
	plan.profileID = profile.id

	javaExe, err := s.resolveJavaExe(ctx, profile.jdkID)
	if err != nil {
		return plan, err
	}

	jarRelPath, jarSHA, err := s.promotedArtifact(ctx, appName, plan.artifactID)
	if err != nil {
		return plan, err
	}
	// Verifying here rather than trusting the row means a jar that was
	// replaced or truncated on disk since upload fails with a checksum error
	// instead of booting as if nothing had changed.
	repo := artifact.NewRepository(s.paths.RepoDir())
	if err := repo.Verify(jarRelPath, jarSHA); err != nil {
		return plan, err
	}

	jvmArgs := profile.jvmArgs()
	workDir := s.paths.AppWorkDir(appName)
	if workDirCfg.Valid && workDirCfg.String != "" {
		workDir = workDirCfg.String
	}
	_ = s.paths.EnsureApp(appName)

	plan.spec = runner.LaunchSpec{
		AppName:     appName,
		JavaExe:     javaExe,
		JarPath:     repo.AbsPath(jarRelPath),
		JVMArgs:     jvmArgs,
		ProgramArgs: profile.programArgs,
		Env:         profile.envSlice(),
		WorkDir:     workDir,
		ConsoleLog: filepath.Join(s.paths.AppLogDir(appName),
			fmt.Sprintf("console-%s.log", time.Now().Format("20060102-150405"))),
		ShutdownURL: shutdownURL.String,
		StopGrace:   time.Duration(stopTimeout) * time.Second,
	}
	return plan, nil
}

// activeProfile is the launch configuration of one app at one revision.
type activeProfile struct {
	id                       sql.NullInt64
	jdkID                    sql.NullInt64
	heapMin, heapMax, gcName string
	extraJVMArgs             []string
	programArgs              []string
	env                      map[string]string
}

// jvmArgs assembles the flags in a fixed order: the shorthand fields first,
// then the operator's own arguments. Later flags win in the JVM, so putting
// the free-form list last is what lets an operator override a shorthand
// without having to clear it.
func (p activeProfile) jvmArgs() []string {
	var args []string
	if p.heapMin != "" {
		args = append(args, "-Xms"+p.heapMin)
	}
	if p.heapMax != "" {
		args = append(args, "-Xmx"+p.heapMax)
	}
	if p.gcName != "" {
		args = append(args, "-XX:+Use"+p.gcName+"GC")
	}
	return append(args, p.extraJVMArgs...)
}

// envSlice merges the profile's variables over JARVIS' own environment.
//
// Replacing the environment wholesale would strip PATH and SystemRoot, which
// the JVM needs to load its own DLLs, so the profile can only add and
// override.
func (p activeProfile) envSlice() []string {
	if len(p.env) == 0 {
		return nil
	}

	merged := os.Environ()
	for k, v := range p.env {
		prefix := strings.ToUpper(k) + "="
		replaced := false
		for i, existing := range merged {
			if strings.HasPrefix(strings.ToUpper(existing), prefix) {
				merged[i] = k + "=" + v
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, k+"="+v)
		}
	}
	return merged
}

func (s *Supervisor) readActiveProfile(ctx context.Context, appID int64) (activeProfile, error) {
	var (
		p                                 activeProfile
		heapMin, heapMax, gc              sql.NullString
		jvmArgsJSON, progArgsJSON, envRaw string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, jdk_id, heap_min, heap_max, gc, jvm_args, program_args, env
		FROM jvm_profiles WHERE app_id = ? AND active = 1
		ORDER BY revision DESC LIMIT 1`, appID).
		Scan(&p.id, &p.jdkID, &heapMin, &heapMax, &gc, &jvmArgsJSON, &progArgsJSON, &envRaw)
	if errors.Is(err, sql.ErrNoRows) {
		// An app with no profile runs on defaults; that is a valid, if
		// untuned, configuration.
		return activeProfile{}, nil
	}
	if err != nil {
		return activeProfile{}, fmt.Errorf("read jvm profile: %w", err)
	}

	p.heapMin, p.heapMax, p.gcName = heapMin.String, heapMax.String, gc.String
	p.extraJVMArgs = parseJSONStringArray(jvmArgsJSON)
	p.programArgs = parseJSONStringArray(progArgsJSON)
	p.env = parseJSONStringMap(envRaw)
	return p, nil
}

// resolveJavaExe picks the JDK for this launch: the profile's choice, else the
// registered default, else whatever `java` is on PATH.
func (s *Supervisor) resolveJavaExe(ctx context.Context, jdkID sql.NullInt64) (string, error) {
	javaExe := "java"
	query := `SELECT java_exe FROM jdks WHERE is_default = 1 LIMIT 1`
	args := []any{}
	if jdkID.Valid {
		query, args = `SELECT java_exe FROM jdks WHERE id = ?`, []any{jdkID.Int64}
	}
	var configured string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&configured); err == nil && configured != "" {
		javaExe = configured
	}

	resolved, err := jdk.Resolve(ctx, javaExe)
	if err != nil {
		return "", fmt.Errorf("resolve java (%s): %w", javaExe, err)
	}
	if resolved.Redirected(javaExe) {
		s.log.Info("java launcher redirected to its own installation",
			"requested", javaExe, "using", resolved.JavaExe)
	}
	return resolved.JavaExe, nil
}

// promotedArtifact returns the jar an app should run.
//
// Falling back to the newest upload keeps apps created before promotion
// existed startable, but it is logged because it means nobody has chosen a
// version and the next upload will change what runs.
func (s *Supervisor) promotedArtifact(ctx context.Context, appName string, artifactID sql.NullInt64) (relPath, sha256 string, err error) {
	if artifactID.Valid {
		err = s.db.QueryRowContext(ctx,
			`SELECT rel_path, sha256 FROM artifacts WHERE id = ?`, artifactID.Int64).
			Scan(&relPath, &sha256)
		if err == nil {
			return relPath, sha256, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
		s.log.Warn("promoted artifact is missing; falling back to the newest upload",
			"app", appName, "artifactId", artifactID.Int64)
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT rel_path, sha256 FROM artifacts
		WHERE app_id = (SELECT id FROM apps WHERE name = ?)
		ORDER BY uploaded_at DESC LIMIT 1`, appName).Scan(&relPath, &sha256)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("app %q has no uploaded jar yet", appName)
	}
	if err != nil {
		return "", "", err
	}
	s.log.Warn("no version promoted; using the newest upload", "app", appName, "jar", relPath)
	return relPath, sha256, nil
}

// insertInstance records the launch, including which artifact and profile
// produced it. Without those ids a running process could not be traced back to
// the version and settings it was started with, which is exactly what an
// operator needs to know when deciding whether a restart is safe.
func (s *Supervisor) insertInstance(ctx context.Context, plan launchPlan, h runner.Handle, restartCount int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO instances (app_id, state, instance_uuid, pid, process_create_time,
			artifact_id, profile_id, command_line, console_log, started_at,
			restart_count, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), ?, datetime('now'))
		ON CONFLICT(app_id) DO UPDATE SET
			state = excluded.state,
			instance_uuid = excluded.instance_uuid,
			pid = excluded.pid,
			process_create_time = excluded.process_create_time,
			artifact_id = excluded.artifact_id,
			profile_id = excluded.profile_id,
			command_line = excluded.command_line,
			console_log = excluded.console_log,
			started_at = excluded.started_at,
			stopped_at = NULL,
			exit_code = NULL,
			restart_count = excluded.restart_count,
			last_error = NULL,
			updated_at = datetime('now')`,
		plan.appID, string(runner.StateRunning), h.InstanceID, h.PID, h.CreateTime,
		plan.artifactID, plan.profileID, h.CommandLine, h.ConsoleLog, restartCount)
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

	for {
		if !s.waitUntilGone(ctx, h) {
			return
		}

		s.log.Warn("process exited", "app", appName, "pid", h.PID)
		s.recordEvent(context.Background(), appID, "exit", "warn",
			fmt.Sprintf("process %d exited", h.PID))

		policy, err := s.readPolicy(context.Background(), appID)
		if err != nil || !policy.watchdog {
			s.markState(context.Background(), appID, runner.StateStopped, "")
			return
		}

		count := s.currentRestartCount(context.Background(), appID)
		if policy.maxRestarts > 0 && count >= policy.maxRestarts {
			msg := fmt.Sprintf("watchdog gave up after %d restart(s)", count)
			s.log.Error(msg, "app", appName)
			s.markState(context.Background(), appID, runner.StateFailed, msg)
			s.recordEvent(context.Background(), appID, "watchdog", "error", msg)
			return
		}

		backoff := watchdogBackoff(count)
		s.markState(context.Background(), appID, runner.StateStarting,
			fmt.Sprintf("watchdog restart in %s", backoff))
		s.log.Info("watchdog waiting to relaunch",
			"app", appName, "attempt", count+1, "backoff", backoff)

		select {
		case <-ctx.Done():
			s.markState(context.Background(), appID, runner.StateStopped, "")
			return
		case <-time.After(backoff):
		}

		next, err := s.relaunch(ctx, appName, count+1)
		if err != nil {
			s.log.Error("watchdog relaunch failed", "app", appName, "err", err)
			s.markState(context.Background(), appID, runner.StateFailed, err.Error())
			s.recordEvent(context.Background(), appID, "watchdog", "error", err.Error())
			return
		}
		s.recordEvent(context.Background(), appID, "watchdog", "info",
			fmt.Sprintf("relaunched as pid %d (attempt %d)", next.PID, count+1))
		h = next
	}
}

// waitUntilGone returns false when the watcher was cancelled (an operator
// stop, or JARVIS itself shutting down). Those must not be treated as crashes
// or the watchdog would undo the stop.
func (s *Supervisor) waitUntilGone(ctx context.Context, h runner.Handle) bool {
	for {
		gone, err := s.runner.WaitExit(ctx, h, 5*time.Second)
		if err != nil {
			return ctx.Err() == nil
		}
		if gone {
			return ctx.Err() == nil
		}
	}
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

func parseJSONStringMap(raw string) map[string]string {
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var out map[string]string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
