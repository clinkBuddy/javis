package metrics

import "time"

// ProcessReading is one sample of a managed process.
type ProcessReading struct {
	AppID        int64     `json:"appId"`
	AppName      string    `json:"appName"`
	TS           int64     `json:"ts"`
	CPUPercent   float64   `json:"cpuPercent"`
	RSSBytes     int64     `json:"rssBytes"`
	PrivateBytes int64     `json:"privateBytes"`
	Threads      int       `json:"threads"`
	Handles      int       `json:"handles"`
	PID          uint32    `json:"pid,omitempty"`
	At           time.Time `json:"-"`
}

// HostReading is one sample of the machine JARVIS is running on.
type HostReading struct {
	TS         int64   `json:"ts"`
	CPUPercent float64 `json:"cpuPercent"`
	MemTotal   int64   `json:"memTotal"`
	MemUsed    int64   `json:"memUsed"`
	SwapUsed   int64   `json:"swapUsed"`
	DiskTotal  int64   `json:"diskTotal"`
	DiskFree   int64   `json:"diskFree"`
	LoadProcs  int     `json:"loadProcs"`
}
