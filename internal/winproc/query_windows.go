//go:build windows

package winproc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ntdll                         = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
)

const (
	// ProcessCommandLineInformation. Reading the command line this way needs
	// only PROCESS_QUERY_LIMITED_INFORMATION, unlike poking at the PEB with
	// ReadProcessMemory, and it avoids a WMI round trip. Windows 8.1+.
	processCommandLineInformation = 60

	statusInfoLengthMismatch = 0xC0000004
	statusBufferTooSmall     = 0xC0000023
	statusBufferOverflow     = 0x80000005
)

// queryAccess is the least privilege that still allows reading creation time
// and command line, and waiting for exit.
const queryAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE

// CreateTime returns the raw creation FILETIME of a process.
func CreateTime(pid uint32) (int64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("winproc: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	return createTimeOf(h, pid)
}

func createTimeOf(h windows.Handle, pid uint32) (int64, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("winproc: process times for %d: %w", pid, err)
	}
	return int64(creation.HighDateTime)<<32 | int64(creation.LowDateTime), nil
}

// CommandLine returns the full command line of a running process, which is
// where the instance marker lives.
func CommandLine(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("winproc: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	return commandLineOf(h, pid)
}

func commandLineOf(h windows.Handle, pid uint32) (string, error) {
	// The result is an NTUnicodeString whose Buffer points just past itself,
	// so one allocation holds both. Start generously and grow on demand.
	size := uint32(1024)
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		var needed uint32
		status, _, _ := procNtQueryInformationProcess.Call(
			uintptr(h),
			processCommandLineInformation,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
			uintptr(unsafe.Pointer(&needed)),
		)
		switch uint32(status) {
		case 0:
			us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
			return windows.UTF16PtrToString(us.Buffer), nil
		case statusInfoLengthMismatch, statusBufferTooSmall, statusBufferOverflow:
			if needed <= size {
				needed = size * 2
			}
			size = needed
		default:
			return "", fmt.Errorf(
				"winproc: query command line of %d: NTSTATUS 0x%X", pid, uint32(status))
		}
	}
	return "", fmt.Errorf("winproc: command line of %d did not fit in %d bytes", pid, size)
}

// Alive reports whether the identified process is still running.
//
// Matching the creation time is what makes this safe. Windows reuses PIDs
// aggressively, so a bare PID check would happily report an unrelated process
// as "our" application and, worse, let a later stop request kill it.
func Alive(target Info) (bool, error) {
	if target.PID == 0 {
		return false, nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, target.PID)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return false, nil
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			// The PID has been recycled by a process we are not allowed to
			// inspect, so it is certainly not ours.
			return false, nil
		}
		return false, fmt.Errorf("winproc: open process %d: %w", target.PID, err)
	}
	defer windows.CloseHandle(h)

	// An exited-but-not-yet-reaped process still opens successfully.
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != stillActive {
		return false, nil
	}

	createTime, err := createTimeOf(h, target.PID)
	if err != nil {
		return false, err
	}
	if target.CreateTime != 0 && createTime != target.CreateTime {
		return false, nil
	}
	return true, nil
}

const stillActive = 259 // STILL_ACTIVE

// WaitExit blocks until the process exits, ctx is cancelled, or timeout
// elapses. It reports whether the process is gone.
func WaitExit(ctx context.Context, target Info, timeout time.Duration) (bool, error) {
	h, err := windows.OpenProcess(queryAccess, false, target.PID)
	if err != nil {
		// Cannot be opened at all: treat as gone.
		return true, nil
	}
	defer windows.CloseHandle(h)

	createTime, err := createTimeOf(h, target.PID)
	if err != nil || (target.CreateTime != 0 && createTime != target.CreateTime) {
		return true, nil
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		// Wake up regularly so context cancellation stays responsive instead
		// of being blocked inside a single long wait.
		slice := min(remaining, 200*time.Millisecond)
		switch ev, err := windows.WaitForSingleObject(h, uint32(slice.Milliseconds())); {
		case err != nil:
			return false, fmt.Errorf("winproc: wait on %d: %w", target.PID, err)
		case ev == uint32(windows.WAIT_OBJECT_0):
			return true, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
}

// Terminate is the last resort. It cannot be trapped, so JVM shutdown hooks do
// not run and the application gets no chance to clean up.
func Terminate(target Info) error {
	alive, err := Alive(target)
	if err != nil {
		return err
	}
	if !alive {
		return ErrNotRunning
	}

	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, target.PID)
	if err != nil {
		return fmt.Errorf("winproc: open process %d for terminate: %w", target.PID, err)
	}
	defer windows.CloseHandle(h)

	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("winproc: terminate %d: %w", target.PID, err)
	}
	return nil
}

// Match is a process found by scanning command lines.
type Match struct {
	Info
	ParentPID   uint32
	Image       string
	CommandLine string
}

// FindByMarker returns every running process whose command line contains
// marker.
//
// This is the fallback for adoption when the recorded PID is missing or no
// longer matches, and it is also how orphans left behind by a crashed JARVIS
// are surfaced. Every process is inspected rather than filtering on image name
// first, because a JVM can be launched through a wrapper executable; the cost
// is one cheap syscall pair per process and this only runs on startup.
func FindByMarker(marker string) ([]Match, error) {
	if marker == "" {
		return nil, errors.New("winproc: marker is required")
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("winproc: process snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("winproc: enumerate processes: %w", err)
	}

	var matches []Match
	for {
		if pid := entry.ProcessID; pid > 4 {
			if m, ok := inspect(pid, marker); ok {
				m.ParentPID = entry.ParentProcessID
				m.Image = windows.UTF16ToString(entry.ExeFile[:])
				matches = append(matches, m)
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return matches, nil
			}
			return matches, fmt.Errorf("winproc: enumerate processes: %w", err)
		}
	}
}

// inspect opens one process and checks it for the marker. Failures are not
// errors: most are simply processes this account may not inspect, and a
// protected system process is never one of ours.
func inspect(pid uint32, marker string) (Match, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Match{}, false
	}
	defer windows.CloseHandle(h)

	cmdline, err := commandLineOf(h, pid)
	if err != nil || !strings.Contains(cmdline, marker) {
		return Match{}, false
	}
	createTime, err := createTimeOf(h, pid)
	if err != nil {
		return Match{}, false
	}
	return Match{
		Info:        Info{PID: pid, CreateTime: createTime},
		CommandLine: cmdline,
	}, true
}
