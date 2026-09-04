//go:build windows

package winsvc

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/sjkim/jarvis/internal/config"
)

var procShellExecuteW = modShell32.NewProc("ShellExecuteW")

// AdminURL is the local management UI for the given data root.
func AdminURL(home string) string {
	cfg, _, err := config.Load(home)
	if err != nil {
		return "http://127.0.0.1:9527/"
	}
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	addr := cfg.Server.Addr
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return fmt.Sprintf("%s://%s/", scheme, addr)
}

// OpenURL opens url with the user's default handler (normally a browser).
func OpenURL(url string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0,
		uintptr(swShownormal),
	)
	if r <= 32 {
		if callErr != nil {
			return fmt.Errorf("open url: %w", callErr)
		}
		return fmt.Errorf("open url: ShellExecute returned %d", r)
	}
	return nil
}
