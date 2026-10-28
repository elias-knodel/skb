package install_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/elias-knodel/skb/deploy"
	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/install"
)

type fakeRunner struct {
	calls   [][]string
	failOn  string
	failErr error
}

func (r *fakeRunner) Run(_ context.Context, args ...string) (string, int, error) {
	r.calls = append(r.calls, args)

	if r.failOn != "" && strings.Join(args, " ") == r.failOn {
		return "", 1, r.failErr
	}

	return "", 0, nil
}

func (r *fakeRunner) joined() []string {
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, strings.Join(c, " "))
	}

	return out
}

type env struct {
	root   string
	exe    string
	runner *fakeRunner
	out    *bytes.Buffer
	inst   install.Installer
}

func newEnv(t *testing.T, euid int) *env {
	t.Helper()

	root := t.TempDir()
	exe := filepath.Join(t.TempDir(), "skb-src")

	if err := os.WriteFile(exe, []byte("binary-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := &env{root: root, exe: exe, runner: &fakeRunner{}, out: &bytes.Buffer{}}
	e.inst = install.Installer{
		Root:       root,
		Runner:     e.runner,
		Executable: func() (string, error) { return exe, nil },
		Euid:       func() int { return euid },
		Out:        e.out,
	}

	return e
}

func (e *env) path(p string) string { return filepath.Join(e.root, p) }

func (e *env) writeConfig(t *testing.T, content string) {
	t.Helper()

	if err := os.MkdirAll(e.path("etc/skb"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(e.path("etc/skb/config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRequiresRoot(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 1000)

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err == nil {
		t.Fatal("expected error")
	}

	entries, _ := os.ReadDir(e.root)
	if len(entries) != 0 || len(e.runner.calls) != 0 {
		t.Errorf("side effects: %v %v", entries, e.runner.calls)
	}
}

func TestFreshInstall(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err != nil {
		t.Fatal(err)
	}

	bin := e.path("usr/local/bin/skb")

	data, err := os.ReadFile(filepath.Clean(bin))
	if err != nil || string(data) != "binary-bytes" {
		t.Fatalf("binary: %q %v", data, err)
	}

	if st, _ := os.Stat(bin); st.Mode().Perm() != 0o755 {
		t.Errorf("binary mode = %v", st.Mode().Perm())
	}

	unit, err := os.ReadFile(e.path("etc/systemd/system/skb.service"))
	if err != nil || !bytes.Equal(unit, deploy.Unit) {
		t.Fatalf("unit mismatch: %v", err)
	}

	desktop, err := os.ReadFile(e.path("usr/local/share/applications/skb.desktop"))
	if err != nil || !bytes.Equal(desktop, deploy.Desktop) {
		t.Fatalf("desktop entry mismatch: %v", err)
	}

	if st, _ := os.Stat(e.path("usr/local/share/applications/skb.desktop")); st.Mode().Perm() != 0o644 {
		t.Errorf("desktop entry mode = %v", st.Mode().Perm())
	}

	cfg, err := config.Load(e.path("etc/skb/config.toml"))
	if err != nil || cfg.AdminUID == nil || *cfg.AdminUID != 1000 || len(cfg.Devices) != 2 {
		t.Fatalf("config: %+v %v", cfg, err)
	}

	want := []string{"daemon-reload", "enable skb.service", "restart skb.service"}
	if got := e.runner.joined(); !slices.Equal(got, want) {
		t.Errorf("calls = %v", got)
	}
}

func TestInstallKeepsMappingsAddsAdmin(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)
	e.writeConfig(t, "[[devices]]\nname = \"d\"\nvendor = 1\nproduct = 2\n\n[[mappings]]\nfrom = \"KEY_1\"\nto = [\"KEY_6\"]\n")

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err != nil {
		t.Fatal(err)
	}

	desktop, err := os.ReadFile(e.path("usr/local/share/applications/skb.desktop"))
	if err != nil || !bytes.Equal(desktop, deploy.Desktop) {
		t.Fatalf("desktop entry mismatch: %v", err)
	}

	if st, _ := os.Stat(e.path("usr/local/share/applications/skb.desktop")); st.Mode().Perm() != 0o644 {
		t.Errorf("desktop entry mode = %v", st.Mode().Perm())
	}

	cfg, err := config.Load(e.path("etc/skb/config.toml"))
	if err != nil || len(cfg.Mappings) != 1 || cfg.AdminUID == nil || *cfg.AdminUID != 1000 {
		t.Fatalf("config: %+v %v", cfg, err)
	}
}

func TestInstallKeepsOtherAdmin(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)
	original := "admin_uid = 1001\n\n[[devices]]\nname = \"d\"\nvendor = 1\nproduct = 2\n"
	e.writeConfig(t, original)

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(e.path("etc/skb/config.toml"))
	if string(data) != original {
		t.Errorf("config changed: %s", data)
	}

	if !strings.Contains(e.out.String(), "1001") {
		t.Errorf("no notice: %q", e.out.String())
	}
}

func TestInstallInvalidConfigTouchesNothing(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)
	e.writeConfig(t, "foo = 1\n")

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err == nil {
		t.Fatal("expected error")
	}

	data, _ := os.ReadFile(e.path("etc/skb/config.toml"))
	if string(data) != "foo = 1\n" || len(e.runner.calls) != 0 {
		t.Errorf("config=%q calls=%v", data, e.runner.calls)
	}

	if _, err := os.Stat(e.path("usr/local/bin/skb")); err == nil {
		t.Error("binary installed despite invalid config")
	}
}

func TestInstallDisablesInputRemapper(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)

	err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000, DisableInputRemapper: true})
	if err != nil {
		t.Fatal(err)
	}

	got := e.runner.joined()
	if got[len(got)-1] != "disable --now input-remapper.service" {
		t.Errorf("calls = %v", got)
	}
}

