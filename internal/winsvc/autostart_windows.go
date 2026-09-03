//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath    = `Software\Microsoft\Windows\CurrentVersion\Run`
	trayValueName = "JARVIS Tray"
)

// The tray icon cannot live in the service. Services run in session 0, which
// has no interactive desktop, so the notification area is unreachable from
// there. The tray therefore runs as a separate per-user process that talks to
// the service over its local HTTP API, and it is started at logon via the
// classic Run key.

// EnableTrayAutostart registers `jarvis tray` to start at logon for the
// current user.
func EnableTrayAutostart(root string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	cmd := fmt.Sprintf("%q tray", exe)
	if root != "" {
		cmd += fmt.Sprintf(" --home %q", root)
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()

	if err := k.SetStringValue(trayValueName, cmd); err != nil {
		return fmt.Errorf("write Run value: %w", err)
	}
	return nil
}

func DisableTrayAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()

	if err := k.DeleteValue(trayValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete Run value: %w", err)
	}
	return nil
}

// TrayAutostartCommand returns the registered command, or "" when autostart is
// not configured for the current user.
func TrayAutostartCommand() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer k.Close()

	v, _, err := k.GetStringValue(trayValueName)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return v, err
}
