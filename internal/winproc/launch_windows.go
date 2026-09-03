//go:build windows

package winproc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// LaunchSpec describes a process to start detached from JARVIS.
type LaunchSpec struct {
	Exe  string
	Args []string
	Dir  string

	// Env replaces the environment when non-nil; nil inherits JARVIS'.
	Env []string

	// LogPath receives both stdout and stderr. The child holds this handle for
	// its whole life, so the file cannot be rotated underneath it; callers are
	// expected to use a fresh path per launch.
	LogPath string
}

// Launch starts the process and returns once it is running and identified.
//
// The returned Info is everything needed to find the process again after a
// JARVIS restart, so callers must persist it before doing anything else.
func Launch(spec LaunchSpec) (Info, error) {
	if spec.Exe == "" {
		return Info{}, errors.New("winproc: Exe is required")
	}
	if spec.LogPath == "" {
		return Info{}, errors.New("winproc: LogPath is required")
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o750); err != nil {
		return Info{}, fmt.Errorf("winproc: create log directory: %w", err)
	}

	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return Info{}, fmt.Errorf("winproc: open %s: %w", spec.LogPath, err)
	}
	defer logFile.Close()

	info, err := start(spec, logFile, baseCreationFlags|createBreakawayFromJob)
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The containing job forbids breakaway. Starting without the flag is
		// still correct; it only means the child shares JARVIS' job and would
		// be killed with it if that job is ever closed with kill-on-close set.
		info, err = start(spec, logFile, baseCreationFlags)
	}
	return info, err
}

func start(spec LaunchSpec, out *os.File, flags uint32) (Info, error) {
	cmd := exec.Command(spec.Exe, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: flags,
		HideWindow:    true,
	}

	if err := cmd.Start(); err != nil {
		return Info{}, fmt.Errorf("winproc: start %s: %w", spec.Exe, err)
	}

	pid := uint32(cmd.Process.Pid)

	// Release the process handle straight away. From here on JARVIS treats the
	// child exactly like one it adopted after a restart, which means the
	// launch path and the re-attach path exercise the same code.
	defer cmd.Process.Release()

	createTime, err := CreateTime(pid)
	if err != nil {
		return Info{PID: pid}, fmt.Errorf(
			"winproc: process %d started but could not be identified, "+
				"it most likely exited immediately (check %s): %w",
			pid, spec.LogPath, err)
	}
	return Info{PID: pid, CreateTime: createTime}, nil
}
