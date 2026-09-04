package config

import (
	"bytes"
	"net"
	"strings"
)

const (
	// DefaultAddr is reachable from other machines on the network. The admin
	// UI is still behind login, CSRF, and IP bans.
	DefaultAddr = "0.0.0.0:9527"

	previousLoopbackAddr = "127.0.0.1:9527"
)

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return addr == "127.0.0.1" || addr == "localhost" || addr == "::1"
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// migrateListenAddr upgrades the previous product default (loopback) to listen
// on every interface. An operator-chosen address is left alone.
func migrateListenAddr(data []byte, cfg *Config) ([]byte, bool) {
	if cfg.Server.Addr != previousLoopbackAddr && cfg.Server.Addr != "localhost:9527" {
		return data, false
	}
	cfg.Server.Addr = DefaultAddr
	replacements := [][2]string{
		{`addr: "127.0.0.1:9527"`, `addr: "0.0.0.0:9527"`},
		{`addr: '127.0.0.1:9527'`, `addr: "0.0.0.0:9527"`},
		{`addr: 127.0.0.1:9527`, `addr: 0.0.0.0:9527`},
		{`addr: "localhost:9527"`, `addr: "0.0.0.0:9527"`},
	}
	out := data
	changed := false
	for _, pair := range replacements {
		next := bytes.ReplaceAll(out, []byte(pair[0]), []byte(pair[1]))
		if !bytes.Equal(next, out) {
			out = next
			changed = true
		}
	}
	return out, changed
}
