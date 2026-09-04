package metrics

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sjkim/jarvis/internal/applog"
)

const (
	trafficKeepExchanges = 80
	trafficKeepPoints    = 60
	actuatorCoolDown     = 2 * time.Minute
)

// HTTPExchange is one completed request as seen from the app.
type HTTPExchange struct {
	At     time.Time `json:"at"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Status int       `json:"status"`
	Ms     float64   `json:"ms"`
	Remote string    `json:"remote,omitempty"`
}

// TrafficPoint is one sample of request rate / concurrency.
type TrafficPoint struct {
	TS     int64   `json:"ts"`
	TPS    float64 `json:"tps"`
	Active int     `json:"active"`
	AvgMs  float64 `json:"avgMs"`
	Errors int     `json:"errors"`
}

// TrafficSnapshot is the live APM view for one managed app.
type TrafficSnapshot struct {
	AppID     int64          `json:"appId"`
	AppName   string         `json:"appName"`
	Source    string         `json:"source"`
	Active    int            `json:"active"`
	Listen    []int          `json:"listen,omitempty"`
	TPS       float64        `json:"tps"`
	ErrorRate float64        `json:"errorRate"`
	AvgMs     float64        `json:"avgMs"`
	MaxMs     float64        `json:"maxMs"`
	History   []TrafficPoint `json:"history"`
	Recent    []HTTPExchange `json:"recent"`
}

type appTraffic struct {
	mu           sync.Mutex
	recent       []HTTPExchange
	history      []TrafficPoint
	logOff       int64
	logReady     bool
	actSkipUntil time.Time
	actKind      string
	prevAt       time.Time
	last         TrafficSnapshot
}

type tcpSnap struct {
	listen []int
	active int
	peers  []string
}

type trafficRow struct {
	id          int64
	name        string
	pid         uint32
	shutdownURL string
	consoleLog  string
}

func (c *Collector) sampleTraffic(ctx context.Context) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT a.id, a.name, COALESCE(i.pid, 0), COALESCE(a.shutdown_url, ''),
			   COALESCE(i.console_log, '')
		FROM apps a JOIN instances i ON i.app_id = a.id
		WHERE i.state IN ('RUNNING','STARTING') AND i.pid IS NOT NULL AND i.pid > 0`)
	if err != nil {
		c.log.Debug("could not list apps for traffic sampling", "err", err)
		return
	}
	defer rows.Close()

	var live []trafficRow
	seen := map[int64]bool{}
	for rows.Next() {
		var r trafficRow
		if err := rows.Scan(&r.id, &r.name, &r.pid, &r.shutdownURL, &r.consoleLog); err != nil {
			continue
		}
		live = append(live, r)
		seen[r.id] = true
	}

	c.mu.Lock()
	if c.traffic == nil {
		c.traffic = map[int64]*appTraffic{}
	}
	for id := range c.traffic {
		if !seen[id] {
			delete(c.traffic, id)
		}
	}
	c.mu.Unlock()

	now := time.Now()
	for _, r := range live {
		c.sampleOneTraffic(ctx, r, now)
	}
}

func (c *Collector) sampleOneTraffic(ctx context.Context, r trafficRow, now time.Time) {
	c.mu.Lock()
	st := c.traffic[r.id]
	if st == nil {
		st = &appTraffic{}
		c.traffic[r.id] = st
	}
	c.mu.Unlock()

	tcp := sampleTCP(r.pid)
	incoming := c.collectExchanges(ctx, st, r, tcp, now)

	st.mu.Lock()
	defer st.mu.Unlock()
	st.recent = mergeExchanges(st.recent, incoming, trafficKeepExchanges)

	var sumMs float64
	var errs, withMs int
	window := now.Add(-60 * time.Second)
	for _, e := range st.recent {
		if e.At.Before(window) {
			continue
		}
		if e.Status >= 500 {
			errs++
		}
		if e.Ms > 0 {
			sumMs += e.Ms
			withMs++
		}
	}

	dt := now.Sub(st.prevAt).Seconds()
	if dt <= 0 {
		dt = sampleInterval.Seconds()
	}
	tps := float64(len(incoming)) / dt
	if st.prevAt.IsZero() {
		tps = 0
	}
	st.prevAt = now

	avgMs := 0.0
	if withMs > 0 {
		avgMs = sumMs / float64(withMs)
	}
	maxMs := 0.0
	for _, e := range st.recent {
		if e.Ms > maxMs {
			maxMs = e.Ms
		}
	}

	src := "tcp"
	if st.actKind != "" && st.actKind != "none" {
		src = "actuator"
	} else if len(incoming) > 0 || hasHTTPRecent(st.recent) {
		src = "log"
	}

	inWindow := 0
	for _, e := range st.recent {
		if !e.At.Before(window) {
			inWindow++
		}
	}
	errRate := 0.0
	if inWindow > 0 {
		errRate = float64(errs) / float64(inWindow)
	}

	pt := TrafficPoint{
		TS: now.Unix(), TPS: tps, Active: tcp.active, AvgMs: avgMs, Errors: errs,
	}
	st.history = append(st.history, pt)
	if len(st.history) > trafficKeepPoints {
		st.history = append([]TrafficPoint{}, st.history[len(st.history)-trafficKeepPoints:]...)
	}

	st.last = TrafficSnapshot{
		AppID: r.id, AppName: r.name, Source: src,
		Active: tcp.active, Listen: tcp.listen,
		TPS: tps, ErrorRate: errRate, AvgMs: avgMs, MaxMs: maxMs,
		History: append([]TrafficPoint{}, st.history...),
		Recent:  append([]HTTPExchange{}, st.recent...),
	}
}

