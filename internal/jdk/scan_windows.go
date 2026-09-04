//go:build windows

package jdk

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scan looks for Java installations in the places Windows installers use.
//
// The alternative is asking the operator to type a path, which is the step
// most likely to be wrong: JAVA_HOME often points at a different major version
// than the one on PATH, and both may differ from what a given app needs.
// Candidates are probed with Detect, so anything that cannot describe itself is
// dropped rather than offered as a broken choice.
func Scan(ctx context.Context) []Info {
	seen := map[string]bool{}
	var found []Info

	for _, exe := range candidateLaunchers() {
		info, err := Resolve(ctx, exe)
		if err != nil {
			continue
		}
		// Different candidate paths routinely resolve to one installation:
		// JAVA_HOME, a vendor directory and the PATH entry are frequently the
		// same JDK reached three ways.
		key := strings.ToLower(filepath.Clean(info.JavaHome))
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, info)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].Major != found[j].Major {
			return found[i].Major > found[j].Major
		}
		return found[i].JavaHome < found[j].JavaHome
	})
	return found
}

func candidateLaunchers() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}

	if home := os.Getenv("JAVA_HOME"); home != "" {
		add(filepath.Join(home, "bin", "java.exe"))
	}
	// PATH is included even though it is often the Oracle stub, because
	// Resolve rewrites it to the real installation behind it.
	out = append(out, "java.exe")

	for _, root := range installRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			add(filepath.Join(root, e.Name(), "bin", "java.exe"))
		}
	}
	return out
}

// installRoots covers the layouts of the distributions actually seen on
// developer and server machines: the vendor-named directories under Program
// Files, plus SDKMAN-style per-user trees.
func installRoots() []string {
	var roots []string
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramW6432"),
	} {
		if base == "" {
			continue
		}
		roots = append(roots,
			filepath.Join(base, "Java"),
			filepath.Join(base, "Eclipse Adoptium"),
			filepath.Join(base, "Amazon Corretto"),
			filepath.Join(base, "Microsoft"),
			filepath.Join(base, "Zulu"),
			filepath.Join(base, "BellSoft"),
			filepath.Join(base, "RedHat"),
			filepath.Join(base, "Semeru"),
			filepath.Join(base, "GraalVM"),
			filepath.Join(base, "SapMachine"),
		)
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		roots = append(roots,
			filepath.Join(userProfile, ".jdks"),
			filepath.Join(userProfile, "scoop", "apps"),
		)
	}
	return roots
}
