package tui

import (
	"context"
	"net"
	"os"
	"os/exec"

	"github.com/elias-knodel/skb/internal/systemd"
)

const pkexecPath = "/usr/bin/pkexec"

type setupProber struct {
	socket string
	runner systemd.Runner
}

// NewProber returns the production prober: an unprivileged socket dial,
// `systemctl is-active` and a unit file check.
func NewProber(socket string) Prober {
	return setupProber{socket: socket, runner: systemd.ExecRunner{Path: systemd.DefaultSystemctl}}
}

// Probe implements Prober.
func (p setupProber) Probe(ctx context.Context) SetupState {
	var st SetupState

	var dialer net.Dialer

	if conn, err := dialer.DialContext(ctx, "unix", p.socket); err == nil {
		st.SocketOK = true

		_ = conn.Close()
	}

	st.UnitInstalled = systemd.UnitFileExists("/", "skb.service")

	var err error

	if st.ServiceActive, err = systemd.IsActive(ctx, p.runner, "skb.service"); err != nil {
		st.Err = err.Error()
	}

	if st.InputRemapperActive, err = systemd.IsActive(ctx, p.runner, "input-remapper.service"); err != nil && st.Err == "" {
		st.Err = err.Error()
	}

	return st
}

// PkexecCommand runs this executable's subcommand through pkexec. pkexec
// requires an absolute program path.
func PkexecCommand(args ...string) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}

	return exec.CommandContext(context.Background(), pkexecPath, append([]string{exe}, args...)...) //nolint:gosec // fixed helper, own executable
}
