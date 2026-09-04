// Package api exposes the REST surface and serves the embedded admin UI.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/ban"
	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/metrics"
	"github.com/sjkim/jarvis/internal/store"
	"github.com/sjkim/jarvis/internal/supervisor"
	"github.com/sjkim/jarvis/internal/webui"
)

type Deps struct {
	Log        *slog.Logger
	Cfg        *config.Config
	Paths      config.Paths
	DB         *store.DB
	Auth       *auth.Service
	Bans       *ban.Service
	Supervisor *supervisor.Supervisor
	Metrics    *metrics.Collector
	Started    time.Time
}

type Server struct {
	deps Deps
	http *http.Server
	ln   net.Listener
	errc chan error
}

func New(d Deps) *Server {
	s := &Server{deps: d, errc: make(chan error, 1)}
	s.http = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: log tailing and metric streams are long-lived.
	}
	return s
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.capturePeer)
	r.Use(s.dropBanned)
	r.Use(s.detectProbes)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	r.Route("/api/v1", func(r chi.Router) {
		// Reachable without a session. /health is what tells an operator that
		// JARVIS is up at all, so putting it behind the login would make a
		// down instance indistinguishable from a locked-out one.
		r.Group(func(r chi.Router) {
			r.Use(s.requireOrigin)
			r.Get("/health", s.handleHealth)
			r.Get("/version", s.handleVersion)
			s.registerAuthRoutes(r)
		})

		// Everything else requires a session. CSRF runs before authentication
		// so a forged request is rejected on its own merits rather than
		// depending on whether the session happened to be valid.
		r.Group(func(r chi.Router) {
			r.Use(s.requireCSRF)
			r.Use(s.requireAuth)
			r.Use(s.audit)

			s.registerSessionRoutes(r)
			s.registerAppRoutes(r)
			s.registerArtifactRoutes(r)
			s.registerProfileRoutes(r)
			s.registerJDKRoutes(r)
			s.registerMetricRoutes(r)
			s.registerLogRoutes(r)
			s.registerBanRoutes(r)
		})
	})

	r.Handle("/*", webui.Handler())
	return r
}

// Start binds the listener and serves in the background. Binding synchronously
// means a port conflict surfaces as a startup failure instead of a silent
// service that never answers.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.deps.Cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", s.deps.Cfg.Server.Addr, err)
	}
	s.ln = ln

	tls := s.deps.Cfg.Server.TLS
	scheme := "http"
	if tls.Enabled {
		scheme = "https"
		if tls.CertFile == "" || tls.KeyFile == "" {
			_ = ln.Close()
			return errors.New("server.tls.enabled is set but certFile/keyFile are empty")
		}
	}
	s.deps.Log.Info("admin interface listening",
		"url", fmt.Sprintf("%s://%s/", scheme, ln.Addr().String()))

	go func() {
		var err error
		if tls.Enabled {
			err = s.http.ServeTLS(ln, tls.CertFile, tls.KeyFile)
		} else {
			err = s.http.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errc <- err
			return
		}
		s.errc <- nil
	}()
	return nil
}

// Err reports an unexpected serve failure so the core can shut down instead of
// lingering with a dead listener.
func (s *Server) Err() <-chan error { return s.errc }

func (s *Server) Addr() string {
	if s.ln == nil {
		return s.deps.Cfg.Server.Addr
	}
	return s.ln.Addr().String()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		level := slog.LevelDebug
		if ww.Status() >= 500 {
			level = slog.LevelError
		} else if ww.Status() >= 400 {
			level = slog.LevelWarn
		}
		s.deps.Log.Log(r.Context(), level, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"dur", time.Since(start).Round(time.Millisecond).String(),
			"ip", r.RemoteAddr,
			"rid", middleware.GetReqID(r.Context()),
		)
	})
}
