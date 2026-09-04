package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// DataRoot is normally empty, meaning "use the directory this config file
	// lives in". It exists so an operator can relocate the jar repository to a
	// different volume.
	DataRoot string `yaml:"dataRoot,omitempty"`

	Server ServerConfig `yaml:"server"`
	Log    LogConfig    `yaml:"log"`
}

type ServerConfig struct {
	Addr string    `yaml:"addr"`
	TLS  TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	// Enabled turns on HTTPS. When CertFile/KeyFile are empty a self-signed
	// certificate is generated into the data root on first start.
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"certFile,omitempty"`
	KeyFile  string `yaml:"keyFile,omitempty"`
}

type LogConfig struct {
	Level      string `yaml:"level"`
	MaxSizeMB  int    `yaml:"maxSizeMB"`
	MaxBackups int    `yaml:"maxBackups"`
	MaxAgeDays int    `yaml:"maxAgeDays"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{Addr: DefaultAddr},
		Log: LogConfig{
			Level:      "info",
			MaxSizeMB:  20,
			MaxBackups: 10,
			MaxAgeDays: 30,
		},
	}
}

// Load reads the config file, writing a commented default file first if none
// exists. root is the data root that was resolved from JARVIS_HOME or
// ProgramData; the returned Paths honours an explicit dataRoot override.
func Load(root string) (*Config, Paths, error) {
	p := NewPaths(root)
	if err := p.EnsureBase(); err != nil {
		return nil, p, err
	}

	cfg := Default()
	data, err := os.ReadFile(p.ConfigFile())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := writeDefault(p.ConfigFile()); err != nil {
			return nil, p, err
		}
	case err != nil:
		return nil, p, fmt.Errorf("read %s: %w", p.ConfigFile(), err)
	default:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, p, fmt.Errorf("parse %s: %w", p.ConfigFile(), err)
		}
		if rewritten, ok := migrateListenAddr(data, cfg); ok {
			if err := writeFileAtomic(p.ConfigFile(), rewritten); err != nil {
				return nil, p, fmt.Errorf("update listen address in %s: %w", p.ConfigFile(), err)
			}
		}
	}

	cfg.applyDefaults()
	if cfg.DataRoot != "" {
		p = NewPaths(cfg.DataRoot)
		if err := p.EnsureBase(); err != nil {
			return nil, p, err
		}
	}
	return cfg, p, nil
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Server.Addr == "" {
		c.Server.Addr = d.Server.Addr
	}
	if c.Log.Level == "" {
		c.Log.Level = d.Log.Level
	}
	if c.Log.MaxSizeMB <= 0 {
		c.Log.MaxSizeMB = d.Log.MaxSizeMB
	}
	if c.Log.MaxBackups <= 0 {
		c.Log.MaxBackups = d.Log.MaxBackups
	}
	if c.Log.MaxAgeDays <= 0 {
		c.Log.MaxAgeDays = d.Log.MaxAgeDays
	}
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

const defaultConfigYAML = `# JARVIS configuration
#
# Changes take effect on the next service restart.

server:
  # All interfaces. Restrict to 127.0.0.1:9527 to keep the UI on this machine only.
  addr: "0.0.0.0:9527"
  tls:
    enabled: false
    # Leave empty to generate a self-signed certificate on first start.
    certFile: ""
    keyFile: ""

log:
  # debug | info | warn | error
  level: "info"
  maxSizeMB: 20
  maxBackups: 10
  maxAgeDays: 30

# Relocate the jar repository and per-app data to another volume.
# Leave empty to keep everything next to this file.
# dataRoot: "D:\\jarvis-data"
`

func writeDefault(path string) error {
	return writeFileAtomic(path, []byte(defaultConfigYAML))
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
