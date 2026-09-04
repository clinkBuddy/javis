//go:build windows

package metrics

import (
	"encoding/binary"
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afINET              = 2
	afINET6             = 23
	tcpTableOwnerPIDAll = 5
	mibTCPListen        = 2
	mibTCPEstab         = 5
)

type tcpRow4 struct {
	state, localAddr, localPort, remoteAddr, remotePort, pid uint32
}

type tcpRow6 struct {
	localAddr  [16]byte
	localScope uint32
	localPort  uint32
	remoteAddr [16]byte
	remoteScp  uint32
	remotePort uint32
	state      uint32
	pid        uint32
}

func sampleTCP(pid uint32) tcpSnap {
	if pid == 0 {
		return tcpSnap{}
	}
	listenSet := map[int]bool{}
	for _, p := range tcp4Listen(pid) {
		listenSet[p] = true
	}
	for _, p := range tcp6Listen(pid) {
		listenSet[p] = true
	}
	listen := make([]int, 0, len(listenSet))
	for p := range listenSet {
		listen = append(listen, p)
	}
	sort.Ints(listen)

	active := 0
	for _, r := range tcp4Rows() {
		if r.pid == pid && r.state == mibTCPEstab && listenSet[netPort(r.localPort)] {
			active++
		}
	}
	for _, r := range tcp6Rows() {
		if r.pid == pid && r.state == mibTCPEstab && listenSet[netPort(r.localPort)] {
			active++
		}
	}
	return tcpSnap{listen: listen, active: active}
}

func tcp4Listen(pid uint32) []int {
	var ports []int
	for _, r := range tcp4Rows() {
		if r.pid == pid && r.state == mibTCPListen {
			ports = append(ports, netPort(r.localPort))
		}
	}
	return ports
}

func tcp6Listen(pid uint32) []int {
	var ports []int
	for _, r := range tcp6Rows() {
		if r.pid == pid && r.state == mibTCPListen {
			ports = append(ports, netPort(r.localPort))
		}
	}
	return ports
}

func tcp4Rows() []tcpRow4 {
	raw := tcpTable(afINET, uint32(unsafe.Sizeof(tcpRow4{})))
	if len(raw) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(raw[:4])
	rowSize := int(unsafe.Sizeof(tcpRow4{}))
	out := make([]tcpRow4, 0, n)
	off := 4
	for i := uint32(0); i < n && off+rowSize <= len(raw); i++ {
		out = append(out, *(*tcpRow4)(unsafe.Pointer(&raw[off])))
		off += rowSize
	}
	return out
}

func tcp6Rows() []tcpRow6 {
	raw := tcpTable(afINET6, uint32(unsafe.Sizeof(tcpRow6{})))
	if len(raw) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(raw[:4])
	rowSize := int(unsafe.Sizeof(tcpRow6{}))
	out := make([]tcpRow6, 0, n)
	off := 4
	for i := uint32(0); i < n && off+rowSize <= len(raw); i++ {
		out = append(out, *(*tcpRow6)(unsafe.Pointer(&raw[off])))
		off += rowSize
	}
	return out
}

func tcpTable(family uint32, rowSize uint32) []byte {
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDAll, 0)
	if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && size == 0 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0, uintptr(family), tcpTableOwnerPIDAll, 0)
	if r != 0 {
		return nil
	}
	if int(size) < len(buf) {
		buf = buf[:size]
	}
	_ = rowSize
	return buf
}

func netPort(p uint32) int {
	return int(uint16(p>>8) | uint16(p&0xff)<<8)
}
