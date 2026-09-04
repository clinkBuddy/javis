package artifact

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootJarBytes(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("Manifest-Version: 1.0\r\n" +
		"Start-Class: com.example.App\r\n" +
		"Spring-Boot-Version: 3.3.4\r\n\r\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaveStoresJarAndParsesManifest(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)
	jar := bootJarBytes(t)

	stored, err := repo.Save("garage", "0.1.0", "sjkim-garage-0.1.0.jar", bytes.NewReader(jar))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if stored.RelPath != "garage/0.1.0/sjkim-garage-0.1.0.jar" {
		t.Errorf("RelPath = %q", stored.RelPath)
	}
	if stored.SizeBytes != int64(len(jar)) {
		t.Errorf("SizeBytes = %d, want %d", stored.SizeBytes, len(jar))
	}
	sum := sha256.Sum256(jar)
	if stored.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q, want %q", stored.SHA256, hex.EncodeToString(sum[:]))
	}
	if stored.Manifest.StartClass != "com.example.App" {
		t.Errorf("StartClass = %q", stored.Manifest.StartClass)
	}

	onDisk, err := os.ReadFile(stored.AbsPath)
	if err != nil {
		t.Fatalf("read stored jar: %v", err)
	}
	if !bytes.Equal(onDisk, jar) {
		t.Error("stored bytes differ from the upload")
	}
}

func TestSaveRejectsDuplicateVersion(t *testing.T) {
	repo := NewRepository(t.TempDir())
	jar := bootJarBytes(t)

	if _, err := repo.Save("garage", "0.1.0", "app.jar", bytes.NewReader(jar)); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	_, err := repo.Save("garage", "0.1.0", "app.jar", bytes.NewReader(jar))
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("err = %v, want ErrAlreadyExists", err)
	}
}

// A rejected upload must not leave a directory behind, or the version would
// look taken and could never be uploaded again.
func TestSaveLeavesNothingBehindWhenUploadIsNotAJar(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)

	_, err := repo.Save("garage", "0.1.0", "app.jar", strings.NewReader("not a zip"))
	if !errors.Is(err, ErrNotAJar) {
		t.Fatalf("err = %v, want ErrNotAJar", err)
	}

	if _, err := os.Stat(filepath.Join(root, "garage")); !os.IsNotExist(err) {
		t.Error("a failed upload created the app directory")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".incoming"))
	if err == nil && len(entries) != 0 {
		t.Errorf("%d staging file(s) left behind", len(entries))
	}
}

func TestSaveRejectsEmptyUpload(t *testing.T) {
	repo := NewRepository(t.TempDir())

	_, err := repo.Save("garage", "0.1.0", "app.jar", strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want an empty-upload error", err)
	}
}

// The client controls the filename, so a traversal attempt must not be able to
// place a file outside the app's version directory.
func TestSaveStripsPathFromClientFilename(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)

	stored, err := repo.Save("garage", "0.1.0", `..\..\..\evil.jar`, bytes.NewReader(bootJarBytes(t)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if stored.FileName != "evil.jar" {
		t.Errorf("FileName = %q, want evil.jar", stored.FileName)
	}

	want := filepath.Join(root, "garage", "0.1.0", "evil.jar")
	if stored.AbsPath != want {
		t.Errorf("AbsPath = %q, want %q", stored.AbsPath, want)
	}
}

func TestSaveRejectsNonJarExtension(t *testing.T) {
	repo := NewRepository(t.TempDir())

	_, err := repo.Save("garage", "0.1.0", "app.zip", bytes.NewReader(bootJarBytes(t)))
	if err == nil || !strings.Contains(err.Error(), ".jar") {
		t.Errorf("err = %v, want a .jar extension error", err)
	}
}

func TestValidateNameRejectsPathTricks(t *testing.T) {
	bad := []string{"", ".", "..", "../etc", `a\b`, "a/b", "with space", "colon:name",
		strings.Repeat("x", 65), "-leading-dash"}
	for _, v := range bad {
		if err := ValidateName("app name", v); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", v)
		}
	}

	good := []string{"garage", "sjkim-garage", "app_1", "v1.2.3", "A"}
	for _, v := range good {
		if err := ValidateName("app name", v); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", v, err)
		}
	}
}

func TestVerifyDetectsTamperedJar(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)

	stored, err := repo.Save("garage", "0.1.0", "app.jar", bytes.NewReader(bootJarBytes(t)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Verify(stored.RelPath, stored.SHA256); err != nil {
		t.Fatalf("Verify on an untouched jar: %v", err)
	}

	// Simulate a build overwriting the stored jar in place.
	if err := os.WriteFile(stored.AbsPath, []byte("replaced"), 0o640); err != nil {
		t.Fatal(err)
	}
	err = repo.Verify(stored.RelPath, stored.SHA256)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want a checksum mismatch", err)
	}
}

func TestRemoveDeletesVersionAndPrunesEmptyApp(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)

	if _, err := repo.Save("garage", "0.1.0", "app.jar", bytes.NewReader(bootJarBytes(t))); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Remove("garage", "0.1.0"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "garage")); !os.IsNotExist(err) {
		t.Error("app directory survived removal of its only version")
	}
}

func TestRemoveKeepsAppDirectoryWithRemainingVersions(t *testing.T) {
	root := t.TempDir()
	repo := NewRepository(root)
	jar := bootJarBytes(t)

	for _, v := range []string{"0.1.0", "0.2.0"} {
		if _, err := repo.Save("garage", v, "app.jar", bytes.NewReader(jar)); err != nil {
			t.Fatalf("Save %s: %v", v, err)
		}
	}
	if err := repo.Remove("garage", "0.1.0"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "garage", "0.2.0", "app.jar")); err != nil {
		t.Errorf("remaining version was removed too: %v", err)
	}
}
