//go:build windows

package winproc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
)

// StopMethod records which tier actually stopped the process. It is worth
// surfacing to operators: a process that routinely needs the last tier is not
// shutting down cleanly and will keep losing in-flight work.
type StopMethod string

const (
	StoppedByEndpoint  StopMethod = "shutdown-endpoint"
	StoppedByCtrlC     StopMethod = "ctrl-c"
	StoppedByTerminate StopMethod = "terminate"
)

const (
	defaultGrace    = 30 * time.Second
	terminateGrace  = 10 * time.Second
	endpointTimeout = 5 * time.Second
)

type StopOptions struct {
	// ShutdownURL is an application endpoint that triggers an orderly
	// shutdown, typically Spring Boot Actuator's /actuator/shutdown. Empty
	// skips the tier.
	ShutdownURL     string
	ShutdownHeaders map[string]string

	// Grace is how long each graceful tier is given before escalating.
	Grace time.Duration

	Log *slog.Logger
}

// Stop shuts the process down, escalating only as far as it has to.
//
// Tier 1 asks the application to stop itself, which is the cleanest option
// because the app decides when it is safe. Tier 2 raises a console CTRL+C,
// which the JVM turns into SIGINT and answers by running shutdown hooks; this
// works for any java process without requiring it to expose an endpoint.
// Tier 3 terminates, which cannot be refused and cannot be cleaned up after.
func Stop(ctx context.Context, target Info, opt StopOptions) (StopMethod, error) {
	log := opt.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	grace := opt.Grace
	if grace <= 0 {
		grace = defaultGrace
	}

	alive, err := Alive(target)
	if err != nil {
		return "", err
	}
	if !alive {
		return "", ErrNotRunning
	}

	if opt.ShutdownURL != "" {
		if err := postShutdown(ctx, opt); err != nil {
			log.Debug("shutdown endpoint unavailable", "pid", target.PID, "err", err)
		} else {
			gone, err := WaitExit(ctx, target, grace)
			if err != nil {
				return "", err
			}
			if gone {
				return StoppedByEndpoint, nil
			}
			log.Warn("shutdown endpoint accepted the request but the process is still running",
				"pid", target.PID, "waited", grace)
		}
	}

	if err := RequestCtrlC(ctx, target.PID); err != nil {
		log.Warn("could not deliver ctrl+c", "pid", target.PID, "err", err)
	} else {
		gone, err := WaitExit(ctx, target, grace)
		if err != nil {
			return "", err
		}
		if gone {
			return StoppedByCtrlC, nil
		}
		log.Warn("process ignored ctrl+c, escalating to termination",
			"pid", target.PID, "waited", grace)
	}

	if err := Terminate(target); err != nil {
		return "", err
	}
	gone, err := WaitExit(ctx, target, terminateGrace)
	if err != nil {
		return "", err
	}
	if !gone {
		return "", fmt.Errorf("winproc: process %d survived termination", target.PID)
	}
	return StoppedByTerminate, nil
}

func postShutdown(ctx context.Context, opt StopOptions) error {
	ctx, cancel := context.WithTimeout(ctx, endpointTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opt.ShutdownURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Length", "0")
	for k, v := range opt.ShutdownHeaders {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return shutdownAccepted(resp.StatusCode, resp.Header.Get("Content-Type"))
}

// shutdownAccepted decides whether a response really came from a shutdown
// endpoint.
//
// A 2xx status on its own is not enough. Applications routinely answer unknown
// paths with a rendered error page and status 200, and believing one of those
// makes JARVIS wait out the entire grace period for a shutdown that was never
// started, turning a one-second stop into a thirty-second one. Actuator
// answers with JSON, so requiring a JSON media type separates a real
// acknowledgement from an HTML page that merely looks successful.
func shutdownAccepted(status int, contentType string) error {
	if status < 200 || status > 299 {
		return fmt.Errorf("shutdown endpoint returned status %d", status)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return fmt.Errorf("shutdown endpoint returned unparseable content type %q", contentType)
	}
	if !strings.Contains(mediaType, "json") {
		return fmt.Errorf(
			"shutdown endpoint answered %d with %q, which is not an actuator response",
			status, mediaType)
	}
	return nil
}
