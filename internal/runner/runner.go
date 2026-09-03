// Package runner defines the process lifecycle contract. LocalWindowsRunner
// implements it for java processes on the host machine; RemoteLinuxRunner
// (planned for P6) will implement it over SSH.
package runner

import (
	"context"
	"time"
)

// State is the supervisor's view of a managed process.
type State string

const (
	StateStopped  State = "STOPPED"
	StateStarting State = "STARTING"
	StateRunning  State = "RUNNING"
	StateStopping State = "STOPPING"
	StateFailed   State = "FAILED"
	StateOrphan   State = "ORPHAN" // found by marker but not in the DB
)

// Handle is the identity of one running (or recently-run) process. The
// concrete Runner stores whatever it needs in here; everything else works
// through the interface.
type Handle struct {
	AppName    string
	InstanceID string

	PID        uint32
	CreateTime int64 // Windows FILETIME (100 ns ticks since 1601-01-01)

	State     State
	StartedAt time.Time
	StoppedAt time.Time
	ExitCode  int

	ArtifactPath string
	CommandLine  string
	ConsoleLog   string
}

// LaunchSpec carries everything the runner needs to start a process.
type LaunchSpec struct {
	AppName string

	JavaExe     string // resolved, never the Oracle stub
	JarPath     string
	JVMArgs     []string
	ProgramArgs []string
	Env         []string // KEY=VALUE; nil inherits parent

	WorkDir    string
	ConsoleLog string // path for redirected stdout/stderr

	ShutdownURL     string
	ShutdownHeaders map[string]string
	StopGrace       time.Duration
}

// StopResult tells the caller how the process was stopped.
type StopResult struct {
	Method   string // "shutdown-endpoint", "ctrl-c", "terminate"
	Duration time.Duration
}

// Runner is the abstraction that separates the supervisor from the OS.
type Runner interface {
	// Start launches a new process and returns its handle. The process is
	// fully detached: it will survive even if this Go process crashes.
	Start(ctx context.Context, spec LaunchSpec) (Handle, error)

	// Stop shuts the process down using the three-tier escalation.
	Stop(ctx context.Context, h Handle) (StopResult, error)

	// Alive reports whether the identified process is still running.
	Alive(h Handle) (bool, error)

	// WaitExit blocks until the process exits or the context is cancelled.
	WaitExit(ctx context.Context, h Handle, timeout time.Duration) (bool, error)

	// Discover scans the OS for processes belonging to appName. This is the
	// adoption path: after a JARVIS restart, each DB record is checked against
	// live processes and orphans are surfaced.
	Discover(ctx context.Context, appName string) ([]Handle, error)

	// DiscoverAll returns every process whose command line contains a JARVIS
	// app marker, regardless of which app. Used on startup to find orphans.
	DiscoverAll(ctx context.Context) ([]Handle, error)
}
