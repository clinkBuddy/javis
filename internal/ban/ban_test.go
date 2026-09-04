package ban

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:9527":        "127.0.0.1",
		"10.0.0.8":              "10.0.0.8",
		"[::1]:443":             "::1",
		"::1":                   "::1",
		"::ffff:127.0.0.1":      "127.0.0.1",
		"[::ffff:192.0.2.1]:80": "192.0.2.1",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExemptLoopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "127.0.0.1:9", "[::1]:9"} {
		if !Exempt(ip) {
			t.Errorf("%q should be exempt", ip)
		}
	}
	if Exempt("8.8.8.8") {
		t.Error("a public address was treated as loopback")
	}
}

func TestProbe(t *testing.T) {
	ok := []string{"/", "/index.html", "/assets/index.js", "/api/v1/health", "/api/v1/auth/login", "/favicon.ico"}
	for _, p := range ok {
		if Probe(p) {
			t.Errorf("Probe(%q) = true, want false", p)
		}
	}
	bad := []string{"/.env", "/wp-admin/index.php", "/phpmyadmin", "/backup.sql.php", "/.git/config"}
	for _, p := range bad {
		if !Probe(p) {
			t.Errorf("Probe(%q) = false, want true", p)
		}
	}
}
