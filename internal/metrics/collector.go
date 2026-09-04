// Package metrics samples managed processes and the host, and keeps a short
// history in SQLite so the UI can draw a chart without scraping the OS on
// every poll.
package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/sjkim/jarvis/internal/store"
)

const (
	sampleInterval = 5 * time.Second
	rawRetention   = 2 * time.Hour
	min1Retention  = 24 * time.Hour
)

// Collector ticks in the background. Latest() is safe to call from HTTP
// handlers; the database history is for the chart that looks further back.
type Collector struct {
	db       *store.DB
	diskPath string
	log      *slog.Logger

	mu         sync.Mutex
	prevProc   map[int64]rawProc
	prevProcAt map[int64]time.Time
	prevHost   rawHost
	prevHostAt time.Time
	latest     map[int64]ProcessReading
	latestHost HostReading
	traffic    map[int64]*appTraffic
}

func NewCollector(db *store.DB, diskPath string, log *slog.Logger) *Collector {
	return &Collector{
		db:         db,
		diskPath:   diskPath,
		log:        log,
		prevProc:   map[int64]rawProc{},
		prevProcAt: map[int64]time.Time{},
		latest:     map[int64]ProcessReading{},
		traffic:    map[int64]*appTraffic{},
	}
}

// Run samples until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.tick(ctx)
	t := time.NewTicker(sampleInterval)
	defer t.Stop()
	var n int
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.tick(ctx)
			n++
			// Roll up and prune on a slower cadence so a sample never waits
			// on a delete of thousands of old rows.
			if n%12 == 0 {
				c.maintain(ctx)
			}
		}
	}
}

func (c *Collector) tick(ctx context.Context) {
	now := time.Now()
	ts := now.Unix()

	c.sampleHost(now, ts)
	c.sampleApps(ctx, now, ts)
	c.sampleTraffic(ctx)
}

type liveRow struct {
	id         int64
	name       string
	pid        uint32
	createTime int64
}

func (c *Collector) sampleApps(ctx context.Context, now time.Time, ts int64) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT a.id, a.name, i.pid, i.process_create_time
		FROM apps a JOIN instances i ON i.app_id = a.id
		WHERE i.state IN ('RUNNING','STARTING') AND i.pid IS NOT NULL AND i.pid > 0`)
	if err != nil {
		c.log.Warn("could not list running apps for sampling", "err", err)
		return
	}
	defer rows.Close()

	var live []liveRow
	seen := map[int64]bool{}
	for rows.Next() {
		var r liveRow
		if err := rows.Scan(&r.id, &r.name, &r.pid, &r.createTime); err != nil {
			continue
		}
		live = append(live, r)
		seen[r.id] = true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for id := range c.latest {
		if !seen[id] {
			delete(c.latest, id)
			delete(c.prevProc, id)
			delete(c.prevProcAt, id)
		}
	}

	for _, r := range live {
		raw, err := sampleProcess(r.pid, r.createTime)
		if err != nil {
			continue
		}

		reading := ProcessReading{
			AppID: r.id, AppName: r.name, TS: ts, At: now, PID: r.pid,
			RSSBytes: raw.rss, PrivateBytes: raw.priv,
			Threads: raw.threads, Handles: raw.handles,
		}
		if prev, ok := c.prevProc[r.id]; ok {
			reading.CPUPercent = cpuPercent(prev.cpu, raw.cpu, now.Sub(c.prevProcAt[r.id]))
		}
		c.prevProc[r.id] = raw
		c.prevProcAt[r.id] = now
		c.latest[r.id] = reading

		if _, err := c.db.ExecContext(ctx, `
			INSERT OR REPLACE INTO metric_samples
				(app_id, resolution, ts, cpu_percent, rss_bytes, private_bytes, threads, handles)
			VALUES (?, 'raw', ?, ?, ?, ?, ?, ?)`,
			r.id, ts, reading.CPUPercent, reading.RSSBytes, reading.PrivateBytes,
			reading.Threads, reading.Handles); err != nil {
			c.log.Debug("could not persist a process sample", "app", r.name, "err", err)
		}
	}
}

func (c *Collector) sampleHost(now time.Time, ts int64) {
	raw, err := sampleHost(c.diskPath)
	if err != nil {
		c.log.Debug("could not sample the host", "err", err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	reading := HostReading{
		TS: ts, MemTotal: raw.memTotal, MemUsed: raw.memUsed, SwapUsed: raw.swapUsed,
		DiskTotal: raw.diskTotal, DiskFree: raw.diskFree, LoadProcs: raw.procs,
	}
	if !c.prevHostAt.IsZero() {
		reading.CPUPercent = hostCPUPercent(
			c.prevHost.idle, c.prevHost.kernel, c.prevHost.user,
			raw.idle, raw.kernel, raw.user)
	}
	c.prevHost = raw
	c.prevHostAt = now
	c.latestHost = reading

	if _, err := c.db.ExecContext(context.Background(), `
		INSERT OR REPLACE INTO host_samples
			(resolution, ts, cpu_percent, mem_total, mem_used, swap_used, disk_total, disk_free, load_procs)
		VALUES ('raw', ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, reading.CPUPercent, reading.MemTotal, reading.MemUsed, reading.SwapUsed,
		reading.DiskTotal, reading.DiskFree, reading.LoadProcs); err != nil {
		c.log.Debug("could not persist a host sample", "err", err)
	}
}

// LatestProcess returns the most recent in-memory sample, if any.
func (c *Collector) LatestProcess(appID int64) (ProcessReading, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.latest[appID]
	return r, ok
}

