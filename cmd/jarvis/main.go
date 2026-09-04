//go:build windows

// Command jarvis is the single executable that ships as JARVIS.
//
// One binary, several modes:
//
//	jarvis run          foreground, logs to the console (development)
//	jarvis service      entry point used by the Service Control Manager
//	jarvis install      register the service for automatic start
//	jarvis uninstall    stop and deregister the service
//	jarvis start|stop|status
//	jarvis tray         per-user notification area icon
//	jarvis stopper      internal helper that delivers Ctrl+C to a managed process
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/sjkim/jarvis/internal/buildinfo"
	"github.com/sjkim/jarvis/internal/config"
	"github.com/sjkim/jarvis/internal/core"
	"github.com/sjkim/jarvis/internal/tray"
	"github.com/sjkim/jarvis/internal/winproc"
	"github.com/sjkim/jarvis/internal/winsvc"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "jarvis: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	// The SCM normally invokes us with the "service" argument, but handle a
	// bare launch too so a hand-created service definition still works.
	if len(args) == 0 {
		if isSvc, err := winsvc.IsService(); err == nil && isSvc {
			return winsvc.Run("")
		}
		// A double-click of jarvis.exe should land in the notification area,
		// not print a usage page that nobody will see.
		return cmdTray(nil)
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return cmdRun(rest)
	case "service":
		return cmdService(rest)
	case "install":
		return cmdInstall(rest)
	case "uninstall":
		return cmdUninstall(rest)
	case "start":
		return cmdStart(rest)
	case "stop":
		return cmdStop(rest)
	case "status":
		return cmdStatus(rest)
	case "tray":
		return cmdTray(rest)
	case "proc":
		return cmdProc(rest)
	case "stopper":
		return cmdStopper(rest)
	case "version", "--version", "-v":
		fmt.Println(buildinfo.String())
		return nil
	case "help", "--help", "-h":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `%s

Usage: jarvis <command> [flags]

Commands:
  (no args)          Install if needed, show the tray icon, start the service.
  run                Run the core in the foreground (development).
  service            Service Control Manager entry point.
  install            Register the Windows service for automatic start.
  uninstall          Stop and deregister the Windows service.
  start              Start the installed service.
  stop               Stop the installed service.
  status             Show service state and configured data root.
  tray               Same as launching with no arguments.
  proc               Low-level process control: launch, list, info, stop.
  version            Print build information.

Common flags:
  --home <dir>       Data root. Defaults to %%ProgramData%%\JARVIS,
                     or the JARVIS_HOME environment variable.
`, buildinfo.String())
}

