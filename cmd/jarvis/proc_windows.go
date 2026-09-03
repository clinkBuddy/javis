//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/jdk"
	"github.com/sjkim/jarvis/internal/winproc"
)

// `jarvis proc` is the low-level process control surface. It exists so the
// launch, adopt and stop paths can be exercised directly from a shell, and it
// calls exactly the functions the supervisor will use, so verifying it here
// verifies the real thing.

func cmdProc(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jarvis proc <launch|list|info|stop> [flags]")
	}
	switch args[0] {
	case "launch":
		return cmdProcLaunch(args[1:])
	case "list":
		return cmdProcList(args[1:])
	case "info":
		return cmdProcInfo(args[1:])
	case "stop":
		return cmdProcStop(args[1:])
	default:
		return fmt.Errorf("unknown proc subcommand %q", args[0])
	}
}

type launchRecord struct {
	App         string `json:"app"`
	InstanceID  string `json:"instanceId"`
	PID         uint32 `json:"pid"`
	CreateTime  int64  `json:"createTime"`
	Java        string `json:"java"`
	JavaVersion string `json:"javaVersion"`
	Jar         string `json:"jar"`
	WorkDir     string `json:"workDir"`
	LogPath     string `json:"logPath"`
	CommandLine string `json:"commandLine"`
}

func cmdProcLaunch(args []string) error {
	fs := newFlagSet("proc launch")
	home := homeFlag(fs)
	app := fs.String("app", "", "application name (required)")
	jar := fs.String("jar", "", "path to the Spring Boot jar (required)")
	java := fs.String("java", "", "path to java.exe (default: from PATH)")
	jvm := fs.String("jvm", "", "JVM options, e.g. \"-Xms256m -Xmx512m\"")
	appArgs := fs.String("args", "", "program arguments passed after the jar")
	dir := fs.String("dir", "", "working directory (default: <home>/apps/<app>/work)")
	logPath := fs.String("log", "", "console log file (default: <home>/apps/<app>/logs/console-<ts>.log)")
	asJSON := fs.Bool("json", false, "print the launch record as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *app == "" || *jar == "" {
		return errors.New("--app and --jar are required")
	}

	jarPath, err := filepath.Abs(*jar)
	if err != nil {
		return err
	}
	if _, err := os.Stat(jarPath); err != nil {
		return fmt.Errorf("jar not readable: %w", err)
	}

	requested := *java
	if requested == "" {
		if requested, err = exec.LookPath("java"); err != nil {
			return fmt.Errorf("no --java given and java is not on PATH: %w", err)
		}
	}
	runtimeInfo, err := jdk.Resolve(context.Background(), requested)
	if err != nil {
		return err
	}
	if runtimeInfo.Redirected(requested) {
		fmt.Fprintf(os.Stderr,
			"note: %s is a launcher for %s; running the latter directly\n",
			requested, runtimeInfo.JavaExe)
	}

	paths := config.NewPaths(*home)
	if err := paths.EnsureApp(*app); err != nil {
		return err
	}
	workDir := *dir
	if workDir == "" {
		workDir = paths.AppWorkDir(*app)
	}
	console := *logPath
	if console == "" {
		console = filepath.Join(paths.AppLogDir(*app),
			fmt.Sprintf("console-%s.log", time.Now().Format("20060102-150405")))
	}

	instanceID, err := winproc.NewInstanceID()
	if err != nil {
		return err
	}

	// Markers go last among the JVM options so an operator cannot accidentally
	// override them from the options field.
	javaArgs := append(splitArgs(*jvm),
		winproc.AppMarker(*app),
		winproc.InstanceMarker(instanceID),
		"-jar", jarPath,
	)
	javaArgs = append(javaArgs, splitArgs(*appArgs)...)

	info, err := winproc.Launch(winproc.LaunchSpec{
		Exe:     runtimeInfo.JavaExe,
		Args:    javaArgs,
		Dir:     workDir,
		LogPath: console,
	})
	if err != nil {
		return err
	}

	rec := launchRecord{
		App:         *app,
		InstanceID:  instanceID,
		PID:         info.PID,
		CreateTime:  info.CreateTime,
		Java:        runtimeInfo.JavaExe,
		JavaVersion: runtimeInfo.Version,
		Jar:         jarPath,
		WorkDir:     workDir,
		LogPath:     console,
	}
	if cl, err := winproc.CommandLine(info.PID); err == nil {
		rec.CommandLine = cl
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(rec)
	}
	fmt.Printf("launched %s\n", rec.App)
	fmt.Printf("  pid         %d\n", rec.PID)
	fmt.Printf("  createTime  %d\n", rec.CreateTime)
	fmt.Printf("  instance    %s\n", rec.InstanceID)
	fmt.Printf("  java        %s (%s)\n", rec.Java, rec.JavaVersion)
	fmt.Printf("  log         %s\n", rec.LogPath)
	return nil
}