func (c *Collector) collectExchanges(ctx context.Context, st *appTraffic, r trafficRow, tcp tcpSnap, now time.Time) []HTTPExchange {
	st.mu.Lock()
	skipAct := !now.After(st.actSkipUntil)
	st.mu.Unlock()

	var out []HTTPExchange
	if !skipAct {
		got, kind, hardFail := pullActuator(ctx, r.shutdownURL, tcp.listen)
		st.mu.Lock()
		if kind != "" {
			st.actKind = kind
		}
		if hardFail {
			st.actSkipUntil = now.Add(actuatorCoolDown)
			st.actKind = "none"
		}
		st.mu.Unlock()
		out = append(out, got...)
	}

	if r.consoleLog != "" {
		out = append(out, readLogExchanges(st, r.consoleLog)...)
	}
	return out
}

func readLogExchanges(st *appTraffic, path string) []HTTPExchange {
	st.mu.Lock()
	ready := st.logReady
	off := st.logOff
	st.mu.Unlock()

	if !ready {
		if _, size, err := applog.TailLines(path, 1); err == nil {
			off = size
		}
		st.mu.Lock()
		st.logOff = off
		st.logReady = true
		st.mu.Unlock()
		lines, _, err := applog.TailLines(path, 200)
		if err != nil {
			return nil
		}
		return parseHTTPLines(lines)
	}
	data, next, err := applog.ReadSince(path, off)
	if err != nil || len(data) == 0 {
		return nil
	}
	st.mu.Lock()
	st.logOff = next
	st.mu.Unlock()
	return parseHTTPLines(splitLogLines(string(data)))
}

func splitLogLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func mergeExchanges(old, incoming []HTTPExchange, keep int) []HTTPExchange {
	if len(incoming) == 0 {
		if len(old) > keep {
			return append([]HTTPExchange{}, old[len(old)-keep:]...)
		}
		return old
	}
	seen := map[string]bool{}
	for _, e := range old {
		seen[exchangeKey(e)] = true
	}
	out := append([]HTTPExchange{}, old...)
	for _, e := range incoming {
		if e.Path == "" || strings.HasPrefix(e.Path, "/actuator") {
			continue
		}
		k := exchangeKey(e)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > keep {
		out = append([]HTTPExchange{}, out[len(out)-keep:]...)
	}
	return out
}

func exchangeKey(e HTTPExchange) string {
	return e.At.Truncate(time.Millisecond).Format(time.RFC3339Nano) + "|" + e.Method + "|" + e.Path + "|" + itoa(e.Status)
}

func hasHTTPRecent(xs []HTTPExchange) bool {
	return len(xs) > 0
}

func (c *Collector) LatestTraffic(appID int64) (TrafficSnapshot, bool) {
	c.mu.Lock()
	st := c.traffic[appID]
	c.mu.Unlock()
	if st == nil {
		return TrafficSnapshot{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.last, true
}

func (c *Collector) LatestTrafficAll() map[int64]TrafficSnapshot {
	c.mu.Lock()
	ids := make([]*appTraffic, 0, len(c.traffic))
	keys := make([]int64, 0, len(c.traffic))
	for id, st := range c.traffic {
		ids = append(ids, st)
		keys = append(keys, id)
	}
	c.mu.Unlock()
	out := make(map[int64]TrafficSnapshot, len(ids))
	for i, st := range ids {
		if st == nil {
			continue
		}
		st.mu.Lock()
		out[keys[i]] = st.last
		st.mu.Unlock()
	}
	return out
}
