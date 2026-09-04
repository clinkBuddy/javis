//go:build windows

package main

import (
	"golang.org/x/sys/windows/svc"

	"github.com/sjkim/jarvis/internal/winsvc"
)

// cmdLaunch is what the desktop shortcut runs. No notification-area icon:
// the Windows service is the only long-running process.
//
//   - service stopped  → start it (and return)
//   - service running  → open the admin UI immediately
func cmdLaunch(args []string) error {
	winsvc.HideConsole()

	fs := newFlagSet("open")
	home := homeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	_ = winsvc.DisableTrayAutostart()

	if err := winsvc.EnsureInstalled(*home); err != nil {
		winsvc.NotifyError("JARVIS", err.Error())
		return err
	}

	st, err := winsvc.Status()
	if err != nil {
		winsvc.NotifyError("JARVIS", err.Error())
		return err
	}

	switch st {
	case svc.Running:
		if err := winsvc.OpenURL(winsvc.AdminURL(*home)); err != nil {
			winsvc.NotifyError("JARVIS", "관리자 UI를 열지 못했습니다: "+err.Error())
			return err
		}
		return nil
	case svc.StartPending:
		if err := winsvc.StartService(); err != nil {
			winsvc.NotifyError("JARVIS", err.Error())
			return err
		}
		return nil
	default:
		if err := winsvc.StartService(); err != nil {
			winsvc.NotifyError("JARVIS", "서비스를 시작하지 못했습니다: "+err.Error())
			return err
		}
		return nil
	}
}
