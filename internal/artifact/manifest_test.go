package artifact

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// jarWithManifest builds an in-memory jar so the tests exercise the real zip
// reader rather than a stubbed one.
func jarWithManifest(t *testing.T, manifest string) *bytes.Reader {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if manifest != "" {
		w, err := zw.Create("META-INF/MANIFEST.MF")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(manifest)); err != nil {
			t.Fatal(err)
		}
	}
	if w, err := zw.Create("BOOT-INF/classes/application.yml"); err == nil {
		_, _ = w.Write([]byte("server:\n  port: 8080\n"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestReadManifestSpringBootJar(t *testing.T) {
	// Byte-for-byte shape of a Gradle bootJar manifest, including CRLF.
	manifest := "Manifest-Version: 1.0\r\n" +
		"Main-Class: org.springframework.boot.loader.launch.JarLauncher\r\n" +
		"Start-Class: com.sjkim.garage.GarageApplication\r\n" +
		"Spring-Boot-Version: 3.3.4\r\n" +
		"Implementation-Title: sjkim-garage\r\n" +
		"Implementation-Version: 0.1.0\r\n" +
		"Build-Jdk-Spec: 21\r\n" +
		"\r\n" +
		"Name: BOOT-INF/classes/\r\n" +
		"Some-Entry-Attribute: ignored\r\n"

	got, err := ReadManifest(jarWithManifest(t, manifest), int64(len(manifest)+1024))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	if got.StartClass != "com.sjkim.garage.GarageApplication" {
		t.Errorf("StartClass = %q", got.StartClass)
	}
	if got.SpringBootVersion != "3.3.4" {
		t.Errorf("SpringBootVersion = %q", got.SpringBootVersion)
	}
	if got.ImplementationVersion != "0.1.0" {
		t.Errorf("ImplementationVersion = %q", got.ImplementationVersion)
	}
	if got.BuildJDKSpec != "21" {
		t.Errorf("BuildJDKSpec = %q", got.BuildJDKSpec)
	}
	if !got.IsSpringBootJar() {
		t.Error("IsSpringBootJar() = false for a bootJar manifest")
	}
	// Per-entry sections come after the blank line and must not leak into the
	// archive-level attributes.
	if _, leaked := got.All["Some-Entry-Attribute"]; leaked {
		t.Error("per-entry attribute leaked into the main section")
	}
}

// Manifest lines wrap at 72 bytes with a single leading space on the
// continuation. A parser that splits on newlines alone silently truncates
// long class names, which would then be reported as the wrong main class.
func TestReadManifestJoinsWrappedLines(t *testing.T) {
	wantClass := "com.example.deeply.nested.package.name.that.is.long.ApplicationEntryPoint"
	manifest := "Manifest-Version: 1.0\r\n" +
		"Start-Class: com.example.deeply.nested.package.name.that.is.long.Applic\r\n" +
		" ationEntryPoint\r\n" +
		"\r\n"

	got, err := ReadManifest(jarWithManifest(t, manifest), int64(len(manifest)+1024))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.StartClass != wantClass {
		t.Errorf("StartClass = %q, want %q", got.StartClass, wantClass)
	}
}

func TestReadManifestPlainLibraryIsNotBootJar(t *testing.T) {
	manifest := "Manifest-Version: 1.0\r\nCreated-By: Maven\r\n\r\n"

	got, err := ReadManifest(jarWithManifest(t, manifest), int64(len(manifest)+1024))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.IsSpringBootJar() {
		t.Error("IsSpringBootJar() = true for a library jar")
	}
}

func TestReadManifestRejectsNonZip(t *testing.T) {
	notAJar := bytes.NewReader([]byte("this is a text file, not an archive"))

	_, err := ReadManifest(notAJar, int64(notAJar.Len()))
	if !errors.Is(err, ErrNotAJar) {
		t.Errorf("err = %v, want ErrNotAJar", err)
	}
}

func TestReadManifestRejectsJarWithoutManifest(t *testing.T) {
	r := jarWithManifest(t, "")

	_, err := ReadManifest(r, int64(r.Len()))
	if err == nil || !strings.Contains(err.Error(), "MANIFEST.MF") {
		t.Errorf("err = %v, want a missing-manifest error", err)
	}
}
