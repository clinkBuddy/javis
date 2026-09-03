//go:build windows

// Package winproc is the Win32 layer that makes managed java processes
// independent of JARVIS' own lifetime.
//
// Three things have to hold, and each one drives a design decision here.
//
// A managed process must survive JARVIS dying. Windows already does not kill
// children when a parent exits, so the only real hazard is job object
// inheritance: if JARVIS was itself started inside a job that kills its
// processes on close (a terminal, a CI runner, another supervisor), children
// would be dragged down with it. Launch therefore asks for
// CREATE_BREAKAWAY_FROM_JOB and drops the process handle immediately.
//
// A managed process must be stoppable gracefully. Windows has no SIGTERM, and
// TerminateProcess skips JVM shutdown hooks, which loses in-flight requests
// and leaks connections. The portable way to reach a JVM is a console CTRL+C,
// so every child is given its own hidden console purely as a signal channel
// while its output is redirected to a file.
//
// A managed process must be re-identifiable after JARVIS restarts. A PID alone
// is not an identity because Windows recycles them, so a process is pinned by
// the triple (PID, creation FILETIME, instance marker on the command line).
package winproc

import (
	"errors"
	"fmt"
)

// ErrNotRunning means no live process matches the requested identity. It is
// returned in preference to a Win32 error because the caller usually wants to
// treat "already gone" as success.
var ErrNotRunning = errors.New("winproc: process is not running")

// Info identifies a managed process.
//
// CreateTime is the raw process creation FILETIME (100 ns ticks since 1601)
// rather than a converted time.Time. It is only ever compared for equality, so
// keeping the exact kernel value avoids any rounding that could make a
// re-attach check spuriously fail.
type Info struct {
	PID        uint32
	CreateTime int64
}

func (i Info) String() string {
	return fmt.Sprintf("pid=%d createTime=%d", i.PID, i.CreateTime)
}

// Zero reports whether the info was never populated.
func (i Info) Zero() bool { return i.PID == 0 }

// CreateProcess flags. These are spelled out locally so the rationale for each
// one sits next to the value.
const (
	// createNewConsole gives the child its own console. It is what makes a
	// later CTRL+C possible; DETACHED_PROCESS would be simpler but leaves no
	// way to signal the JVM. The console is created hidden.
	createNewConsole = 0x00000010

	// createNewProcessGroup makes the child the root of its own group, so
	// console signals aimed at JARVIS never reach it by accident.
	createNewProcessGroup = 0x00000200

	// createBreakawayFromJob escapes an inherited job object. Without it a job
	// configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE would take the java
	// process down together with JARVIS.
	createBreakawayFromJob = 0x01000000
)

const baseCreationFlags = createNewConsole | createNewProcessGroup
