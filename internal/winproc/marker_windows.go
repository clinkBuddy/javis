//go:build windows

package winproc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Every managed process carries two JVM system properties that serve purely as
// identification. They cost nothing at runtime and they are visible in the
// command line, which makes them readable from outside the process.
//
// jarvis.app is stable across restarts and answers "which application is
// this". jarvis.instance is unique per launch and answers "is this the exact
// process I started", which is what allows a stale record to be rejected
// instead of being matched against a recycled PID.
const (
	AppProperty      = "jarvis.app"
	InstanceProperty = "jarvis.instance"
)

func AppMarker(app string) string {
	return fmt.Sprintf("-D%s=%s", AppProperty, app)
}

func InstanceMarker(instanceID string) string {
	return fmt.Sprintf("-D%s=%s", InstanceProperty, instanceID)
}

// NewInstanceID returns a random identifier for one launch.
func NewInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("winproc: generate instance id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// FindInstance locates the process for a specific launch.
//
// More than one process can carry the same instance marker when java was
// started through a launcher stub that re-executes the real JVM with the same
// arguments. In that chain the JVM is the leaf, so the match that is nobody's
// parent is the process worth watching. Resolving the JDK before launching
// avoids the chain in the first place; this is the safety net for processes
// adopted from an older configuration.
func FindInstance(instanceID string) (Match, error) {
	matches, err := FindByMarker(InstanceMarker(instanceID))
	if err != nil {
		return Match{}, err
	}
	if len(matches) == 0 {
		return Match{}, ErrNotRunning
	}

	leaves := leafMatches(matches)
	switch len(leaves) {
	case 1:
		return leaves[0], nil
	default:
		return Match{}, fmt.Errorf(
			"winproc: instance %s is claimed by %d unrelated processes",
			instanceID, len(leaves))
	}
}

// leafMatches keeps the matches that are not the parent of another match.
func leafMatches(matches []Match) []Match {
	isParent := make(map[uint32]bool, len(matches))
	for _, m := range matches {
		isParent[m.ParentPID] = true
	}

	leaves := make([]Match, 0, len(matches))
	for _, m := range matches {
		if !isParent[m.PID] {
			leaves = append(leaves, m)
		}
	}
	return leaves
}

// FindApp returns every running process launched for an application. Normally
// there is at most one; extras are orphans from a previous JARVIS lifetime.
func FindApp(app string) ([]Match, error) {
	return FindByMarker(AppMarker(app))
}
