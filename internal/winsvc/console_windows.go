//go:build windows

package winsvc

import (
	"golang.org/x/sys/windows"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")

	user32         = windows.NewLazySystemDLL("user32.dll")
	procShowWindow = user32.NewProc("ShowWindow")
)

const swHide = 0

// HideConsole hides the console window of the current process.
//
// The binary is linked as a console application on purpose: `-H=windowsgui`
// would silence the CLI subcommands, which are the primary troubleshooting
// tool. Instead the modes that should not show a window (service, tray) hide
// it at startup. Returns false when there is no console, which is the normal
// case for a service started by the SCM.
func HideConsole() bool {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return false
	}
	procShowWindow.Call(hwnd, swHide)
	return true
}