func cmdProcList(args []string) error {
	fs := newFlagSet("proc list")
	app := fs.String("app", "", "list processes launched for this application")
	instance := fs.String("instance", "", "list the process for this instance id")
	marker := fs.String("marker", "", "list processes whose command line contains this text")
	asJSON := fs.Bool("json", false, "print matches as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var (
		matches []winproc.Match
		err     error
	)
	switch {
	case *app != "":
		matches, err = winproc.FindApp(*app)
	case *instance != "":
		matches, err = winproc.FindByMarker(winproc.InstanceMarker(*instance))
	case *marker != "":
		matches, err = winproc.FindByMarker(*marker)
	default:
		// With no filter, show every process JARVIS has ever labelled. This is
		// how orphans from a previous lifetime become visible.
		matches, err = winproc.FindByMarker("-D" + winproc.AppProperty + "=")
	}
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(matches)
	}
	if len(matches) == 0 {
		fmt.Println("no matching processes")
		return nil
	}
	for _, m := range matches {
		fmt.Printf("pid=%-7d createTime=%-20d image=%s\n", m.PID, m.CreateTime, m.Image)
		fmt.Printf("  %s\n", m.CommandLine)
	}
	return nil
}

func cmdProcInfo(args []string) error {
	fs := newFlagSet("proc info")
	pid := fs.Uint("pid", 0, "process id (required)")
	createTime := fs.Int64("create-time", 0, "expected creation FILETIME; 0 skips the identity check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pid == 0 {
		return errors.New("--pid is required")
	}

	target := winproc.Info{PID: uint32(*pid), CreateTime: *createTime}
	alive, err := winproc.Alive(target)
	if err != nil {
		return err
	}
	fmt.Printf("pid         %d\n", target.PID)
	fmt.Printf("alive       %t\n", alive)
	if !alive {
		return nil
	}
	if ct, err := winproc.CreateTime(target.PID); err == nil {
		fmt.Printf("createTime  %d\n", ct)
	}
	if cl, err := winproc.CommandLine(target.PID); err == nil {
		fmt.Printf("commandLine %s\n", cl)
	}
	return nil
}

func cmdProcStop(args []string) error {
	fs := newFlagSet("proc stop")
	pid := fs.Uint("pid", 0, "process id")
	instance := fs.String("instance", "", "stop the process carrying this instance id")
	createTime := fs.Int64("create-time", 0, "expected creation FILETIME; 0 resolves it now")
	grace := fs.Duration("grace", 30*time.Second, "time allowed for each graceful attempt")
	shutdownURL := fs.String("shutdown-url", "", "application shutdown endpoint to try first")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var target winproc.Info
	switch {
	case *instance != "":
		m, err := winproc.FindInstance(*instance)
		if err != nil {
			return err
		}
		target = m.Info
	case *pid != 0:
		target = winproc.Info{PID: uint32(*pid), CreateTime: *createTime}
		if target.CreateTime == 0 {
			ct, err := winproc.CreateTime(target.PID)
			if err != nil {
				return err
			}
			target.CreateTime = ct
		}
	default:
		return errors.New("--pid or --instance is required")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	start := time.Now()

	method, err := winproc.Stop(context.Background(), target, winproc.StopOptions{
		ShutdownURL: *shutdownURL,
		Grace:       *grace,
		Log:         log,
	})
	if errors.Is(err, winproc.ErrNotRunning) {
		fmt.Println("process was already gone")
		return nil
	}
	if err != nil {
		return err
	}

	fmt.Printf("stopped pid %d via %s in %s\n",
		target.PID, method, time.Since(start).Round(time.Millisecond))
	return nil
}

// splitArgs breaks an option string into arguments on whitespace, honouring
// double quotes so that paths with spaces survive. It is only a convenience
// for this CLI; the UI stores options as a list and needs no splitting.
func splitArgs(s string) []string {
	var (
		out     []string
		current strings.Builder
		quoted  bool
	)
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}
