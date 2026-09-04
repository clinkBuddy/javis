package config

import (
	"testing"
)

func TestMigrateListenAddrUpgradesLoopbackDefault(t *testing.T) {
	in := []byte("server:\n  addr: \"127.0.0.1:9527\"\n")
	cfg := &Config{Server: ServerConfig{Addr: previousLoopbackAddr}}
	out, changed := migrateListenAddr(in, cfg)
	if !changed {
		t.Fatal("expected the default loopback address to be rewritten")
	}
	if cfg.Server.Addr != DefaultAddr {
		t.Fatalf("addr = %q, want %q", cfg.Server.Addr, DefaultAddr)
	}
	if got := string(out); got != "server:\n  addr: \"0.0.0.0:9527\"\n" {
		t.Fatalf("rewritten yaml = %q", got)
	}
}

func TestMigrateListenAddrLeavesCustomAddress(t *testing.T) {
	in := []byte("server:\n  addr: \"192.168.1.10:9527\"\n")
	cfg := &Config{Server: ServerConfig{Addr: "192.168.1.10:9527"}}
	out, changed := migrateListenAddr(in, cfg)
	if changed {
		t.Fatalf("rewrote a custom address: %s", out)
	}
	if cfg.Server.Addr != "192.168.1.10:9527" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
}

func TestIsLoopbackListenAddr(t *testing.T) {
	if !isLoopbackListenAddr("127.0.0.1:9527") {
		t.Fatal("loopback should be detected")
	}
	if isLoopbackListenAddr("0.0.0.0:9527") {
		t.Fatal("unspecified address is not loopback")
	}
}
