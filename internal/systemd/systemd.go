// Package systemd wraps the systemctl command line.
package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// UnitDir is the system unit directory relative to the root.
const UnitDir = "etc/systemd/system"

// Runner executes systemctl with the given arguments.
type Runner interface {
	// Run returns stdout and the exit code. A non-zero exit is not an error;
	// only a failure to start the command is.
	Run(ctx context.Context, args ...string) (stdout string, exitCode int, err error)
}

// ExecRunner runs the systemctl binary at Path with a minimal environment.
type ExecRunner struct {
	Path string
}

// DefaultSystemctl is the absolute systemctl path.
const DefaultSystemctl = "/usr/bin/systemctl"

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) (string, int, error) {
	path := r.Path
	if path == "" {
		path = DefaultSystemctl
	}

	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed binary path, arguments built by the caller
	cmd.Env = []string{"PATH=/usr/bin:/usr/sbin", "LANG=C"}

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	err := cmd.Run()

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return stdout.String(), exitErr.ExitCode(), nil
	}

	if err != nil {
		return "", -1, fmt.Errorf("run %s: %w", path, err)
	}

	return stdout.String(), 0, nil
}

// IsActive reports whether the unit is active (exit status 0).
func IsActive(ctx context.Context, r Runner, unit string) (bool, error) {
	_, code, err := r.Run(ctx, "is-active", "--quiet", unit)
	if err != nil {
		return false, err
	}

	return code == 0, nil
}

// UnitFileExists reports whether the unit file exists below root.
func UnitFileExists(root, unit string) bool {
	_, err := os.Stat(filepath.Join(root, UnitDir, unit))

	return err == nil
}
