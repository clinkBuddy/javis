package metrics

import "time"

type rawProc struct {
	cpu              time.Duration
	rss, priv        int64
	threads, handles int
}

type rawHost struct {
	idle, kernel, user          time.Duration
	memTotal, memUsed, swapUsed int64
	diskTotal, diskFree         int64
	procs                       int
}
