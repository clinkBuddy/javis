package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxUploadBytes caps a single jar. Spring Boot fat jars are tens of
// megabytes; anything past this is either a mistake or an attempt to fill the
// disk, and both should be refused before the bytes are written.
const MaxUploadBytes = 512 << 20 // 512 MiB

// Stored describes a jar that now lives in the repository.
type Stored struct {
	AppName   string
	Version   string
	FileName  string
	RelPath   string // forward-slash path relative to the repository root
	AbsPath   string
	SizeBytes int64
	SHA256    string
	Manifest  Manifest
}

// safeName allows only characters that are unambiguous in a Windows path and
// in a URL, because these values become directory names and appear in REST
// routes.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateName checks an app or version identifier.
//
// Rejecting rather than sanitising is deliberate: silently rewriting a name
// would make the value in the database differ from the one the operator typed,
// and the directory layout is derived from it.
func ValidateName(kind, value string) error {
	if value == "" {
		return fmt.Errorf("artifact: %s is required", kind)
	}
	if !safeName.MatchString(value) {
		return fmt.Errorf(
			"artifact: %s %q is invalid; use letters, digits, dot, dash or underscore (max 64)",
			kind, value)
	}
	return nil
}

// Repository owns the on-disk jar store rooted at a single directory.
type Repository struct {
	root string
}

func NewRepository(root string) *Repository {
	return &Repository{root: root}
}

// ErrAlreadyExists means this app already has that version.
var ErrAlreadyExists = errors.New("artifact: this version already exists")

// Save streams an upload into the repository.
//
// The bytes go to a temporary file first so the jar can be hashed and its
// manifest verified before anything appears under the repository root. A
// half-written or malformed jar therefore never becomes visible as a
// deployable version, which matters because the launcher trusts whatever it
// finds there.
func (r *Repository) Save(appName, version, fileName string, src io.Reader) (Stored, error) {
	if err := ValidateName("app name", appName); err != nil {
		return Stored{}, err
	}
	if err := ValidateName("version", version); err != nil {
		return Stored{}, err
	}

	fileName = filepath.Base(strings.ReplaceAll(fileName, `\`, "/"))
	if fileName == "" || fileName == "." || fileName == string(filepath.Separator) {
		fileName = appName + "-" + version + ".jar"
	}
	if !strings.EqualFold(filepath.Ext(fileName), ".jar") {
		return Stored{}, errors.New("artifact: upload must be a .jar file")
	}

	targetDir := filepath.Join(r.root, appName, version)
	targetPath := filepath.Join(targetDir, fileName)
	if _, err := os.Stat(targetPath); err == nil {
		return Stored{}, fmt.Errorf("%w: %s %s", ErrAlreadyExists, appName, version)
	}

	tmp, err := r.stageUpload(src)
	if err != nil {
		return Stored{}, err
	}
	defer os.Remove(tmp.path)

	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return Stored{}, fmt.Errorf("artifact: create %s: %w", targetDir, err)
	}
	if err := os.Rename(tmp.path, targetPath); err != nil {
		return Stored{}, fmt.Errorf("artifact: publish %s: %w", targetPath, err)
	}

	return Stored{
		AppName:   appName,
		Version:   version,
		FileName:  fileName,
		RelPath:   path.Join(appName, version, fileName),
		AbsPath:   targetPath,
		SizeBytes: tmp.size,
		SHA256:    tmp.sha256,
		Manifest:  tmp.manifest,
	}, nil
}

type stagedUpload struct {
	path     string
	size     int64
	sha256   string
	manifest Manifest
}

func (r *Repository) stageUpload(src io.Reader) (stagedUpload, error) {
	tmpDir := filepath.Join(r.root, ".incoming")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return stagedUpload{}, fmt.Errorf("artifact: create staging directory: %w", err)
	}

	f, err := os.CreateTemp(tmpDir, "upload-*.jar")
	if err != nil {
		return stagedUpload{}, fmt.Errorf("artifact: create staging file: %w", err)
	}
	tmpPath := f.Name()

	cleanup := func() {
		f.Close()
		os.Remove(tmpPath)
	}

	hasher := sha256.New()
	// LimitReader guards the disk; the +1 lets an oversized upload be detected
	// rather than silently truncated to exactly the limit.
	limited := io.LimitReader(src, MaxUploadBytes+1)
	size, err := io.Copy(io.MultiWriter(f, hasher), limited)
	if err != nil {
		cleanup()
		return stagedUpload{}, fmt.Errorf("artifact: receive upload: %w", err)
	}
	if size > MaxUploadBytes {
		cleanup()
		return stagedUpload{}, fmt.Errorf("artifact: upload exceeds the %d MiB limit", MaxUploadBytes>>20)
	}
	if size == 0 {
		cleanup()
		return stagedUpload{}, errors.New("artifact: upload is empty")
	}

	manifest, err := ReadManifest(f, size)
	if err != nil {
		cleanup()
		return stagedUpload{}, err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return stagedUpload{}, fmt.Errorf("artifact: flush upload: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return stagedUpload{}, fmt.Errorf("artifact: close upload: %w", err)
	}

	return stagedUpload{
		path:     tmpPath,
		size:     size,
		sha256:   hex.EncodeToString(hasher.Sum(nil)),
		manifest: manifest,
	}, nil
}

// AbsPath resolves a stored relative path against the repository root.
func (r *Repository) AbsPath(relPath string) string {
	return filepath.Join(r.root, filepath.FromSlash(relPath))
}

// Verify re-hashes a stored jar and compares it against the recorded digest.
//
// This runs immediately before every launch. A jar that was replaced or
// truncated on disk since upload would otherwise start as if nothing had
// changed, and the resulting failure would look like an application bug rather
// than a corrupted artifact.
func (r *Repository) Verify(relPath, wantSHA256 string) error {
	absPath := r.AbsPath(relPath)
	f, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("artifact: open %s: %w", absPath, err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("artifact: read %s: %w", absPath, err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf(
			"artifact: %s no longer matches its recorded checksum (want %s, got %s)",
			relPath, wantSHA256, got)
	}
	return nil
}

// Remove deletes one version and prunes the app directory when it empties.
func (r *Repository) Remove(appName, version string) error {
	if err := ValidateName("app name", appName); err != nil {
		return err
	}
	if err := ValidateName("version", version); err != nil {
		return err
	}

	dir := filepath.Join(r.root, appName, version)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("artifact: remove %s: %w", dir, err)
	}
	// Ignore the error: a non-empty parent simply stays.
	_ = os.Remove(filepath.Join(r.root, appName))
	return nil
}

// CleanStaging deletes staging files left behind by an interrupted upload.
// Called at startup, since a crash mid-upload cannot clean up after itself.
func (r *Repository) CleanStaging(olderThan time.Duration) error {
	tmpDir := filepath.Join(r.root, ".incoming")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(tmpDir, e.Name()))
	}
	return nil
}
