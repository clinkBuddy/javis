package supervisor

import (
	"testing"
	"time"
)

func TestWatchdogBackoffGrowsThenCaps(t *testing.T) {
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for i, w := range want {
		if got := watchdogBackoff(i); got != w {
			t.Errorf("watchdogBackoff(%d) = %s, want %s", i, got, w)
		}
	}
}

func TestWatchdogBackoffRejectsNegative(t *testing.T) {
	if got := watchdogBackoff(-3); got != time.Second {
		t.Errorf("watchdogBackoff(-3) = %s, want 1s", got)
	}
}
