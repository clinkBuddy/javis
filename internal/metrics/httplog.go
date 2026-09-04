package metrics

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reQuoted = regexp.MustCompile(
		`(?i)"(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+([^"?\s]+)[^"]*"\s+(\d{3})(?:\s+(\d+))?(?:\s+(\d+(?:\.\d+)?)(ms)?)?`)
	rePlain = regexp.MustCompile(
		`(?i)\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/[^\s"]*)\s+(\d{3})(?:\s+(\d+(?:\.\d+)?)ms)?`)
	reKV = regexp.MustCompile(
		`(?i)method=([A-Z]+)\s+.*?path=(\S+)\s+.*?status=(\d{3})\s+.*?dur=([0-9.]+(?:ms|s|µs|us)?)`)
	reSpringMapped = regexp.MustCompile(
		`(?i)\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+"(/[^"]*)"`)
)

func parseHTTPLines(lines []string) []HTTPExchange {
	now := time.Now()
	out := make([]HTTPExchange, 0, len(lines))
	for _, line := range lines {
		if e, ok := parseHTTPLine(line, now); ok {
			out = append(out, e)
		}
	}
	return out
}

func parseHTTPLine(line string, now time.Time) (HTTPExchange, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return HTTPExchange{}, false
	}

	if m := reKV.FindStringSubmatch(line); m != nil {
		return HTTPExchange{
			At: now, Method: strings.ToUpper(m[1]), Path: sanitizePath(m[2]),
			Status: atoi(m[3]), Ms: parseDurMs(m[4]),
		}, true
	}
	if m := reQuoted.FindStringSubmatch(line); m != nil {
		ms := 0.0
		if m[6] == "ms" && m[5] != "" {
			ms = atof(m[5])
		} else if m[5] != "" {
			ms = atof(m[5])
		}
		return HTTPExchange{
			At: now, Method: strings.ToUpper(m[1]), Path: sanitizePath(m[2]),
			Status: atoi(m[3]), Ms: ms,
		}, true
	}
	if m := rePlain.FindStringSubmatch(line); m != nil {
		return HTTPExchange{
			At: now, Method: strings.ToUpper(m[1]), Path: sanitizePath(m[2]),
			Status: atoi(m[3]), Ms: atof(m[4]),
		}, true
	}
	if m := reSpringMapped.FindStringSubmatch(line); m != nil && strings.Contains(line, "parameters=") {
		return HTTPExchange{
			At: now, Method: strings.ToUpper(m[1]), Path: sanitizePath(m[2]),
		}, true
	}
	return HTTPExchange{}, false
}

func sanitizePath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}
	return p
}

func parseDurMs(s string) float64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "µs") || strings.HasSuffix(s, "us") {
		s = strings.TrimSuffix(strings.TrimSuffix(s, "µs"), "us")
		return atof(s) / 1000
	}
	if strings.HasSuffix(s, "ms") {
		return atof(strings.TrimSuffix(s, "ms"))
	}
	if strings.HasSuffix(s, "s") {
		return atof(strings.TrimSuffix(s, "s")) * 1000
	}
	return atof(s)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atof(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