// homeFlag wires --home onto a flag set. An empty value means "resolve the
// default", which keeps the flag out of the config-loading logic.
func homeFlag(fs *flag.FlagSet) *string {
	return fs.String("home", "", "data root directory")
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("jarvis "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func cmdRun(args []string) error {
	fs := newFlagSet("run")
	home := homeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c, err := core.Bootstrap(ctx, *home, true)
	if err != nil {
		return err
	}

	runErr := c.Run(ctx)
	// Shut down even when Run failed, so the listener and database are always
	// released before the process exits.
	if err := c.Shutdown(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func cmdService(args []string) error {
	fs := newFlagSet("service")
	home := homeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return winsvc.Run(*home)
}

func cmdInstall(args []string) error {
	fs := newFlagSet("install")
	home := homeFlag(fs)
	start := fs.Bool("start", true, "start the service after installing")
	tray := fs.Bool("tray", true, "run the tray icon at logon for the current user")
	aclOnly := fs.Bool("acl-only", false, "grant interactive users start/stop rights and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *aclOnly {
		if err := winsvc.AllowInteractiveControl(); err != nil {
			return err
		}
		fmt.Println("interactive users can start and stop the service")
		return nil
	}

	if err := winsvc.Install(*home); err != nil {
		if !errors.Is(err, winsvc.ErrAlreadyInstalled) {
			return err
		}
		fmt.Printf("service %q is already installed\n", winsvc.ServiceName)
	} else {
		fmt.Printf("service %q installed (automatic start)\n", winsvc.ServiceName)
	}

	if err := winsvc.AllowInteractiveControl(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not grant start/stop to interactive users: %v\n", err)
	}

	if *tray {
		if err := winsvc.EnableTrayAutostart(*home); err != nil {
			// Not fatal: the core is what matters, the icon is convenience.
			fmt.Fprintf(os.Stderr, "warning: could not register tray autostart: %v\n", err)
		} else {
			fmt.Println("tray icon registered to start at logon")
		}
	}

	if *start {
		if err := winsvc.StartService(); err != nil {
			return err
		}
		fmt.Println("service started")
		printAdminURL(*home)
	}
	return nil
}

func cmdUninstall(args []string) error {
	fs := newFlagSet("uninstall")
	keepTray := fs.Bool("keep-tray", false, "leave the tray autostart entry in place")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := winsvc.Uninstall(); err != nil {
		if errors.Is(err, winsvc.ErrNotInstalled) {
			fmt.Println("service is not installed, nothing to do")
		} else {
			return err
		}
	} else {
		fmt.Printf("service %q removed\n", winsvc.ServiceName)
	}

	if !*keepTray {
		if err := winsvc.DisableTrayAutostart(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove tray autostart: %v\n", err)
		}
	}

	fmt.Println("note: java processes started by JARVIS are still running and were not touched")
	return nil
}

func cmdStart([]string) error {
	if err := winsvc.StartService(); err != nil {
		return err
	}
	fmt.Println("service started")
	return nil
}

func cmdStop([]string) error {
	if err := winsvc.StopService(); err != nil {
		return err
	}
	fmt.Println("service stopped; managed java processes were not affected")
	return nil
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	home := homeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println(buildinfo.String())

	state, err := winsvc.Status()
	switch {
	case errors.Is(err, winsvc.ErrNotInstalled):
		fmt.Println("service:    not installed")
	case err != nil:
		fmt.Printf("service:    query failed: %v\n", err)
	default:
		fmt.Printf("service:    %s\n", winsvc.StateString(state))
	}

	paths := config.NewPaths(*home)
	fmt.Printf("data root:  %s\n", paths.Root)

	if cmd, err := winsvc.TrayAutostartCommand(); err == nil && cmd != "" {
		fmt.Printf("tray:       registered at logon\n")
	} else {
		fmt.Printf("tray:       not registered for %s\n", os.Getenv("USERNAME"))
	}
	return nil
}

func cmdTray(args []string) error {
	fs := newFlagSet("tray")
	home := homeFlag(fs)
	register := fs.Bool("register", false, "register the tray to start at logon and exit")
	unregister := fs.Bool("unregister", false, "remove the logon entry and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Autostart lives in HKCU, so these need no elevation. They are separate
	// from `install` because the service is machine-wide while the tray icon
	// is per-user: a second administrator logging in needs their own entry.
	switch {
	case *register && *unregister:
		return errors.New("--register and --unregister are mutually exclusive")
	case *register:
		if err := winsvc.EnableTrayAutostart(*home); err != nil {
			return err
		}
		cmd, _ := winsvc.TrayAutostartCommand()
		fmt.Printf("tray registered at logon: %s\n", cmd)
		return nil
	case *unregister:
		if err := winsvc.DisableTrayAutostart(); err != nil {
			return err
		}
		fmt.Println("tray logon entry removed")
		return nil
	}

	if err := winsvc.EnsureInstalled(*home); err != nil {
		winsvc.NotifyError("JARVIS 설치", err.Error())
		return err
	}
	return tray.Run(*home)
}

// cmdStopper is an internal helper, not something an operator invokes. It
// exists as a separate process because a console CTRL+C reaches everything
// attached to the target's console, so whoever raises it must be expendable.
// See winproc.SendCtrlC.
func cmdStopper(args []string) error {
	fs := newFlagSet("stopper")
	pid := fs.Uint("pid", 0, "process id to signal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pid == 0 {
		return errors.New("--pid is required")
	}
	return winproc.SendCtrlC(uint32(*pid))
}

func printAdminURL(home string) {
	cfg, paths, err := config.Load(home)
	if err != nil {
		return
	}
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	addr := cfg.Server.Addr
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	fmt.Printf("admin UI:   %s://%s/\n", scheme, addr)
	fmt.Printf("data root:  %s\n", paths.Root)
}
