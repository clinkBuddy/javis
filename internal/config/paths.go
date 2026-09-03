package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppName is used for the ProgramData folder, the Windows service name and the
// event log source.
const AppName = "JARVIS"

// EnvHome overrides the data root. Useful during development so that a dev
// build never touches the machine-wide ProgramData tree.
const EnvHome = "JARVIS_HOME"

// Paths resolves every on-disk location JARVIS uses at runtime.
//
// Layout:
//
//	<root>/config.yaml
//	<root>/jarvis.db
//	<root>/logs/                    JARVIS' own logs
//	<root>/repo/<app>/<version>/    uploaded jar artifacts, immutable
//	<root>/apps/<app>/current/      active jar
//	<root>/apps/<app>/logs/         redirected stdout/stderr of the java process
//	<root>/apps/<app>/work/         working directory of the java process
//	<root>/apps/<app>/run/          instance.json (pid, create time, uuid)
type Paths struct {
	Root string
}

// DefaultRoot returns %ProgramData%\JARVIS unless JARVIS_HOME is set.
func DefaultRoot() string {
	if v := os.Getenv(EnvHome); v != "" {
		return v
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, AppName)
}

func NewPaths(root string) Paths {
	if root == "" {
		root = DefaultRoot()
	}
	return Paths{Root: root}
}

func (p Paths) ConfigFile() string { return filepath.Join(p.Root, "config.yaml") }
func (p Paths) DBFile() string     { return filepath.Join(p.Root, "jarvis.db") }
func (p Paths) LogDir() string     { return filepath.Join(p.Root, "logs") }
func (p Paths) RepoDir() string    { return filepath.Join(p.Root, "repo") }
func (p Paths) AppsDir() string    { return filepath.Join(p.Root, "apps") }
func (p Paths) TmpDir() string     { return filepath.Join(p.Root, "tmp") }

func (p Paths) AppDir(app string) string        { return filepath.Join(p.AppsDir(), app) }
func (p Paths) AppCurrentDir(app string) string { return filepath.Join(p.AppDir(app), "current") }
func (p Paths) AppLogDir(app string) string     { return filepath.Join(p.AppDir(app), "logs") }
func (p Paths) AppWorkDir(app string) string    { return filepath.Join(p.AppDir(app), "work") }
func (p Paths) AppRunDir(app string) string     { return filepath.Join(p.AppDir(app), "run") }

func (p Paths) ArtifactDir(app, version string) string {
	return filepath.Join(p.RepoDir(), app, version)
}

// EnsureBase creates the directories that must exist before the core starts.
func (p Paths) EnsureBase() error {
	for _, d := range []string{p.Root, p.LogDir(), p.RepoDir(), p.AppsDir(), p.TmpDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// EnsureApp creates the per-application directory tree.
func (p Paths) EnsureApp(app string) error {
	for _, d := range []string{
		p.AppDir(app), p.AppCurrentDir(app), p.AppLogDir(app),
		p.AppWorkDir(app), p.AppRunDir(app),
	} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}
