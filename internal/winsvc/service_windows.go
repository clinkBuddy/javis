//go:build windows

// Package winsvc runs JARVIS under the Windows Service Control Manager and
// handles install/uninstall.
package winsvc

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"github.com/sjkim/jarvis/internal/core"
)

const (
	ServiceName = "JARVIS"
	DisplayName = "JARVIS - Spring Boot jar Manager"
	Description = "Hosts, supervises and monitors Spring Boot jar applications on this machine."
)

// IsService reports whether the process was launched by the SCM.
func IsService() (bool, error) {
	return svc.IsWindowsService()
}

// Run hands control to the SCM. It only returns when the service stops.
func Run(root string) error {
	HideConsole()
	return svc.Run(ServiceName, &handler{root: root})
}

type handler struct {
	root string
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	// Startup does disk and database work, so ask the SCM for extra time
	// rather than risk a spurious "service did not respond" failure.
	status <- svc.Status{State: svc.StartPending, WaitHint: 30_000}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := core.Bootstrap(ctx, h.root, false)
	if err != nil {
		reportFatal(fmt.Sprintf("bootstrap failed: %v", err))
		status <- svc.Status{State: svc.Stopped}
		return true, 1
	}
	if err := c.Start(ctx); err != nil {
		reportFatal(fmt.Sprintf("start failed: %v", err))
		_ = c.Shutdown()
		status <- svc.Status{State: svc.Stopped}
		return true, 1
	}

	status <- svc.Status{State: svc.Running, Accepts: accepted}

	exitCode := uint32(0)
loop:
	for {
		select {
		case cr := <-req:
			switch cr.Cmd {
			case svc.Interrogate:
				status <- cr.CurrentStatus
			case svc.Stop, svc.Shutdown:
				break loop
			default:
				c.Log.Warn("unexpected service control", "cmd", cr.Cmd)
			}
		case serveErr := <-c.API.Err():
			if serveErr != nil {
				c.Log.Error("admin interface failed", "err", serveErr)
				exitCode = 1
			}
			break loop
		}
	}

	status <- svc.Status{State: svc.StopPending, WaitHint: 15_000}
	cancel()
	if err := c.Shutdown(); err != nil {
		c.Log.Error("shutdown reported errors", "err", err)
	}
	status <- svc.Status{State: svc.Stopped}
	return false, exitCode
}

// reportFatal writes to the Windows event log. Bootstrap failures can happen
// before the file logger exists, and the event log is the only place an
// operator will think to look when the service refuses to start.
func reportFatal(msg string) {
	el, err := eventlog.Open(ServiceName)
	if err != nil {
		return
	}
	defer el.Close()
	_ = el.Error(1, msg)
}

// waitForState polls a service until it reaches want or the deadline passes.
func waitForState(query func() (svc.State, error), want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := query()
		if err != nil {
			return err
		}
		if st == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service state %d (currently %d)", want, st)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
