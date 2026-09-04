// Package buildinfo carries values stamped in at link time.
package buildinfo

import (
	"fmt"
	"runtime"
)

var (
	Version = "0.1.0"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("JARVIS %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
