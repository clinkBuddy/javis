package metrics

import (
	"testing"
	"time"
)

func TestCPUPercentFullyBusyCore(t *testing.T) {
	got := cpuPercent(time.Second, 2*time.Second, time.Second)
	if got != 100 {
		t.Errorf("cpuPercent = %v, want 100", got)
	}
}

func TestCPUPercentHalfBusy(t *testing.T) {
	got := cpuPercent(0, 500*time.Millisecond, time.Second)
	if got != 50 {
		t.Errorf("cpuPercent = %v, want 50", got)
	}
}

func TestCPUPercentIgnoresBackwardsClock(t *testing.T) {
	if got := cpuPercent(time.Second, 500*time.Millisecond, time.Second); got != 0 {
		t.Errorf("cpuPercent = %v, want 0 on a backwards reading", got)
	}
	if got := cpuPercent(0, time.Second, 0); got != 0 {
		t.Errorf("cpuPercent = %v, want 0 on a zero elapsed", got)
	}
}

func TestHostCPUPercentIdleMachine(t *testing.T) {
	// On Windows, kernel includes idle. An idle second: idle +1s, kernel +1s, user 0.
	got := hostCPUPercent(0, 0, 0, time.Second, time.Second, 0)
	if got != 0 {
		t.Errorf("hostCPUPercent idle = %v, want 0", got)
	}
}

func TestHostCPUPercentFullyBusy(t *testing.T) {
	// No idle growth, kernel and user both grow: fully busy.
	got := hostCPUPercent(0, 0, 0, 0, 500*time.Millisecond, 500*time.Millisecond)
	if got != 100 {
		t.Errorf("hostCPUPercent busy = %v, want 100", got)
	}
}
