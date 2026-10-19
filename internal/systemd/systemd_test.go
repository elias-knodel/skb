package systemd_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elias-knodel/skb/internal/systemd"
)

func TestExecRunnerExitCodes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, code, err := systemd.ExecRunner{Path: "/bin/true"}.Run(ctx)
	if err != nil || code != 0 {
		t.Errorf("true: code=%d err=%v", code, err)
	}

	_, code, err = systemd.ExecRunner{Path: "/bin/false"}.Run(ctx)
	if err != nil || code != 1 {
		t.Errorf("false: code=%d err=%v", code, err)
	}
}

func TestExecRunnerStdout(t *testing.T) {
	t.Parallel()

	out, code, err := systemd.ExecRunner{Path: "/bin/echo"}.Run(context.Background(), "hello")
	if err != nil || code != 0 || out != "hello\n" {
		t.Errorf("out=%q code=%d err=%v", out, code, err)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	t.Parallel()

	_, _, err := systemd.ExecRunner{Path: "/nonexistent/systemctl"}.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeRunner struct{ code int }

func (f fakeRunner) Run(context.Context, ...string) (string, int, error) { return "", f.code, nil }

func TestIsActive(t *testing.T) {
	t.Parallel()

	for code, want := range map[int]bool{0: true, 3: false} {
		got, err := systemd.IsActive(context.Background(), fakeRunner{code}, "skb.service")
		if err != nil || got != want {
			t.Errorf("code %d: got %v err %v", code, got, err)
		}
	}
}

func TestUnitFileExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if systemd.UnitFileExists(root, "skb.service") {
		t.Error("unit reported before creation")
	}

	dir := filepath.Join(root, "etc", "systemd", "system")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "skb.service"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if !systemd.UnitFileExists(root, "skb.service") {
		t.Error("unit not found")
	}
}
