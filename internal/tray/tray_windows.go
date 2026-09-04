//go:build windows

// Package tray owns the per-user notification-area icon.
//
// It cannot live inside the Windows service: services run in session 0 and
// have no desktop. The tray is a separate process that starts and stops the
// installed Windows service.
package tray

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/sjkim/jarvis/internal/winsvc"
)

const (
	className     = "JARVIS.TrayWindow"
	mutexName     = "Local\\JARVIS.Tray"
	iconID        = 1
	callbackMsg   = wmUser + 1
	timerID       = 1
	timerMs       = 3000
	idOpen        = 1001
	idStart       = 1002
	idStop        = 1003
	idExit        = 1004
	notifyIconVer = 0x00030000
)

const (
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmTimer         = 0x0113
	wmUser          = 0x0400
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	nifInfoFlags = 0x00000001 // NIIF_INFO

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfGrayed    = 0x00000001
	tpmRightBtn = 0x0002
	swHide      = 0
	wsPopup     = 0x80000000
)

type notifyIconData struct {
	size             uint32
	wnd              windows.HWND
	id               uint32
	flags            uint32
	callback         uint32
	icon             windows.Handle
	tip              [128]uint16
	state            uint32
	stateMask        uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guid             [16]byte
	balloonIcon      windows.Handle
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type point struct {
	x int32
	y int32
}

type msg struct {
	wnd     windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW       = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW        = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW         = modUser32.NewProc("DefWindowProcW")
	procGetMessageW            = modUser32.NewProc("GetMessageW")
	procTranslateMessage       = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW       = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage        = modUser32.NewProc("PostQuitMessage")
	procDestroyWindow          = modUser32.NewProc("DestroyWindow")
	procSetForegroundWindow    = modUser32.NewProc("SetForegroundWindow")
	procCreatePopupMenu        = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW            = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu         = modUser32.NewProc("TrackPopupMenu")
	procDestroyMenu            = modUser32.NewProc("DestroyMenu")
	procGetCursorPos           = modUser32.NewProc("GetCursorPos")
	procSetTimer               = modUser32.NewProc("SetTimer")
	procKillTimer              = modUser32.NewProc("KillTimer")
	procPostMessageW           = modUser32.NewProc("PostMessageW")
	procCreateIconFromResource = modUser32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon            = modUser32.NewProc("DestroyIcon")
	procShellNotifyIconW       = modShell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = modKernel32.NewProc("GetModuleHandleW")
	procShellExecuteW          = modShell32.NewProc("ShellExecuteW")
)

var (
	ctrl  *Controller
	nid   notifyIconData
	hIcon windows.Handle
	hWnd  windows.HWND
)

// Run hides the console, takes a single-instance lock, shows the icon and
// pumps messages until the user chooses 종료.
func Run(home string) error {
	winsvc.HideConsole()

	lock, first, err := acquireSingleton()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(lock)
	if !first {
		// The icon is already up. Still start the service — launching
		// jarvis.exe always means "bring the server up".
		if err := NewController(home).Start(); err != nil {
			winsvc.NotifyError("JARVIS", err.Error())
		}
		return nil
	}

	ctrl = NewController(home)

	if err := startWindow(); err != nil {
		return err
	}

	// jarvis.exe (and logon autostart) always start the Windows service.
	// Failure is shown as a balloon; the icon stays so the user can retry
	// from the menu. Leaving the tray does not stop the service.
	if err := ctrl.Start(); err != nil {
		balloon("JARVIS", "서버를 시작하지 못했습니다: "+err.Error())
	} else {
		balloon("JARVIS", "서버를 실행했습니다.")
	}
	refreshTip()

	return messageLoop()
}

func acquireSingleton() (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return 0, false, err
	}
	h, err := windows.CreateMutex(nil, true, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return h, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("tray: create mutex: %w", err)
	}
	return h, true, nil
}

func startWindow() error {
	instance, _, _ := procGetModuleHandleW.Call(0)

	icon, err := loadIcon()
	if err != nil {
		return err
	}
	hIcon = icon

	clsName, _ := windows.UTF16PtrFromString(className)
	wc := wndClassEx{
		size:      uint32(unsafe.Sizeof(wndClassEx{})),
		wndProc:   windows.NewCallback(wndProc),
		instance:  windows.Handle(instance),
		className: clsName,
		icon:      icon,
		iconSm:    icon,
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return fmt.Errorf("tray: register class: %w", callErr)
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(clsName)),
		wsPopup,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("tray: create window: %w", callErr)
	}
	hWnd = windows.HWND(hwnd)

	nid = notifyIconData{
		size:     uint32(unsafe.Sizeof(notifyIconData{})),
		wnd:      hWnd,
		id:       iconID,
		flags:    nifMessage | nifIcon | nifTip,
		callback: callbackMsg,
		icon:     icon,
	}
	setTip(&nid, ctrl.StatusText())
	if r, _, callErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return fmt.Errorf("tray: add icon: %w", callErr)
	}

	procSetTimer.Call(hwnd, timerID, timerMs, 0)
	return nil
}