func (c *Collector) LatestAll() map[int64]ProcessReading {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int64]ProcessReading, len(c.latest))
	for k, v := range c.latest {
		out[k] = v
	}
	return out
}

func (c *Collector) LatestHost() HostReading {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestHost
}

// History returns stored samples for one app.
func (c *Collector) History(ctx context.Context, appID int64, since time.Time) ([]ProcessReading, error) {
	res := "raw"
	if time.Since(since) > 2*time.Hour {
		res = "1m"
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT ts, COALESCE(cpu_percent,0), COALESCE(rss_bytes,0), COALESCE(private_bytes,0),
			   COALESCE(threads,0), COALESCE(handles,0)
		FROM metric_samples
		WHERE app_id = ? AND resolution = ? AND ts >= ?
		ORDER BY ts`, appID, res, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProcessReading{}
	for rows.Next() {
		var r ProcessReading
		if err := rows.Scan(&r.TS, &r.CPUPercent, &r.RSSBytes, &r.PrivateBytes,
			&r.Threads, &r.Handles); err != nil {
			return nil, err
		}
		r.AppID = appID
		out = append(out, r)
	}
	return out, rows.Err()
}

// HistoryAll returns stored samples for every app in one query so the
// dashboard does not fan out per process.
func (c *Collector) HistoryAll(ctx context.Context, since time.Time) (map[int64][]ProcessReading, error) {
	res := "raw"
	if time.Since(since) > 2*time.Hour {
		res = "1m"
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT app_id, ts, COALESCE(cpu_percent,0), COALESCE(rss_bytes,0), COALESCE(private_bytes,0),
			   COALESCE(threads,0), COALESCE(handles,0)
		FROM metric_samples
		WHERE resolution = ? AND ts >= ?
		ORDER BY app_id, ts`, res, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]ProcessReading{}
	for rows.Next() {
		var r ProcessReading
		if err := rows.Scan(&r.AppID, &r.TS, &r.CPUPercent, &r.RSSBytes, &r.PrivateBytes,
			&r.Threads, &r.Handles); err != nil {
			return nil, err
		}
		out[r.AppID] = append(out[r.AppID], r)
	}
	return out, rows.Err()
}

// HostHistory returns stored host samples.
func (c *Collector) HostHistory(ctx context.Context, since time.Time) ([]HostReading, error) {
	res := "raw"
	if time.Since(since) > 2*time.Hour {
		res = "1m"
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT ts, COALESCE(cpu_percent,0), COALESCE(mem_total,0), COALESCE(mem_used,0),
			   COALESCE(swap_used,0), COALESCE(disk_total,0), COALESCE(disk_free,0),
			   COALESCE(load_procs,0)
		FROM host_samples
		WHERE resolution = ? AND ts >= ?
		ORDER BY ts`, res, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []HostReading{}
	for rows.Next() {
		var r HostReading
		if err := rows.Scan(&r.TS, &r.CPUPercent, &r.MemTotal, &r.MemUsed,
			&r.SwapUsed, &r.DiskTotal, &r.DiskFree, &r.LoadProcs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *Collector) maintain(ctx context.Context) {
	now := time.Now().Unix()
	c.rollupProcess(ctx, now)
	c.rollupHost(ctx, now)

	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM metric_samples WHERE resolution = 'raw' AND ts < ?`,
		now-int64(rawRetention.Seconds())); err != nil {
		c.log.Debug("prune raw process samples", "err", err)
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM metric_samples WHERE resolution = '1m' AND ts < ?`,
		now-int64(min1Retention.Seconds())); err != nil {
		c.log.Debug("prune 1m process samples", "err", err)
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM host_samples WHERE resolution = 'raw' AND ts < ?`,
		now-int64(rawRetention.Seconds())); err != nil {
		c.log.Debug("prune raw host samples", "err", err)
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM host_samples WHERE resolution = '1m' AND ts < ?`,
		now-int64(min1Retention.Seconds())); err != nil {
		c.log.Debug("prune 1m host samples", "err", err)
	}
}

// rollupProcess averages each minute of raw samples. The bucket timestamp is
// the minute's start, so a chart can join process and host series on ts.
func (c *Collector) rollupProcess(ctx context.Context, now int64) {
	_, err := c.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO metric_samples
			(app_id, resolution, ts, cpu_percent, rss_bytes, private_bytes, threads, handles)
		SELECT app_id, '1m', (ts / 60) * 60,
			   AVG(cpu_percent), AVG(rss_bytes), AVG(private_bytes),
			   AVG(threads), AVG(handles)
		FROM metric_samples
		WHERE resolution = 'raw' AND ts >= ?
		GROUP BY app_id, ts / 60`, now-120)
	if err != nil && err != sql.ErrNoRows {
		c.log.Debug("rollup process samples", "err", err)
	}
}

func (c *Collector) rollupHost(ctx context.Context, now int64) {
	_, err := c.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO host_samples
			(resolution, ts, cpu_percent, mem_total, mem_used, swap_used, disk_total, disk_free, load_procs)
		SELECT '1m', (ts / 60) * 60,
			   AVG(cpu_percent), AVG(mem_total), AVG(mem_used), AVG(swap_used),
			   AVG(disk_total), AVG(disk_free), AVG(load_procs)
		FROM host_samples
		WHERE resolution = 'raw' AND ts >= ?
		GROUP BY ts / 60`, now-120)
	if err != nil && err != sql.ErrNoRows {
		c.log.Debug("rollup host samples", "err", err)
	}
}
