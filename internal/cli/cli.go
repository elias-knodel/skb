// Package cli implements the skb command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/daemon"
	"github.com/elias-knodel/skb/internal/evdevio"
	"github.com/elias-knodel/skb/internal/install"
	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/systemd"
	"github.com/elias-knodel/skb/internal/tui"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const hotplugDebounce = 100 * time.Millisecond

// Run executes the subcommand named by args[0], or the TUI when no command is
// given, and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	var (
		run  func(context.Context, []string, io.Writer, io.Writer, string) error
		rest []string
	)

	switch {
	case len(args) == 0:
		run = runTUI
	case args[0] == "-h" || args[0] == "--help" || args[0] == "help":
		usage(stderr)

		return exitOK
	case strings.HasPrefix(args[0], "-"):
		run, rest = runTUI, args
	default:
		rest = args[1:]

		switch args[0] {
		case "daemon":
			run = runDaemon
		case "install":
			run = runInstall
		case "uninstall":
			run = runUninstall
		case "check-config":
			run = runCheckConfig
		default:
			_, _ = fmt.Fprintf(stderr, "skb: unknown command %q\n", args[0])
			usage(stderr)

			return exitUsage
		}
	}

	err := run(ctx, rest, stdout, stderr, version)

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errUsage):
		return exitUsage
	default:
		_, _ = fmt.Fprintf(stderr, "skb: %v\n", err)

		return exitError
	}
}

// errUsage marks flag parsing failures, which the flag package already reported.
var errUsage = errors.New("usage")

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: skb [flags]
       skb <command> [flags]

Without a command, skb starts the terminal UI.

Flags:
`)

	fs := flag.NewFlagSet("skb", flag.ContinueOnError)
	fs.SetOutput(w)
	tuiFlags(fs)
	fs.PrintDefaults()

	_, _ = fmt.Fprint(w, `
Commands:
  daemon        grab the configured devices and remap their keys
  install       install binary, unit and config (run as root; see TUI setup)
  uninstall     remove binary and unit, keeping the config
  check-config  validate a configuration file

Use "skb <command> --help" for the flags of a command.
`)
}

// tuiFlags registers the flags of the default TUI mode.
func tuiFlags(fs *flag.FlagSet) (socket *string, interval *time.Duration) {
	socket = fs.String("socket", ipc.DefaultSocket, "daemon status socket")
	interval = fs.Duration("interval", 2*time.Second, "refresh interval")

	return socket, interval
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("skb "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)

	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}

		return fmt.Errorf("%w: %w", errUsage, err)
	}

	return nil
}

func runCheckConfig(_ context.Context, args []string, stdout, stderr io.Writer, _ string) error {
	fs := newFlagSet("check-config", stderr)
	path := fs.String("config", config.DefaultPath, "configuration file")

	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "Devices:")

	for _, d := range cfg.Devices {
		_, _ = fmt.Fprintf(stdout, "  %s (0x%04x:0x%04x)\n", d.Name, d.Vendor, d.Product)
	}

	_, _ = fmt.Fprintln(stdout, "Mappings:")

	for _, m := range cfg.Mappings {
		_, _ = fmt.Fprintf(stdout, "  %s\n", m)
	}

	return nil
}

func runTUI(ctx context.Context, args []string, _, stderr io.Writer, _ string) error {
	fs := flag.NewFlagSet("skb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr) }
	socket, interval := tuiFlags(fs)

	if err := parse(fs, args); err != nil {
		return err
	}

	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "skb: unexpected argument %q\n", fs.Arg(0))

		return errUsage
	}

	model := tui.New(tui.Config{
		Client:   ipc.Client{Socket: *socket},
		Prober:   tui.NewProber(*socket),
		Command:  tui.PkexecCommand,
		UID:      uint32(os.Getuid()), //nolint:gosec // uid is non-negative
		Interval: *interval,
	})

	// Query the terminal background once, before bubbletea owns stdin.
	_ = lipgloss.HasDarkBackground()

	if _, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}

	return nil
}

func runDaemon(ctx context.Context, args []string, _, stderr io.Writer, version string) error {
	fs := newFlagSet("daemon", stderr)
	path := fs.String("config", config.DefaultPath, "configuration file")
	socket := fs.String("socket", ipc.DefaultSocket, "status socket")

	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, exists, err := config.LoadOrDefault(*path)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	watcher, err := evdevio.NewWatcher(evdevio.DefaultDir)
	if err != nil {
		return err
	}

	defer func() { _ = watcher.Close() }()

	ln, err := ipc.Listen(ctx, *socket)
	if err != nil {
		return err
	}

	d := daemon.New(daemon.Options{
		Backend:     evdevio.Backend{},
		Watcher:     watcher,
		Config:      cfg,
		ConfigPath:  *path,
		ConfigSaver: func(c *config.Config) error { return config.Save(*path, c) },
		Version:     version,
		Logger:      logger,
		Debounce:    hotplugDebounce,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	serveDone := make(chan error, 1)

	go func() { serveDone <- ipc.Serve(runCtx, ln, d) }()

	if !exists {
		logger.Info("config file missing, using built-in defaults", "path", *path)
	}

	logger.Info("skb started", "version", version, "config", *path, "socket", *socket)

	runErr := d.Run(runCtx)

	cancel()

	if serveErr := <-serveDone; serveErr != nil {
		logger.Warn("status server", "error", strings.TrimSpace(serveErr.Error()))
	}

	return runErr
}

func newInstaller(stdout io.Writer) install.Installer {
	return install.Installer{
		Root:       "/",
		Runner:     systemd.ExecRunner{Path: systemd.DefaultSystemctl},
		Executable: os.Executable,
		Euid:       os.Geteuid,
		Out:        stdout,
	}
}

func runInstall(ctx context.Context, args []string, stdout, stderr io.Writer, _ string) error {
	opts, err := install.ParseArgs(args, os.Environ())
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, "Usage: skb install --admin-uid UID [--disable-input-remapper]")

			return err
		}

		_, _ = fmt.Fprintf(stderr, "skb: %v\n", err)

		return errUsage
	}

	return newInstaller(stdout).Install(ctx, opts)
}

func runUninstall(ctx context.Context, args []string, stdout, stderr io.Writer, _ string) error {
	fs := newFlagSet("uninstall", stderr)

	if err := parse(fs, args); err != nil {
		return err
	}

	return newInstaller(stdout).Uninstall(ctx)
}
