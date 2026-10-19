// Package install copies the binary, unit and default configuration to
// their fixed system locations. It is meant to run as root, through pkexec or
// sudo, and never accepts paths from its arguments.
package install

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elias-knodel/skb/deploy"
	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/systemd"
)

// Fixed target paths, relative to Installer.Root.
const (
	binRel    = "usr/local/bin/skb"
	unitRel   = "etc/systemd/system/skb.service"
	configRel = "etc/skb/config.toml"

	unitName          = "skb.service"
	inputRemapperUnit = "input-remapper.service"
)

var uidPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

// Options are the installation parameters.
type Options struct {
	// AdminUID becomes admin_uid of a new or admin-less configuration.
	AdminUID uint32
	// DisableInputRemapper also disables the input-remapper service.
	DisableInputRemapper bool
}

// Installer performs the installation below Root.
type Installer struct {
	Root       string
	Runner     systemd.Runner
	Executable func() (string, error)
	Euid       func() int
	Out        io.Writer
}

func (i Installer) path(rel string) string { return filepath.Join(i.Root, rel) }

func (i Installer) printf(format string, args ...any) {
	if i.Out != nil {
		_, _ = fmt.Fprintf(i.Out, format, args...)
	}
}

// Install installs and starts the service. An invalid existing configuration
// aborts before anything is changed.
func (i Installer) Install(ctx context.Context, opts Options) error {
	if i.Euid() != 0 {
		return errors.New("install must run as root")
	}

	cfg, save, err := i.prepareConfig(opts)
	if err != nil {
		return err
	}

	exe, err := i.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	if err := i.installBinary(exe); err != nil {
		return err
	}

	if err := writeAtomic(i.path(unitRel), deploy.Unit, 0o644); err != nil {
		return fmt.Errorf("install unit: %w", err)
	}

	if save {
		if err := os.MkdirAll(filepath.Dir(i.path(configRel)), 0o755); err != nil { //nolint:gosec // /etc/skb is world-readable
			return fmt.Errorf("create config directory: %w", err)
		}

		if err := config.Save(i.path(configRel), cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
	}

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", unitName},
		{"restart", unitName},
	} {
		if err := i.systemctl(ctx, args...); err != nil {
			return err
		}
	}

	if opts.DisableInputRemapper {
		if err := i.systemctl(ctx, "disable", "--now", inputRemapperUnit); err != nil {
			i.printf("notice: %v\n", err)
		}
	}

	return nil
}

// prepareConfig decides the configuration to keep or write.
func (i Installer) prepareConfig(opts Options) (cfg *config.Config, save bool, err error) {
	cfg, exists, err := config.LoadOrDefault(i.path(configRel))
	if err != nil {
		return nil, false, fmt.Errorf("existing configuration is invalid, nothing changed: %w", err)
	}

	switch {
	case !exists || cfg.AdminUID == nil:
		uid := opts.AdminUID
		cfg.AdminUID = &uid

		return cfg, true, nil
	case *cfg.AdminUID != opts.AdminUID:
		i.printf("notice: keeping existing admin_uid %d (requested %d)\n", *cfg.AdminUID, opts.AdminUID)
	}

	return cfg, false, nil
}

func (i Installer) installBinary(exe string) error {
	target := i.path(binRel)

	if src, err := os.Stat(exe); err == nil {
		if dst, err := os.Stat(target); err == nil && os.SameFile(src, dst) {
			return nil
		}
	}

	data, err := os.ReadFile(filepath.Clean(exe))
	if err != nil {
		return fmt.Errorf("read executable: %w", err)
	}

	if err := writeAtomic(target, data, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}

	return nil
}

func (i Installer) systemctl(ctx context.Context, args ...string) error {
	_, code, err := i.Runner.Run(ctx, args...)
	if err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}

	if code != 0 {
		return fmt.Errorf("systemctl %s: exit status %d", strings.Join(args, " "), code)
	}

	return nil
}

// Uninstall stops the service and removes unit and binary; the configuration stays.
func (i Installer) Uninstall(ctx context.Context) error {
	if i.Euid() != 0 {
		return errors.New("uninstall must run as root")
	}

	// A service that is not installed or not running is not an error here.
	_, _, _ = i.Runner.Run(ctx, "disable", "--now", unitName)

	for _, rel := range []string{unitRel, binRel} {
		if err := os.Remove(i.path(rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", rel, err)
		}
	}

	if err := i.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}

	i.printf("kept %s\n", i.path(filepath.Dir(configRel)))

	return nil
}

// writeAtomic writes data to path unless it already has that content and
// mode: temp file in the target directory, fsync, rename.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(filepath.Clean(path)); err == nil && bytes.Equal(existing, data) {
		if st, err := os.Stat(path); err == nil && st.Mode().Perm() == mode {
			return nil
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // system directories are world-readable
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	cleanup := true

	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	cleanup = false

	return nil
}

// ParseArgs parses the install arguments. Without --admin-uid the uid comes
// from PKEXEC_UID, then SUDO_UID; a differing PKEXEC_UID is an error.
func ParseArgs(args, environ []string) (Options, error) {
	fs := flag.NewFlagSet("skb install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	adminFlag := fs.String("admin-uid", "", "uid allowed to modify the configuration")
	disable := fs.Bool("disable-input-remapper", false, "also disable the input-remapper service")

	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("parse arguments: %w", err)
	}

	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	lookup := func(key string) string {
		for _, kv := range environ {
			if v, ok := strings.CutPrefix(kv, key+"="); ok {
				return v
			}
		}

		return ""
	}

	opts := Options{DisableInputRemapper: *disable}

	pkexec := lookup("PKEXEC_UID")

	switch {
	case *adminFlag != "":
		uid, err := parseUID(*adminFlag)
		if err != nil {
			return Options{}, err
		}

		if pkexec != "" && pkexec != *adminFlag {
			return Options{}, fmt.Errorf("--admin-uid %s differs from the invoking uid %s", *adminFlag, pkexec)
		}

		opts.AdminUID = uid
	case pkexec != "":
		uid, err := parseUID(pkexec)
		if err != nil {
			return Options{}, err
		}

		opts.AdminUID = uid
	case lookup("SUDO_UID") != "":
		uid, err := parseUID(lookup("SUDO_UID"))
		if err != nil {
			return Options{}, err
		}

		opts.AdminUID = uid
	default:
		return Options{}, errors.New("--admin-uid is required")
	}

	return opts, nil
}

func parseUID(s string) (uint32, error) {
	if !uidPattern.MatchString(s) {
		return 0, fmt.Errorf("invalid uid %q", s)
	}

	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil || v > math.MaxUint32-1 {
		return 0, fmt.Errorf("invalid uid %q", s)
	}

	return uint32(v), nil
}
