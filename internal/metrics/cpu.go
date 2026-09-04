package metrics

import "time"

// cpuPercent turns two successive CPU-time readings into a percentage of one
// core. The collector then divides by the number of cores for a host-wide
// figure; a single process is left as "percent of one core" because that is
// what operators compare against -Xmx and thread counts.
func cpuPercent(prevCPU, nextCPU, elapsed time.Duration) float64 {
	if elapsed <= 0 || nextCPU < prevCPU {
		return 0
	}
	pct := float64(nextCPU-prevCPU) / float64(elapsed) * 100
	if pct < 0 {
		return 0
	}
	// A rounding hiccup can push a fully-busy core slightly over 100. Cap
	// it so the UI never has to explain 101%.
	if pct > 100 {
		return 100
	}
	return pct
}

// hostCPUPercent uses the Windows convention that kernel time includes idle
// time, so busy = (kernel - idle) + user and total = kernel + user.
func hostCPUPercent(prevIdle, prevKernel, prevUser, nextIdle, nextKernel, nextUser time.Duration) float64 {
	dIdle := nextIdle - prevIdle
	dKernel := nextKernel - prevKernel
	dUser := nextUser - prevUser
	total := dKernel + dUser
	if total <= 0 {
		return 0
	}
	busy := dKernel - dIdle + dUser
	if busy < 0 {
		return 0
	}
	pct := float64(busy) / float64(total) * 100
	if pct > 100 {
		return 100
	}
	return pct
}
