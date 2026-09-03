//go:build windows

package winproc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procAttachConsole            = kernel32.NewProc("AttachConsole")
	procFreeConsole              = kernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler    = kernel32.NewProc("SetConsoleCtrlHandler")
	procGenerateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const (
	ctrlCEvent = 0

	// detachedProcess gives the helper no console of its own, which is a
	// precondition for AttachConsole succeeding.
	detachedProcess = 0x00000008
)

// stopperTimeout bounds the helper. Attaching a console and raising an event
// takes milliseconds; anything longer means something is badly wrong and we
// should fall through to termination rather than hang a stop request.
const stopperTimeout = 10 * time.Second

// SendCtrlC delivers a console CTRL+C to the target process, which a JVM
// receives as SIGINT and answers by running its shutdown hooks.
//
// This must run in a short-lived helper process, never in the core, and the
// reason is a quirk of the API: CTRL_C_EVENT cannot be aimed at a specific
// process group. GenerateConsoleCtrlEvent only accepts group 0 for it, meaning
// "every process attached to my console". So the sender has to attach itself
// to the target's console first, and at that moment it becomes a recipient
// too. If the core did this it would signal itself and shut down along with
// the application it was trying to stop.
//
// Calling FreeConsole first lets `jarvis stopper` also work when a person runs
// it by hand from an ordinary command prompt, where the process already owns a
// console and AttachConsole would otherwise fail.
func SendCtrlC(pid uint32) error {
	procFreeConsole.Call()

	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("winproc: attach to console of %d: %w", pid, err)
	}
	defer procFreeConsole.Call()

	// Set the inheritable "ignore CTRL+C" attribute on ourselves before
	// raising the event, otherwise the helper dies mid-call.
	if r, _, err := procSetConsoleCtrlHandler.Call(0, 1); r == 0 {
		return fmt.Errorf("winproc: disable own ctrl+c handling: %w", err)
	}

	if r, _, err := procGenerateConsoleCtrlEvent.Call(ctrlCEvent, 0); r == 0 {
		return fmt.Errorf("winproc: raise ctrl+c on console of %d: %w", pid, err)
	}
	return nil
}

// RequestCtrlC asks a freshly spawned helper to signal the target. Returning
// successfully only means the event was raised: the caller still has to wait
// for the process to actually exit.
func RequestCtrlC(ctx context.Context, pid uint32) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("winproc: locate own executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, stopperTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "stopper", "--pid", strconv.FormatUint(uint64(pid), 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}

	// Pipes still work under DETACHED_PROCESS, so the helper's diagnostics are
	// not lost even though it has no console.
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if msg == "" {
			return fmt.Errorf("winproc: stopper helper for %d failed: %w", pid, err)
		}
		return fmt.Errorf("winproc: stopper helper for %d failed: %w: %s", pid, err, msg)
	}
	return nil
}
