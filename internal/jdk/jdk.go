// Package jdk identifies a Java installation from its launcher.
package jdk

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const detectTimeout = 15 * time.Second

type Info struct {
	// JavaExe is the launcher to actually run. Resolve rewrites it to the one
	// inside JavaHome, which is not necessarily the path it was asked about.
	JavaExe  string `json:"javaExe"`
	JavaHome string `json:"javaHome"`
	Vendor   string `json:"vendor"`
	Version  string `json:"version"`
	VMName   string `json:"vmName"`
	Major    int    `json:"major"`
}

func (i Info) String() string {
	return fmt.Sprintf("%s %s (%s)", i.Vendor, i.Version, i.JavaHome)
}

// Detect asks a launcher to describe itself.
//
// -XshowSettings:properties is used in preference to parsing `java -version`
// because it reports java.home, and java.home is the only reliable way to find
// the real installation behind a launcher that is a stub, a symlink or a shim.
func Detect(ctx context.Context, javaExe string) (Info, error) {
	if javaExe == "" {
		return Info{}, fmt.Errorf("jdk: launcher path is required")
	}

	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, javaExe, "-XshowSettings:properties", "-version")
	var out bytes.Buffer
	// The settings dump goes to stderr, the version banner to stdout, and
	// which is which has varied between releases. Read both.
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return Info{}, fmt.Errorf("jdk: run %s: %w: %s",
			javaExe, err, strings.TrimSpace(out.String()))
	}

	props := parseProperties(out.String())
	info := Info{
		JavaExe:  javaExe,
		JavaHome: props["java.home"],
		Vendor:   props["java.vendor"],
		Version:  props["java.version"],
		VMName:   props["java.vm.name"],
	}
	if info.JavaHome == "" {
		return Info{}, fmt.Errorf("jdk: %s did not report java.home", javaExe)
	}
	info.Major = majorVersion(info.Version)
	return info, nil
}

// Resolve detects the installation and returns the launcher inside its own
// java.home.
//
// This matters more than it looks. On a machine with an Oracle installer,
// `java` on PATH is C:\Program Files\Common Files\Oracle\Java\javapath\java.exe,
// a stub that starts the real JVM as a child process and forwards its
// arguments. Launching through it leaves JARVIS holding the PID of a 10 MB
// shim while the application it is supposed to be watching runs in a different
// process: resource readings would be meaningless and identity markers would
// match two processes instead of one. Resolving to java.home eliminates the
// extra process entirely.
func Resolve(ctx context.Context, javaExe string) (Info, error) {
	info, err := Detect(ctx, javaExe)
	if err != nil {
		return Info{}, err
	}

	candidate := filepath.Join(info.JavaHome, "bin", launcherName())
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		info.JavaExe = candidate
	}
	return info, nil
}

// Redirected reports whether Resolve had to point somewhere else, which is
// worth logging so an operator can see why the configured path is not the one
// being run.
func (i Info) Redirected(requested string) bool {
	return !strings.EqualFold(filepath.Clean(requested), filepath.Clean(i.JavaExe))
}

func launcherName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// parseProperties reads the "  key = value" lines of a settings dump. Values
// that continue onto following lines (class paths, library paths) are of no
// interest here and are skipped by requiring the separator.
func parseProperties(s string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		props[key] = strings.TrimSpace(value)
	}
	return props
}

// majorVersion turns a java.version string into its feature release number,
// handling both the modern form (17.0.12 -> 17) and the legacy one
// (1.8.0_402 -> 8).
func majorVersion(version string) int {
	parts := strings.FieldsFunc(version, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '+'
	})
	if len(parts) == 0 {
		return 0
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if first == 1 && len(parts) > 1 {
		second, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0
		}
		return second
	}
	return first
}
