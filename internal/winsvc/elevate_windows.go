//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	seeMaskNoCloseProcess = 0x00000040
	swShownormal          = 1
	mbOK                  = 0x00000000
	mbIconError           = 0x00000010
)

// Default service DACL plus start/stop/query for interactive users and the
// local Users group. Without those ACEs a double-clicked tray cannot control
// the service it just registered.
const interactiveServiceSDDL = `D:` +
	`(A;;CCLCSWRPWPDTLOCRRC;;;SY)` +
	`(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)` +
	`(A;;CCLCSWLOCRRC;;;IU)` +
	`(A;;CCLCSWLOCRRC;;;SU)` +
	`(A;;LCRPWPLO;;;IU)` +
	`(A;;LCRPWPLO;;;BU)`

type shellExecuteInfo struct {
	cbSize     uint32
	fMask      uint32
	hwnd       windows.HWND
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instApp    windows.Handle
	idList     uintptr
	class      *uint16
	keyClass   windows.Handle
	hotKey     uint32
	iconOrMon  windows.Handle
	process    windows.Handle
}

var (
	modShell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = modShell32.NewProc("ShellExecuteExW")
	modUser32           = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW     = modUser32.NewProc("MessageBoxW")
)

// IsElevated reports whether this process is running with a full administrator
// token (the UAC elevated case).
func IsElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// EnsureInstalled registers the Windows service if it is missing, grants
// interactive users start/stop rights, and records tray autostart for this
// user. A non-elevated caller is relaunched once via UAC to do the install.
func EnsureInstalled(root string) error {
	needInstall := !Installed()
	needACL := Installed() && !CanControl()

	if !needInstall && !needACL {
		_ = EnableTrayAutostart(root)
		return nil
	}

	if !IsElevated() {
		args := []string{"install", "--start=false", "--tray=false"}
		if !needInstall && needACL {
			args = append(args, "--acl-only")
		}
		if root != "" {
			args = append(args, "--home", root)
		}
		if err := RelaunchElevated(args); err != nil {
			return err
		}
		if !Installed() {
			return errors.New("서비스 등록이 취소되었거나 실패했습니다")
		}
		if !CanControl() {
			return errors.New("서비스를 시작·중지할 권한이 없습니다. 관리자 권한으로 다시 실행하세요")
		}
		_ = EnableTrayAutostart(root)
		return nil
	}

	if needInstall {
		if err := Install(root); err != nil && !errors.Is(err, ErrAlreadyInstalled) {
			return err
		}
	}
	if err := AllowInteractiveControl(); err != nil {
		return err
	}
	_ = EnableTrayAutostart(root)
	return nil
}

// AllowInteractiveControl lets members of Users start, stop and query the
// service. Must be called elevated; Install does this as part of first setup.
func AllowInteractiveControl() error {
	sd, err := windows.SecurityDescriptorFromString(interactiveServiceSDDL)
	if err != nil {
		return fmt.Errorf("parse service ACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read service DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		ServiceName,
		windows.SE_SERVICE,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("grant start/stop to interactive users: %w", err)
	}
	return nil
}

// RelaunchElevated starts this executable again with a UAC prompt and waits
// for it to exit.
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, quoteArg(a))
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}

	info := shellExecuteInfo{
		cbSize:     uint32(unsafe.Sizeof(shellExecuteInfo{})),
		fMask:      seeMaskNoCloseProcess,
		verb:       verb,
		file:       file,
		parameters: params,
		show:       swShownormal,
	}
	r, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return errors.New("관리자 권한 요청이 취소되었습니다")
		}
		return fmt.Errorf("elevate: %w", callErr)
	}
	if info.process == 0 {
		return errors.New("elevate: no process handle")
	}
	defer windows.CloseHandle(info.process)

	if _, err := windows.WaitForSingleObject(info.process, windows.INFINITE); err != nil {
		return fmt.Errorf("elevate: wait: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return fmt.Errorf("elevate: exit code: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("elevated command exited with status %d", code)
	}
	return nil
}

// NotifyError shows a desktop message box. Used when a double-clicked
// jarvis.exe cannot finish install — stderr would vanish with the console.
func NotifyError(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbOK|mbIconError)
}
