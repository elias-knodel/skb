package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elias-knodel/skb/internal/cli"
)

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer

	code := cli.Run(context.Background(), args, &stdout, &stderr, "test")

	return code, stdout.String(), stderr.String()
}

const validConfig = `
[[devices]]
name = "Darkstar wired"
vendor = 0x1b1c
product = 0x1bb2

[[mappings]]
from = "KEY_4"
to = ["KEY_LEFTSHIFT", "KEY_MINUS"]
`

func TestHelp(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-h", "--help", "help"} {
		code, _, stderr := run(arg)
		if code != 0 {
			t.Errorf("%s: code = %d", arg, code)
		}

		for _, want := range []string{"Usage", "-interval", "-socket", "Without a command"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q", arg, want)
			}
		}
	}
}

func TestTUISubcommandRemoved(t *testing.T) {
	t.Parallel()

	code, _, stderr := run("tui")
	if code != 2 || !strings.Contains(stderr, `unknown command "tui"`) {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestTUIFlagsHelp(t *testing.T) {
	t.Parallel()

	code, _, stderr := run("--interval", "1s", "--help")
	if code != 0 || !strings.Contains(stderr, "Usage") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestTUIBadFlag(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--interval", "bogus"}, {"--bogus"}} {
		if code, _, _ := run(args...); code != 2 {
			t.Errorf("%v: code = %d", args, code)
		}
	}
}

func TestTUIUnexpectedArgument(t *testing.T) {
	t.Parallel()

	code, _, stderr := run("--interval", "1s", "extra")
	if code != 2 || !strings.Contains(stderr, `unexpected argument "extra"`) {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	t.Parallel()

	if code, _, _ := run("bogus"); code != 2 {
		t.Errorf("code = %d", code)
	}
}

func TestCheckConfigValid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run("check-config", "--config", path)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}

	for _, want := range []string{"Darkstar wired", "0x1b1c:0x1bb2", "KEY_4 -> KEY_LEFTSHIFT+KEY_MINUS"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q: %s", want, stdout)
		}
	}
}

func TestCheckConfigInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("foo = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run("check-config", "--config", path)
	if code != 1 || stderr == "" {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestCheckConfigDefault(t *testing.T) {
	t.Parallel()

	if code, _, stderr := run("check-config", "--config", "../../configs/config.toml"); code != 0 {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestSubcommandHelp(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"check-config", "daemon"} {
		if code, _, _ := run(sub, "--help"); code != 0 {
			t.Errorf("%s --help: code = %d", sub, code)
		}
	}
}

func TestUsageListsCommands(t *testing.T) {
	t.Parallel()

	_, _, stderr := run("--help")
	for _, want := range []string{"install", "uninstall", "daemon", "check-config"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("usage lacks %q", want)
		}
	}

	if strings.Contains(stderr, "  tui ") {
		t.Error("usage lists the removed tui command")
	}
}

func TestInstallRejectsBadUIDBeforeAnySideEffect(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := run("install", "--admin-uid", "abc")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "invalid uid") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCheckConfigZeroMappings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[[devices]]\nname = \"d\"\nvendor = 1\nproduct = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := run("check-config", "--config", path); code != 0 {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestInstallHelp(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"install", "uninstall"} {
		if code, _, _ := run(sub, "--help"); code != 0 {
			t.Errorf("%s --help: code = %d", sub, code)
		}
	}
}
