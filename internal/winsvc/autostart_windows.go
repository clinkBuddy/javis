//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"strings"

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

	cmd := quoteArg(exe) + " tray"
	if root != "" {
		cmd += " --home " + quoteArg(root)
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

// quoteArg wraps a value in double quotes for a Windows command line.
//
// fmt's %q must not be used here: it applies Go escaping, which turns
// C:\dev\jarvis into "C:\\dev\\jarvis". Windows quoting only treats the double
// quote as special, and a run of backslashes immediately before the closing
// quote has to be doubled so it is not read as escaping that quote.
func quoteArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')

	backslashes := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			backslashes++
			b.WriteByte(c)
		case '"':
			// N backslashes then a quote must become 2N+1 backslashes then the
			// quote; N are already written, so add N+1 more.
			b.WriteString(strings.Repeat(`\`, backslashes+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			backslashes = 0
			b.WriteByte(c)
		}
	}

	// Trailing backslashes would otherwise escape the closing quote.
	b.WriteString(strings.Repeat(`\`, backslashes))
	b.WriteByte('"')
	return b.String()
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
