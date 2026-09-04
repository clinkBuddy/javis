//go:build windows

package tray

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/winsvc"
)

// Controller starts and stops the installed Windows service from the tray.
// Closing the tray never stops the service.
type Controller struct {
	home string
}

func NewController(home string) *Controller {
	return &Controller{home: home}
}

func (c *Controller) Running() bool {
	st, err := winsvc.Status()
	return err == nil && st == svc.Running
}

func (c *Controller) StatusText() string {
	st, err := winsvc.Status()
	if err != nil {
		return "JARVIS — 서비스 없음"
	}
	if st == svc.Running {
		return "JARVIS — 서비스 실행 중"
	}
	return "JARVIS — 서비스 중지됨"
}

// Start starts the Windows service and waits until the admin UI answers.
func (c *Controller) Start() error {
	if err := winsvc.StartService(); err != nil {
		return fmt.Errorf("서비스를 시작하지 못했습니다: %w", err)
	}
	if err := waitUntil(c.reachable, 20*time.Second); err != nil {
		return err
	}
	return nil
}

// Stop stops the Windows service. Managed java processes are left alone.
func (c *Controller) Stop() error {
	if err := winsvc.StopService(); err != nil {
		return fmt.Errorf("서비스를 중지하지 못했습니다: %w", err)
	}
	return nil
}

// Close is called when the tray exits. The service keeps running.
func (c *Controller) Close() {}

func (c *Controller) AdminURL() string {
	cfg, _, err := config.Load(c.home)
	if err != nil {
		return "http://127.0.0.1:9527/"
	}
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	addr := cfg.Server.Addr
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return fmt.Sprintf("%s://%s/", scheme, addr)
}

func (c *Controller) reachable() bool {
	url := strings.TrimRight(c.AdminURL(), "/") + "/api/v1/health"
	client := &http.Client{Timeout: 800 * time.Millisecond}
	res, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode < 500
}

func waitUntil(ok func() bool, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("서버가 준비되지 않았습니다")
}
