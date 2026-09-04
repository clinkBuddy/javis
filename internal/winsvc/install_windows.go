//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

var (
	// ErrNotInstalled is returned by the control helpers when the service is absent.
	ErrNotInstalled = errors.New("the JARVIS service is not installed")
	// ErrAlreadyInstalled is returned by Install when the service exists.
	ErrAlreadyInstalled = errors.New("the JARVIS service is already installed")
)

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
		return ErrAlreadyInstalled
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
	st, err := Status()
	if err != nil {
		return err
	}
	switch st {
	case svc.Running:
		return nil
	case svc.StartPending:
		return waitForState(Status, svc.Running, 30*time.Second)
	}

	s, done, err := openFor(windows.SERVICE_START | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer done()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return waitForState(func() (svc.State, error) {
		st, err := s.Query()
		return st.State, err
	}, svc.Running, 30*time.Second)
}

func StopService() error {
	st, err := Status()
	if err != nil {
		return err
	}
	switch st {
	case svc.Stopped:
		return nil
	case svc.StopPending:
		return waitForState(Status, svc.Stopped, 30*time.Second)
	}

	s, done, err := openFor(windows.SERVICE_STOP | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer done()

	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	return waitForState(func() (svc.State, error) {
		st, err := s.Query()
		return st.State, err
	}, svc.Stopped, 30*time.Second)
}

// Status returns the current service state, or ErrNotInstalled.
//
// It opens the service manager with SC_MANAGER_CONNECT rather than full
// access, so `jarvis status` works from an ordinary command prompt. Only the
// commands that actually change something require elevation.
func Status() (svc.State, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, fmt.Errorf("connect to service manager: %w", err)
	}
	m := &mgr.Mgr{Handle: h}
	defer m.Disconnect()

	name, err := windows.UTF16PtrFromString(ServiceName)
	if err != nil {
		return 0, err
	}
	sh, err := windows.OpenService(m.Handle, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return 0, ErrNotInstalled
	}
	s := &mgr.Service{Name: ServiceName, Handle: sh}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return 0, fmt.Errorf("query service: %w", err)
	}
	return st.State, nil
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

// Installed reports whether the Windows service is registered.
func Installed() bool {
	_, err := Status()
	return err == nil
}

// CanControl reports whether this process can start and stop the service.
// After AllowInteractiveControl, a non-elevated tray can do that.
func CanControl() bool {
	s, done, err := openFor(windows.SERVICE_START | windows.SERVICE_STOP | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	done()
	return s != nil
}

// openFor opens the service with exactly the rights the caller needs.
// SC_MANAGER_CONNECT is enough for start/stop once the service DACL allows it,
// so the tray does not have to run elevated after the first install.
func openFor(access uint32) (*mgr.Service, func(), error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to service manager: %w", err)
	}

	name, err := windows.UTF16PtrFromString(ServiceName)
	if err != nil {
		_ = windows.CloseServiceHandle(h)
		return nil, nil, err
	}
	sh, err := windows.OpenService(h, name, access)
	if err != nil {
		_ = windows.CloseServiceHandle(h)
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil, ErrNotInstalled
		}
		return nil, nil, err
	}
	s := &mgr.Service{Name: ServiceName, Handle: sh}
	return s, func() {
		s.Close()
		_ = windows.CloseServiceHandle(h)
	}, nil
}
