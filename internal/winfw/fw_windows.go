//go:build windows

package winfw

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

const ruleName = "JARVIS"

// Allow opens an inbound Windows Firewall rule for the admin UI when it is
// bound to a non-loopback address. Failures are returned so the caller can
// log them; the listener itself does not depend on the rule.
func Allow(listenAddr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return fmt.Errorf("listen addr: %w", err)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	_ = netsh("advfirewall", "firewall", "delete", "rule", "name="+ruleName)
	return netsh(
		"advfirewall", "firewall", "add", "rule",
		"name="+ruleName,
		"dir=in",
		"action=allow",
		"program="+exe,
		"enable=yes",
		"profile=any",
		"protocol=TCP",
		"localport="+port,
	)
}

// Remove deletes the inbound rule created by Allow. Missing rules are ignored.
func Remove() error {
	return netsh("advfirewall", "firewall", "delete", "rule", "name="+ruleName)
}

func netsh(args ...string) error {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("netsh %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("netsh %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}