func TestInstallCommandFailure(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)
	e.runner.failOn = "enable skb.service"
	e.runner.failErr = nil

	err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000})
	if err == nil || !strings.Contains(err.Error(), "enable skb.service") {
		t.Fatalf("err = %v", err)
	}

	e = newEnv(t, 0)
	e.runner.failOn = "daemon-reload"
	e.runner.failErr = errors.New("boom")

	err = e.inst.Install(context.Background(), install.Options{AdminUID: 1000})
	if err == nil || !strings.Contains(err.Error(), "daemon-reload") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstall(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err != nil {
		t.Fatal(err)
	}

	e.runner.calls = nil

	if err := e.inst.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"usr/local/bin/skb", "etc/systemd/system/skb.service", "usr/local/share/applications/skb.desktop"} {
		if _, err := os.Stat(e.path(p)); err == nil {
			t.Errorf("%s still present", p)
		}
	}

	if _, err := os.Stat(e.path("etc/skb/config.toml")); err != nil {
		t.Errorf("config removed: %v", err)
	}

	want := []string{"disable --now skb.service", "daemon-reload"}
	if got := e.runner.joined(); !slices.Equal(got, want) {
		t.Errorf("calls = %v", got)
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--admin-uid=abc"}, {"--admin-uid=-1"}, {"--admin-uid=4294967295"}, {"--admin-uid=1e3"},
		{"--admin-uid=1000", "extra"}, {"--bogus"},
	} {
		if _, err := install.ParseArgs(args, nil); err == nil {
			t.Errorf("%v accepted", args)
		}
	}

	opts, err := install.ParseArgs(nil, []string{"PKEXEC_UID=1000"})
	if err != nil || opts.AdminUID != 1000 {
		t.Errorf("pkexec fallback: %+v %v", opts, err)
	}

	opts, err = install.ParseArgs(nil, []string{"SUDO_UID=1001"})
	if err != nil || opts.AdminUID != 1001 {
		t.Errorf("sudo fallback: %+v %v", opts, err)
	}

	if _, err := install.ParseArgs([]string{"--admin-uid=1000"}, []string{"PKEXEC_UID=1001"}); err == nil {
		t.Error("flag/env mismatch accepted")
	}

	opts, err = install.ParseArgs([]string{"--admin-uid", "1000", "--disable-input-remapper"}, nil)
	if err != nil || opts.AdminUID != 1000 || !opts.DisableInputRemapper {
		t.Errorf("flags: %+v %v", opts, err)
	}

	if _, err := install.ParseArgs(nil, nil); err == nil {
		t.Error("missing admin uid accepted")
	}
}

func TestUninstallWithoutDesktopEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, 0)

	if err := e.inst.Install(context.Background(), install.Options{AdminUID: 1000}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(e.path("usr/local/share/applications/skb.desktop")); err != nil {
		t.Fatal(err)
	}

	e.runner.calls = nil

	if err := e.inst.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{"disable --now skb.service", "daemon-reload"}
	if got := e.runner.joined(); !slices.Equal(got, want) {
		t.Errorf("calls = %v", got)
	}
}

func TestDesktopEntry(t *testing.T) {
	t.Parallel()

	lines := strings.Split(strings.TrimSpace(string(deploy.Desktop)), "\n")

	if lines[0] != "[Desktop Entry]" {
		t.Errorf("first line = %q", lines[0])
	}

	for _, want := range []string{
		"Type=Application",
		"Exec=/usr/local/bin/skb",
		"TryExec=/usr/local/bin/skb",
		"Terminal=true",
		"Icon=input-mouse",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("missing line %q", want)
		}
	}

	for _, l := range lines {
		if (strings.HasPrefix(l, "Categories=") || strings.HasPrefix(l, "Keywords=")) && !strings.HasSuffix(l, ";") {
			t.Errorf("list does not end with ';': %q", l)
		}

		if strings.HasPrefix(l, "Exec=") && strings.Contains(l, " ") {
			t.Errorf("Exec contains a space: %q", l)
		}
	}
}
