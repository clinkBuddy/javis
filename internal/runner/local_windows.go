//go:build windows

package runner

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sjkim/jarvis/internal/winproc"
)

// LocalRunner runs java processes on the Windows host.
type LocalRunner struct {
	log *slog.Logger
}

func NewLocalRunner(log *slog.Logger) *LocalRunner {
	return &LocalRunner{log: log}
}

func (r *LocalRunner) Start(ctx context.Context, spec LaunchSpec) (Handle, error) {
	instanceID, err := winproc.NewInstanceID()
	if err != nil {
		return Handle{}, err
	}

	args := make([]string, 0, len(spec.JVMArgs)+len(spec.ProgramArgs)+4)
	args = append(args, spec.JVMArgs...)
	args = append(args, winproc.AppMarker(spec.AppName))
	args = append(args, winproc.InstanceMarker(instanceID))
	args = append(args, "-jar", spec.JarPath)
	args = append(args, spec.ProgramArgs...)

	info, err := winproc.Launch(winproc.LaunchSpec{
		Exe:     spec.JavaExe,
		Args:    args,
		Dir:     spec.WorkDir,
		Env:     spec.Env,
		LogPath: spec.ConsoleLog,
	})
	if err != nil {
		return Handle{}, err
	}

	cmdLine, _ := winproc.CommandLine(info.PID)

	h := Handle{
		AppName:      spec.AppName,
		InstanceID:   instanceID,
		PID:          info.PID,
		CreateTime:   info.CreateTime,
		State:        StateStarting,
		StartedAt:    time.Now(),
		ArtifactPath: spec.JarPath,
		CommandLine:  cmdLine,
		ConsoleLog:   spec.ConsoleLog,
	}

	r.log.Info("process launched",
		"app", spec.AppName,
		"pid", info.PID,
		"instance", instanceID,
		"java", spec.JavaExe,
		"jar", spec.JarPath,
		"log", spec.ConsoleLog,
	)
	return h, nil
}

func (r *LocalRunner) Stop(ctx context.Context, h Handle) (StopResult, error) {
	start := time.Now()

	target := winproc.Info{PID: h.PID, CreateTime: h.CreateTime}
	opt := winproc.StopOptions{
		Log: r.log.With("app", h.AppName, "pid", h.PID),
	}

	// Parse shutdown URL from the handle's command line? No—we pass it from
	// the app config via the spec stored alongside the handle. For now, the
	// supervisor stores these in the Handle's unused fields. The real wiring
	// comes with the supervisor; for this milestone, direct callers set opts.

	method, err := winproc.Stop(ctx, target, opt)
	if err != nil {
		return StopResult{}, err
	}
	return StopResult{
		Method:   string(method),
		Duration: time.Since(start),
	}, nil
}

func (r *LocalRunner) StopWithOptions(ctx context.Context, h Handle, shutdownURL string, shutdownHeaders map[string]string, grace time.Duration) (StopResult, error) {
	start := time.Now()

	target := winproc.Info{PID: h.PID, CreateTime: h.CreateTime}
	method, err := winproc.Stop(ctx, target, winproc.StopOptions{
		ShutdownURL:     shutdownURL,
		ShutdownHeaders: shutdownHeaders,
		Grace:           grace,
		Log:             r.log.With("app", h.AppName, "pid", h.PID),
	})
	if err != nil {
		return StopResult{}, err
	}
	return StopResult{
		Method:   string(method),
		Duration: time.Since(start),
	}, nil
}

func (r *LocalRunner) Alive(h Handle) (bool, error) {
	return winproc.Alive(winproc.Info{PID: h.PID, CreateTime: h.CreateTime})
}

func (r *LocalRunner) WaitExit(ctx context.Context, h Handle, timeout time.Duration) (bool, error) {
	return winproc.WaitExit(ctx, winproc.Info{PID: h.PID, CreateTime: h.CreateTime}, timeout)
}

func (r *LocalRunner) Discover(_ context.Context, appName string) ([]Handle, error) {
	matches, err := winproc.FindApp(appName)
	if err != nil {
		return nil, err
	}
	return matchesToHandles(appName, matches), nil
}

func (r *LocalRunner) DiscoverAll(_ context.Context) ([]Handle, error) {
	matches, err := winproc.FindByMarker("-D" + winproc.AppProperty + "=")
	if err != nil {
		return nil, err
	}
	handles := make([]Handle, 0, len(matches))
	for _, m := range matches {
		app := extractProperty(m.CommandLine, winproc.AppProperty)
		inst := extractProperty(m.CommandLine, winproc.InstanceProperty)
		handles = append(handles, Handle{
			AppName:     app,
			InstanceID:  inst,
			PID:         m.PID,
			CreateTime:  m.CreateTime,
			State:       StateRunning,
			CommandLine: m.CommandLine,
		})
	}
	return handles, nil
}

func matchesToHandles(appName string, matches []winproc.Match) []Handle {
	handles := make([]Handle, 0, len(matches))
	for _, m := range matches {
		inst := extractProperty(m.CommandLine, winproc.InstanceProperty)
		handles = append(handles, Handle{
			AppName:     appName,
			InstanceID:  inst,
			PID:         m.PID,
			CreateTime:  m.CreateTime,
			State:       StateRunning,
			CommandLine: m.CommandLine,
		})
	}
	return handles
}

// extractProperty reads -Dproperty=value from a command line.
func extractProperty(cmdLine, property string) string {
	prefix := "-D" + property + "="
	for _, part := range strings.Fields(cmdLine) {
		// Handle quoted tokens: "...-Djarvis.app=foo..."
		part = strings.Trim(part, `"`)
		if after, found := strings.CutPrefix(part, prefix); found {
			return after
		}
	}
	return ""
}
