package metrics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var actuatorClient = &http.Client{Timeout: 700 * time.Millisecond}

func pullActuator(ctx context.Context, shutdownURL string, listen []int) (ex []HTTPExchange, kind string, hardFail bool) {
	bases := actuatorBases(shutdownURL, listen)
	if len(bases) == 0 {
		return nil, "", false
	}

	for _, base := range bases {
		if got, ok := getHTTPExchanges(ctx, base+"/actuator/httpexchanges"); ok {
			return got, "httpexchanges", false
		}
		if got, ok := getHTTPTrace(ctx, base+"/actuator/httptrace"); ok {
			return got, "httptrace", false
		}
	}
	// Endpoints exist on many Boot apps but are not exposed. Cool down so
	// we do not pollute the app log with 404s every five seconds.
	return nil, "none", true
}

func actuatorBases(shutdownURL string, listen []int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimRight(s, "/")
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if u, err := url.Parse(strings.TrimSpace(shutdownURL)); err == nil && u.Scheme != "" && u.Host != "" {
		path := strings.TrimSuffix(u.Path, "/")
		if i := strings.Index(path, "/actuator"); i >= 0 {
			path = path[:i]
		}
		u.Path = path
		u.RawQuery = ""
		u.Fragment = ""
		add(u.String())
	}
	for _, p := range listen {
		if p > 0 {
			add("http://127.0.0.1:" + itoa(p))
		}
	}
	return out
}

func getHTTPExchanges(ctx context.Context, endpoint string) ([]HTTPExchange, bool) {
	body, ok := getJSON(ctx, endpoint)
	if !ok {
		return nil, false
	}
	var wrap struct {
		Exchanges []struct {
			Timestamp time.Time `json:"timestamp"`
			TimeTaken string    `json:"timeTaken"`
			Request   struct {
				Method        string `json:"method"`
				URI           string `json:"uri"`
				RemoteAddress string `json:"remoteAddress"`
			} `json:"request"`
			Response struct {
				Status int `json:"status"`
			} `json:"response"`
		} `json:"exchanges"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, false
	}
	if wrap.Exchanges == nil {
		return nil, false
	}
	out := make([]HTTPExchange, 0, len(wrap.Exchanges))
	for _, e := range wrap.Exchanges {
		path := e.Request.URI
		if u, err := url.Parse(e.Request.URI); err == nil && u.Path != "" {
			path = u.Path
		}
		out = append(out, HTTPExchange{
			At:     e.Timestamp,
			Method: strings.ToUpper(e.Request.Method),
			Path:   sanitizePath(path),
			Status: e.Response.Status,
			Ms:     parseISODurationMs(e.TimeTaken),
			Remote: e.Request.RemoteAddress,
		})
	}
	return out, true
}

func getHTTPTrace(ctx context.Context, endpoint string) ([]HTTPExchange, bool) {
	body, ok := getJSON(ctx, endpoint)
	if !ok {
		return nil, false
	}
	var wrap struct {
		Traces []struct {
			Timestamp time.Time `json:"timestamp"`
			Info      struct {
				Method    string `json:"method"`
				Path      string `json:"path"`
				TimeTaken any    `json:"timeTaken"`
				Headers   struct {
					Response map[string][]string `json:"response"`
				} `json:"headers"`
			} `json:"info"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, false
	}
	if wrap.Traces == nil {
		return nil, false
	}
	out := make([]HTTPExchange, 0, len(wrap.Traces))
	for _, t := range wrap.Traces {
		status := 0
		if ss := t.Info.Headers.Response["status"]; len(ss) > 0 {
			status = atoi(ss[0])
		}
		out = append(out, HTTPExchange{
			At: t.Timestamp, Method: strings.ToUpper(t.Info.Method),
			Path: sanitizePath(t.Info.Path), Status: status,
			Ms: anyDurationMs(t.Info.TimeTaken),
		})
	}
	return out, true
}

func getJSON(ctx context.Context, endpoint string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "application/json")
	res, err := actuatorClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}

func parseISODurationMs(s string) float64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "PT")
	s = strings.TrimSuffix(s, "S")
	return atof(s) * 1000
}

func anyDurationMs(v any) float64 {
	switch t := v.(type) {
	case string:
		if strings.Contains(strings.ToUpper(t), "PT") {
			return parseISODurationMs(t)
		}
		return parseDurMs(t)
	case float64:
		// Boot 2 httptrace timeTaken is milliseconds.
		return t
	case int:
		return float64(t)
	default:
		return 0
	}
}
