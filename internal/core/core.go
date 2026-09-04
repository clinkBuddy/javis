// Package core wires the long-lived components together. Both `jarvis run` and
// `jarvis service` drive the same Core so that debugging in a console matches
// what the service actually does.
package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/sjkim/jarvis/internal/api"
	"github.com/sjkim/jarvis/internal/artifact"
	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/buildinfo"
	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/logging"
	"github.com/sjkim/jarvis/internal/metrics"
	"github.com/sjkim/jarvis/internal/runner"
	"github.com/sjkim/jarvis/internal/store"
	"github.com/sjkim/jarvis/internal/supervisor"
)

// shutdownGrace bounds how long we wait for in-flight HTTP requests. It has no
// bearing on managed java processes: those are deliberately detached and keep
// running when the core stops.
const shutdownGrace = 10 * time.Second

type Core struct {
	Cfg        *config.Config
	Paths      config.Paths
	Log        *slog.Logger
	DB         *store.DB
	Auth       *auth.Service
	Supervisor *supervisor.Supervisor
	Metrics    *metrics.Collector
	API        *api.Server

	started time.Time
	closers []io.Closer

	// stopHousekeeping ends the background maintenance loop.
	stopHousekeeping context.CancelFunc
}

// Bootstrap prepares every component but does not start serving. console
// controls whether logs are mirrored to stderr.
func Bootstrap(ctx context.Context, root string, console bool) (*Core, error) {
	cfg, paths, err := config.Load(root)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	log, logCloser, err := logging.Setup(paths.LogDir(), cfg.Log, console)
	if err != nil {
		return nil, fmt.Errorf("setup logging: %w", err)
	}

	c := &Core{
		Cfg:     cfg,
		Paths:   paths,
		Log:     log,
		started: time.Now(),
		closers: []io.Closer{logCloser},
	}

	log.Info("starting", "build", buildinfo.String(), "dataRoot", paths.Root)

	db, err := store.Open(ctx, paths.DBFile(), log.With("component", "store"))
	if err != nil {
		c.closeAll()
		return nil, err
	}
	c.DB = db
	c.closers = append([]io.Closer{db}, c.closers...)

	if err := db.Migrate(ctx); err != nil {
		c.closeAll()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// An upload that was interrupted by a crash or a service restart leaves a
	// partial file in the staging directory that nothing else will ever claim.
	// The age cutoff keeps this from touching an upload in flight during a
	// concurrent restart.
	if err := artifact.NewRepository(paths.RepoDir()).CleanStaging(6 * time.Hour); err != nil {
		log.Warn("could not clean the upload staging directory", "err", err)
	}

	authService := auth.NewService(db, log.With("component", "auth"))
	if err := authService.Bootstrap(ctx, paths.Root); err != nil {
		c.closeAll()
		return nil, err
	}
	c.Auth = authService

	localRunner := runner.NewLocalRunner(log.With("component", "runner"))
	sup := supervisor.New(db, paths, localRunner, log.With("component", "supervisor"))
	c.Supervisor = sup

	collector := metrics.NewCollector(db, paths.Root, log.With("component", "metrics"))
	c.Metrics = collector

	c.API = api.New(api.Deps{
		Log:        log.With("component", "api"),
		Cfg:        cfg,
		Paths:      paths,
		DB:         db,
		Auth:       authService,
		Supervisor: sup,
		Metrics:    collector,
		Started:    c.started,
	})
	return c, nil
}

// Start begins serving. It returns as soon as the listener is bound.
func (c *Core) Start(ctx context.Context) error {
	// Reconcile managed processes with reality before accepting API calls.
	if err := c.Supervisor.Reconcile(ctx); err != nil {
		c.Log.Error("reconciliation had errors", "err", err)
		// Non-fatal: better to start degraded than not at all.
	}

	if err := c.API.Start(ctx); err != nil {
		return err
	}

	hkCtx, cancel := context.WithCancel(context.Background())
	c.stopHousekeeping = cancel
	go c.housekeeping(hkCtx)
	go c.Metrics.Run(hkCtx)

	// Autostart after the listener is up so an operator can watch the UI as
	// apps come online, and after reconcile so a surviving process is not
	// launched a second time.
	go c.Supervisor.Autostart(hkCtx)
	return nil
}

// housekeepingInterval is how often expired sessions and stale throttle rows
// are pruned. Neither table is read often enough for the rows to be cleaned up
// as a side effect of normal use.
const housekeepingInterval = 30 * time.Minute

func (c *Core) housekeeping(ctx context.Context) {
	ticker := time.NewTicker(housekeepingInterval)
	defer ticker.Stop()

	c.Auth.Cleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Auth.Cleanup(ctx)
		}
	}
}

// Run starts the core and blocks until ctx is cancelled or a component fails.
func (c *Core) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.Log.Info("shutdown requested")
		return nil
	case err := <-c.API.Err():
		if err != nil {
			return fmt.Errorf("admin interface failed: %w", err)
		}
		return nil
	}
}

// Shutdown stops accepting requests and releases resources. Managed java
// processes are intentionally left alone.
func (c *Core) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	var errs []error
	if c.stopHousekeeping != nil {
		c.stopHousekeeping()
	}
	// Cancel all watchers. Managed java processes are intentionally NOT stopped.
	if c.Supervisor != nil {
		c.Supervisor.Shutdown()
	}

	if c.API != nil {
		if err := c.API.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop admin interface: %w", err))
		}
	}
	if err := c.closeAll(); err != nil {
		errs = append(errs, err)
	}
	if c.Log != nil {
		c.Log.Info("stopped")
	}
	return errors.Join(errs...)
}

func (c *Core) closeAll() error {
	var errs []error
	for _, cl := range c.closers {
		if cl == nil {
			continue
		}
		if err := cl.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	c.closers = nil
	return errors.Join(errs...)
}
