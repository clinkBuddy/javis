//go:build windows

package metrics

import "github.com/sjkim/jarvis/internal/winproc"

func sampleProcess(pid uint32, createTime int64) (rawProc, error) {
	u, err := winproc.SampleUsage(pid, createTime)
	if err != nil {
		return rawProc{}, err
	}
	return rawProc{
		cpu:     u.CPUTime,
		rss:     u.RSSBytes,
		priv:    u.PrivateBytes,
		threads: u.Threads,
		handles: u.Handles,
	}, nil
}

func sampleHost(diskPath string) (rawHost, error) {
	u, err := winproc.SampleHost(diskPath)
	if err != nil {
		return rawHost{}, err
	}
	return rawHost{
		idle:      u.IdleTime,
		kernel:    u.KernelTime,
		user:      u.UserTime,
		memTotal:  u.MemTotal,
		memUsed:   u.MemUsed,
		swapUsed:  u.SwapUsed,
		diskTotal: u.DiskTotal,
		diskFree:  u.DiskFree,
		procs:     u.Procs,
	}, nil
}
