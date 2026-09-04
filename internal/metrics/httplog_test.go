package metrics

import (
	"testing"
	"time"
)

func TestParseHTTPLine(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		line   string
		method string
		path   string
		status int
		ms     float64
	}{
		{`127.0.0.1 - - [04/Sep/2026:12:00:00 +0900] "GET /api/orders HTTP/1.1" 200 512 18`, "GET", "/api/orders", 200, 18},
		{`GET /health 200 3ms`, "GET", "/health", 200, 3},
		{`time=... method=GET path=/api/v1/apps status=200 dur=16ms ip=127.0.0.1`, "GET", "/api/v1/apps", 200, 16},
		{`Mapped "{[GET /hello]}"` + "", "", "", 0, 0},
		{`o.s.web.servlet.DispatcherServlet : GET "/api/pay", parameters={}`, "GET", "/api/pay", 0, 0},
	}
	for _, tc := range cases {
		e, ok := parseHTTPLine(tc.line, now)
		if tc.method == "" {
			if ok {
				t.Errorf("parse %q: got %+v, want miss", tc.line, e)
			}
			continue
		}
		if !ok {
			t.Errorf("parse %q: missed", tc.line)
			continue
		}
		if e.Method != tc.method || e.Path != tc.path || e.Status != tc.status {
			t.Errorf("parse %q: got %s %s %d, want %s %s %d",
				tc.line, e.Method, e.Path, e.Status, tc.method, tc.path, tc.status)
		}
		if tc.ms > 0 && e.Ms != tc.ms {
			t.Errorf("parse %q: ms=%v want %v", tc.line, e.Ms, tc.ms)
		}
	}
}

func TestSanitizePathStripsQuery(t *testing.T) {
	if got := sanitizePath("/x?y=1"); got != "/x" {
		t.Fatalf("got %q", got)
	}
}

func TestActuatorBases(t *testing.T) {
	got := actuatorBases("http://127.0.0.1:8080/actuator/shutdown", []int{8080, 0})
	if len(got) != 1 || got[0] != "http://127.0.0.1:8080" {
		t.Fatalf("got %v", got)
	}
}
