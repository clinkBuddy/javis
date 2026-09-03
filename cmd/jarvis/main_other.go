//go:build !windows

package main

import (
	"fmt"
	"os"

	"github.com/sjkim/jarvis/internal/buildinfo"
)

// JARVIS supervises java processes through Win32 APIs (detached CreateProcess
// flags, console control events, DPAPI). Those have no portable equivalent, so
// the host side is Windows only. Managed targets may of course be Linux, which
// is the remote runner planned for P6.
func main() {
	fmt.Fprintf(os.Stderr, "%s\njarvis: the management host must run on Windows\n", buildinfo.String())
	os.Exit(1)
}
