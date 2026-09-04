package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sjkim/jarvis/internal/auth"
	"github.com/sjkim/jarvis/internal/metrics"
)

func (s *Server) registerMetricRoutes(r chi.Router) {
	r.Get("/host", s.requireRole(auth.RoleViewer, s.handleHostNow))
	r.Get("/host/metrics", s.requireRole(auth.RoleViewer, s.handleHostHistory))
	r.Get("/metrics/overview", s.requireRole(auth.RoleViewer, s.handleMetricsOverview))
	r.Get("/apps/{name}/metrics", s.requireRole(auth.RoleViewer, s.handleAppMetrics))
	r.Get("/apps/{name}/traffic", s.requireRole(auth.RoleViewer, s.handleAppTraffic))
}

func (s *Server) handleHostNow(w http.ResponseWriter, r *http.Request) {
	if s.deps.Metrics == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoMetrics)
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Metrics.LatestHost())
}

func (s *Server) handleHostHistory(w http.ResponseWriter, r *http.Request) {
	if s.deps.Metrics == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoMetrics)
		return
	}
	since := parseSince(r, time.Hour)
	hist, err := s.deps.Metrics.HostHistory(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

func (s *Server) handleMetricsOverview(w http.ResponseWriter, r *http.Request) {
	if s.deps.Metrics == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoMetrics)
		return
	}
	since := parseSince(r, time.Hour)
	hostHist, err := s.deps.Metrics.HostHistory(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	appHist, err := s.deps.Metrics.HistoryAll(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host": map[string]any{
			"latest":  s.deps.Metrics.LatestHost(),
			"history": hostHist,
		},
		"apps": map[string]any{
			"latest":  s.deps.Metrics.LatestAll(),
			"history": appHist,
		},
		"traffic": s.deps.Metrics.LatestTrafficAll(),
	})
}

func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request) {
	if s.deps.Metrics == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoMetrics)
		return
	}
	appID, err := s.appIDByName(r, chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	since := parseSince(r, time.Hour)
	hist, err := s.deps.Metrics.History(r.Context(), appID, since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	latest, _ := s.deps.Metrics.LatestProcess(appID)
	writeJSON(w, http.StatusOK, map[string]any{
		"latest":  latest,
		"history": hist,
	})
}

func (s *Server) handleAppTraffic(w http.ResponseWriter, r *http.Request) {
	if s.deps.Metrics == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoMetrics)
		return
	}
	appID, err := s.appIDByName(r, chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	snap, ok := s.deps.Metrics.LatestTraffic(appID)
	if !ok {
		writeJSON(w, http.StatusOK, metrics.TrafficSnapshot{AppID: appID})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func parseSince(r *http.Request, fallback time.Duration) time.Time {
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*60 {
			return time.Now().Add(-time.Duration(n) * time.Minute)
		}
	}
	return time.Now().Add(-fallback)
}

var errNoMetrics = errString("metrics collector is not running")

type errString string

func (e errString) Error() string { return string(e) }
