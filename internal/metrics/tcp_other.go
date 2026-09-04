//go:build !windows

package metrics

func sampleTCP(uint32) tcpSnap { return tcpSnap{} }
