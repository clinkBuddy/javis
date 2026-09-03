//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// ErrNotInstalled is returned by the control helpers when the service is absent.
var ErrNotInstalled = errors.New("the JARVIS service is not installed")

// Install registers the service for automatic start at boot.
//
// root, when non-empty, is passed through as --home so the service uses the
// same data directory the installing administrator chose.
func Install(root string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(ServiceName); err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists; run `jarvis uninstall` first", ServiceName)
	}

	args := []string{"service"}
	if root != "" {
		args = append(args, "--home", root)
	}

	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		DisplayName: DisplayName,
		Description: Description,
		StartType:   mgr.StartAutomatic,
		// LocalSystem. Apps that need domain or mapped-drive access get a
		// per-app run-as account instead of widening the service identity.
		ServiceStartName: "",
	}, args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// If the core crashes, bring it back. Managed java processes survive the
	// gap untouched and get re-attached on the next start.
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400); err != nil {
		return fmt.Errorf("configure recovery actions: %w", err)
	}

	if err := eventlog.InstallAsEventCreate(ServiceName,
		eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("register event log source: %w", err)
	}
	return nil
}

// Uninstall stops and removes the service. Managed java processes keep running.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("stop service: %w", err)
		}
		if err := waitForState(func() (svc.State, error) {
			st, err := s.Query()
			return st.State, err
		}, svc.Stopped, 30*time.Second); err != nil {
			return err
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if err := eventlog.Remove(ServiceName); err != nil &&
		!strings.Contains(err.Error(), "cannot find") {
		return fmt.Errorf("remove event log source: %w", err)
	}
	return nil
}

func StartService() error {
	return withService(func(s *mgr.Service) error {
		if err := s.Start(); err != nil {
			return fmt.Errorf("start service: %w", err)
		}
		return waitForState(func() (svc.State, error) {
			st, err := s.Query()
			return st.State, err
		}, svc.Running, 30*time.Second)
	})
}

func StopService() error {
	return withService(func(s *mgr.Service) error {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("stop service: %w", err)
		}
		return waitForState(func() (svc.State, error) {
			st, err := s.Query()
			return st.State, err
		}, svc.Stopped, 30*time.Second)
	})
}

// Status returns the current service state, or ErrNotInstalled.
func Status() (svc.State, error) {
	var state svc.State
	err := withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		state = st.State
		return nil
	})
	return state, err
}

// StateString renders a service state for CLI output.
func StateString(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "STOPPED"
	case svc.StartPending:
		return "START_PENDING"
	case svc.StopPending:
		return "STOP_PENDING"
	case svc.Running:
		return "RUNNING"
	case svc.ContinuePending:
		return "CONTINUE_PENDING"
	case svc.PausePending:
		return "PAUSE_PENDING"
	case svc.Paused:
		return "PAUSED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", s)
	}
}

func withService(fn func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return ErrNotInstalled
	}
	defer s.Close()

	return fn(s)
}
