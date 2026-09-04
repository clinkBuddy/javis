package supervisor

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sjkim/jarvis/internal/runner"
)

// watchdogBackoff grows exponentially and caps at 30s. A process that crashes
// on startup must not be relaunched in a tight loop — that would pin a core
// and flood the console log — but it also must not wait minutes before the
// first retry, which is when a flaky port bind usually succeeds.
func watchdogBackoff(restartCount int) time.Duration {
	if restartCount < 0 {
		restartCount = 0
	}
	d := time.Second << restartCount
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

type appPolicy struct {
	autostart   bool
	watchdog    bool
	maxRestarts int
	startOrder  int
	startDelay  time.Duration
	dependsOn   []string
	stopTimeout time.Duration
}

func (s *Supervisor) readPolicy(ctx context.Context, appID int64) (appPolicy, error) {
	var (
		p          appPolicy
		delaySec   int
		stopSec    int
		dependsRaw string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT autostart, watchdog, max_restarts, start_order, start_delay_sec,
			   stop_timeout_sec, depends_on
		FROM apps WHERE id = ?`, appID).
		Scan(&p.autostart, &p.watchdog, &p.maxRestarts, &p.startOrder,
			&delaySec, &stopSec, &dependsRaw)
	if err != nil {
		return p, err
	}
	p.startDelay = time.Duration(delaySec) * time.Second
	p.stopTimeout = time.Duration(stopSec) * time.Second
	p.dependsOn = parseJSONStringArray(dependsRaw)
	return p, nil
}

func (s *Supervisor) currentRestartCount(ctx context.Context, appID int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT restart_count FROM instances WHERE app_id = ?`, appID).Scan(&n)
	return n
}

func (s *Supervisor) markState(ctx context.Context, appID int64, state runner.State, lastErr string) {
	_, _ = s.db.ExecContext(ctx, `
		UPDATE instances SET state = ?, stopped_at = datetime('now'),
			last_error = ?, updated_at = datetime('now')
		WHERE app_id = ?`, string(state), nullEmpty(lastErr), appID)
}

func (s *Supervisor) recordEvent(ctx context.Context, appID int64, kind, severity, message string) {
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO events (app_id, kind, severity, message)
		VALUES (?, ?, ?, ?)`, appID, kind, severity, message)
}

func nullEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Autostart brings up every app marked to start with JARVIS, in start_order.
//
// It runs after Reconcile so an app whose process survived a JARVIS restart
// is re-attached rather than launched a second time. A failure of one app
// does not skip the rest: the operator still wants the others up.
func (s *Supervisor) Autostart(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name FROM apps WHERE autostart = 1 ORDER BY start_order, name`)
	if err != nil {
		s.log.Error("could not list autostart apps", "err", err)
		return
	}
	defer rows.Close()

	type item struct {
		id   int64
		name string
	}
	var apps []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.name); err != nil {
			s.log.Error("scan autostart app", "err", err)
			return
		}
		apps = append(apps, it)
	}
	if err := rows.Err(); err != nil {
		s.log.Error("list autostart apps", "err", err)
		return
	}

	for _, app := range apps {
		if s.isWatched(app.name) {
			continue
		}
		h, err := s.GetStatus(ctx, app.name)
		if err == nil && (h.State == runner.StateRunning || h.State == runner.StateStarting) {
			alive, _ := s.runner.Alive(h)
			if alive {
				continue
			}
		}

		policy, err := s.readPolicy(ctx, app.id)
		if err != nil {
			s.log.Error("read autostart policy", "app", app.name, "err", err)
			continue
		}
		if err := s.waitForDeps(ctx, policy.dependsOn, 2*time.Minute); err != nil {
			s.log.Error("autostart dependencies not ready", "app", app.name, "err", err)
			s.recordEvent(ctx, app.id, "autostart", "error", err.Error())
			continue
		}
		if policy.startDelay > 0 {
			s.log.Info("autostart delay", "app", app.name, "delay", policy.startDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(policy.startDelay):
			}
		}

		if _, err := s.StartApp(ctx, app.name); err != nil {
			s.log.Error("autostart failed", "app", app.name, "err", err)
			s.recordEvent(ctx, app.id, "autostart", "error", err.Error())
			continue
		}
		s.log.Info("autostarted", "app", app.name)
		s.recordEvent(ctx, app.id, "autostart", "info", "started with JARVIS")
	}
}

func (s *Supervisor) isWatched(appName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.watches[appName]
	return ok
}

func (s *Supervisor) waitForDeps(ctx context.Context, deps []string, timeout time.Duration) error {
	if len(deps) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for _, name := range deps {
		for {
			h, err := s.GetStatus(ctx, name)
			if err == nil && h.State == runner.StateRunning {
				alive, _ := s.runner.Alive(h)
				if alive {
					break
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("dependency %q was not running within %s", name, timeout)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func (s *Supervisor) relaunch(ctx context.Context, appName string, restartCount int) (runner.Handle, error) {
	plan, err := s.buildLaunchSpec(ctx, appName)
	if err != nil {
		return runner.Handle{}, err
	}
	h, err := s.runner.Start(ctx, plan.spec)
	if err != nil {
		return runner.Handle{}, err
	}
	if err := s.insertInstance(ctx, plan, h, restartCount); err != nil {
		s.log.Error("failed to record a watchdog relaunch", "app", appName, "err", err)
	}
	return h, nil
}