func loadIcon() (windows.Handle, error) {
	ico := buildIcon()
	// Skip the ICO directory (6 + 16 bytes) and hand CreateIconFromResourceEx
	// the DIB, which is what it expects.
	if len(ico) < 22 {
		return 0, errors.New("tray: icon is truncated")
	}
	dib := ico[22:]
	h, _, err := procCreateIconFromResource.Call(
		uintptr(unsafe.Pointer(&dib[0])),
		uintptr(len(dib)),
		1,
		notifyIconVer,
		32, 32,
		0,
	)
	if h == 0 {
		return 0, fmt.Errorf("tray: create icon: %w", err)
	}
	return windows.Handle(h), nil
}

func messageLoop() error {
	var m msg
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		// GetMessage returns 0 on WM_QUIT and -1 on failure.
		if int32(ret) == 0 {
			return nil
		}
		if int32(ret) < 0 {
			return fmt.Errorf("tray: get message: %w", err)
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case callbackMsg:
		switch lParam {
		case wmLButtonDblClk, wmRButtonUp, wmContextMenu:
			showMenu(hwnd)
		}
		return 0
	case wmCommand:
		switch uint16(wParam) {
		case idOpen:
			onOpen()
		case idStart:
			onStart()
		case idStop:
			onStop()
		case idExit:
			onExit(hwnd)
		}
		return 0
	case wmTimer:
		refreshTip()
		return 0
	case wmDestroy:
		cleanup()
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func showMenu(hwnd windows.HWND) {
	h, _, _ := procCreatePopupMenu.Call()
	if h == 0 {
		return
	}
	defer procDestroyMenu.Call(h)

	running := ctrl.Running()
	if running {
		appendItem(h, mfGrayed, idStart, "서버 실행")
		appendItem(h, mfString, idStop, "서버 중지")
		appendItem(h, mfString, idOpen, "관리자 UI")
	} else {
		appendItem(h, mfString, idStart, "서버 실행")
		appendItem(h, mfGrayed, idStop, "서버 중지")
	}
	appendItem(h, mfSeparator, 0, "")
	appendItem(h, mfString, idExit, "트레이 종료")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// TrackPopupMenu needs the window to be foreground or the menu vanishes
	// the moment it appears — a well-known Shell quirk.
	procSetForegroundWindow.Call(uintptr(hwnd))
	procTrackPopupMenu.Call(h, tpmRightBtn, uintptr(pt.x), uintptr(pt.y), 0, uintptr(hwnd), 0)
	procPostMessageW.Call(uintptr(hwnd), 0, 0, 0)
}

func appendItem(menu uintptr, flags, id uintptr, label string) {
	p, _ := windows.UTF16PtrFromString(label)
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(p)))
}

func onOpen() {
	openBrowser(ctrl.AdminURL())
}

func onStart() {
	if err := ctrl.Start(); err != nil {
		balloon("JARVIS", err.Error())
	} else {
		balloon("JARVIS", "서버를 시작했습니다.\n"+ctrl.AdminURL())
	}
	refreshTip()
}

func onStop() {
	if err := ctrl.Stop(); err != nil {
		balloon("JARVIS", err.Error())
	} else {
		balloon("JARVIS", "서버를 중지했습니다. 실행 중인 Java 프로세스는 그대로입니다.")
	}
	refreshTip()
}

func onExit(hwnd windows.HWND) {
	procDestroyWindow.Call(uintptr(hwnd))
}

func cleanup() {
	if hWnd != 0 {
		procKillTimer.Call(uintptr(hWnd), timerID)
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}
	if hIcon != 0 {
		procDestroyIcon.Call(uintptr(hIcon))
		hIcon = 0
	}
	if ctrl != nil {
		ctrl.Close()
	}
}

func refreshTip() {
	if hWnd == 0 {
		return
	}
	setTip(&nid, ctrl.StatusText())
	nid.flags = nifMessage | nifIcon | nifTip
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func balloon(title, text string) {
	setUTF16(nid.infoTitle[:], title)
	setUTF16(nid.info[:], text)
	nid.infoFlags = nifInfoFlags
	nid.timeoutOrVersion = 8000
	nid.flags = nifMessage | nifIcon | nifTip | nifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func setTip(n *notifyIconData, s string) {
	setUTF16(n.tip[:], s)
}

func setUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

func openBrowser(url string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(url)
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
}
