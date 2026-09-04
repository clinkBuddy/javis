//go:build windows

package winproc

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Usage is a point-in-time reading of a process. CPU percent is not here
// because it can only be derived from two readings; the collector holds the
// previous sample and does that arithmetic.
type Usage struct {
	CPUTime      time.Duration // kernel + user
	RSSBytes     int64
	PrivateBytes int64
	Threads      int
	Handles      int
}

type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var (
	modpsapi                  = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo  = modpsapi.NewProc("GetProcessMemoryInfo")
	modkernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessHandleCount = modkernel32.NewProc("GetProcessHandleCount")
	procGetSystemTimes        = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx  = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceExW   = modkernel32.NewProc("GetDiskFreeSpaceExW")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// SampleUsage reads memory, CPU time, handle and thread counts for pid.
//
// The creation time is checked when expected is non-zero, so a recycled PID
// cannot be reported as the application it replaced.
func SampleUsage(pid uint32, expectedCreateTime int64) (Usage, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Usage{}, fmt.Errorf("winproc: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	if expectedCreateTime != 0 {
		got, err := createTimeOf(h, pid)
		if err != nil {
			return Usage{}, err
		}
		if got != expectedCreateTime {
			return Usage{}, ErrNotRunning
		}
	}

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return Usage{}, fmt.Errorf("winproc: process times for %d: %w", pid, err)
	}

	var mem processMemoryCountersEx
	mem.CB = uint32(unsafe.Sizeof(mem))
	r1, _, callErr := procGetProcessMemoryInfo.Call(
		uintptr(h), uintptr(unsafe.Pointer(&mem)), uintptr(mem.CB))
	if r1 == 0 {
		return Usage{}, fmt.Errorf("winproc: memory info for %d: %w", pid, callErr)
	}

	var handles uint32
	r1, _, callErr = procGetProcessHandleCount.Call(uintptr(h), uintptr(unsafe.Pointer(&handles)))
	if r1 == 0 {
		// Handle count is nice-to-have; a failure here must not drop the
		// rest of the sample, which is still useful on its own.
		handles = 0
	}

	return Usage{
		CPUTime:      filetimeDuration(kernel) + filetimeDuration(user),
		RSSBytes:     int64(mem.WorkingSetSize),
		PrivateBytes: int64(mem.PrivateUsage),
		Threads:      countThreads(pid),
		Handles:      int(handles),
	}, nil
}

func filetimeDuration(ft windows.Filetime) time.Duration {
	ticks := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return time.Duration(ticks * 100)
}

// countThreads walks the system thread snapshot. There is no cheaper
// documented way to get a single process's thread count with the limited
// query right, and a handful of managed JVMs every few seconds is cheap.
func countThreads(pid uint32) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Thread32First(snap, &entry); err != nil {
		return 0
	}
	n := 0
	for {
		if entry.OwnerProcessID == pid {
			n++
		}
		if err := windows.Thread32Next(snap, &entry); err != nil {
			break
		}
	}
	return n
}

// HostUsage is a point-in-time reading of the machine JARVIS is running on.
type HostUsage struct {
	IdleTime   time.Duration
	KernelTime time.Duration
	UserTime   time.Duration
	MemTotal   int64
	MemUsed    int64
	SwapUsed   int64
	DiskTotal  int64
	DiskFree   int64
	Procs      int
}

// SampleHost reads memory, CPU times, process count and the free space of
// the volume that holds path — typically the JARVIS data root, so an operator
// can see a full disk before the next jar upload fails.
func SampleHost(diskPath string) (HostUsage, error) {
	var idle, kernel, user windows.Filetime
	r1, _, callErr := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	if r1 == 0 {
		return HostUsage{}, fmt.Errorf("winproc: system times: %w", callErr)
	}

	var mem memoryStatusEx
	mem.Length = uint32(unsafe.Sizeof(mem))
	r1, _, callErr = procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	if r1 == 0 {
		return HostUsage{}, fmt.Errorf("winproc: memory status: %w", callErr)
	}

	var diskFree, diskTotal, avail uint64
	if diskPath != "" {
		p, err := windows.UTF16PtrFromString(diskPath)
		if err == nil {
			_, _, _ = procGetDiskFreeSpaceExW.Call(
				uintptr(unsafe.Pointer(p)),
				uintptr(unsafe.Pointer(&avail)),
				uintptr(unsafe.Pointer(&diskTotal)),
				uintptr(unsafe.Pointer(&diskFree)))
		}
	}

	return HostUsage{
		IdleTime:   filetimeDuration(idle),
		KernelTime: filetimeDuration(kernel),
		UserTime:   filetimeDuration(user),
		MemTotal:   int64(mem.TotalPhys),
		MemUsed:    int64(mem.TotalPhys - mem.AvailPhys),
		SwapUsed:   int64(mem.TotalPageFile - mem.AvailPageFile),
		DiskTotal:  int64(diskTotal),
		DiskFree:   int64(diskFree),
		Procs:      countProcesses(),
	}, nil
}

func countProcesses() int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return 0
	}
	n := 0
	for {
		n++
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return n
}
