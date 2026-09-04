// Package artifact stores uploaded jars and reads what it can out of them.
package artifact

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Manifest is the subset of META-INF/MANIFEST.MF that JARVIS cares about.
//
// Reading it at upload time means the UI can show what a jar actually is
// before anyone tries to run it, and it lets JARVIS warn when a jar built for
// Java 21 is about to be launched on a Java 17 runtime.
type Manifest struct {
	MainClass             string            `json:"mainClass,omitempty"`
	StartClass            string            `json:"startClass,omitempty"`
	SpringBootVersion     string            `json:"springBootVersion,omitempty"`
	ImplementationTitle   string            `json:"implementationTitle,omitempty"`
	ImplementationVersion string            `json:"implementationVersion,omitempty"`
	BuildJDKSpec          string            `json:"buildJdkSpec,omitempty"`
	CreatedBy             string            `json:"createdBy,omitempty"`
	All                   map[string]string `json:"all,omitempty"`
}

// IsSpringBootJar reports whether this looks like an executable Spring Boot
// fat jar rather than a plain library.
func (m Manifest) IsSpringBootJar() bool {
	return m.StartClass != "" || strings.Contains(m.MainClass, "springframework.boot.loader")
}

// ErrNotAJar is returned when the uploaded file is not a readable zip.
var ErrNotAJar = errors.New("artifact: file is not a readable jar (zip) archive")

// ReadManifest opens the jar and parses its manifest.
func ReadManifest(r io.ReaderAt, size int64) (Manifest, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrNotAJar, err)
	}

	f, err := findManifest(zr)
	if err != nil {
		return Manifest{}, err
	}

	rc, err := f.Open()
	if err != nil {
		return Manifest{}, fmt.Errorf("artifact: open manifest: %w", err)
	}
	defer rc.Close()

	attrs, err := parseManifestAttributes(rc)
	if err != nil {
		return Manifest{}, err
	}

	return Manifest{
		MainClass:             attrs["Main-Class"],
		StartClass:            attrs["Start-Class"],
		SpringBootVersion:     attrs["Spring-Boot-Version"],
		ImplementationTitle:   attrs["Implementation-Title"],
		ImplementationVersion: attrs["Implementation-Version"],
		BuildJDKSpec:          firstNonEmpty(attrs["Build-Jdk-Spec"], attrs["Build-Jdk"]),
		CreatedBy:             attrs["Created-By"],
		All:                   attrs,
	}, nil
}

func findManifest(zr *zip.Reader) (*zip.File, error) {
	for _, f := range zr.File {
		// The spec fixes the name, but casing has been seen to vary in jars
		// produced by hand-rolled tooling.
		if strings.EqualFold(f.Name, "META-INF/MANIFEST.MF") {
			return f, nil
		}
	}
	return nil, errors.New("artifact: jar has no META-INF/MANIFEST.MF")
}

// parseManifestAttributes reads the main section of a manifest.
//
// The format is not quite key-value: lines are limited to 72 bytes and long
// values continue on the next line prefixed by a single space, so naive
// line-splitting truncates class names and version strings. Parsing stops at
// the first blank line because everything after it describes individual
// entries, not the archive.
func parseManifestAttributes(r io.Reader) (map[string]string, error) {
	attrs := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), 1024*1024)

	var currentKey string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			break
		}

		if strings.HasPrefix(line, " ") {
			if currentKey != "" {
				attrs[currentKey] += line[1:]
			}
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			currentKey = ""
			continue
		}
		currentKey = strings.TrimSpace(key)
		attrs[currentKey] = strings.TrimSpace(value)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("artifact: read manifest: %w", err)
	}
	return attrs, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
