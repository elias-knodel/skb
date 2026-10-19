package config_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
)

const validDevices = `
[[devices]]
name = "d"
vendor = 0x1b1c
product = 7000
`

const validMapping = `
[[mappings]]
from = "KEY_1"
to = ["KEY_6"]
`

func parseErr(t *testing.T, doc, wantSub string) {
	t.Helper()

	_, err := config.Parse([]byte(doc))
	if err == nil {
		t.Fatalf("expected error containing %q", wantSub)
	}

	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("error %q does not contain %q", err, wantSub)
	}
}

func TestLoadDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("../../configs/config.toml")
	if err != nil {
		t.Fatal(err)
	}

	wantDevices := []config.DeviceMatcher{
		{Name: "Darkstar wired", Vendor: 0x1b1c, Product: 0x1bb2},
		{Name: "Darkstar Slipstream receiver", Vendor: 0x1b1c, Product: 0x1bdc},
	}
	if !slices.Equal(cfg.Devices, wantDevices) {
		t.Errorf("devices = %+v", cfg.Devices)
	}

	want := []struct {
		from, key evdev.EvCode
		mods      []evdev.EvCode
	}{
		{evdev.KEY_1, evdev.KEY_6, nil},
		{evdev.KEY_2, evdev.KEY_7, nil},
		{evdev.KEY_3, evdev.KEY_F5, nil},
		{evdev.KEY_4, evdev.KEY_MINUS, []evdev.EvCode{evdev.KEY_LEFTSHIFT}},
	}
	if len(cfg.Mappings) != len(want) {
		t.Fatalf("mappings = %d", len(cfg.Mappings))
	}

	for i, w := range want {
		m := cfg.Mappings[i]
		if m.From != w.from || m.Key != w.key || !slices.Equal(m.Modifiers, w.mods) {
			t.Errorf("mapping %d = %+v", i, m)
		}
	}

	if got := cfg.SourceKeys(); !slices.Equal(got, []evdev.EvCode{2, 3, 4, 5}) {
		t.Errorf("SourceKeys = %v", got)
	}
}

func TestIntegerBases(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse([]byte(validDevices + validMapping))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Devices[0].Vendor != 0x1b1c || cfg.Devices[0].Product != 7000 {
		t.Errorf("device = %+v", cfg.Devices[0])
	}
}

func TestInvalid(t *testing.T) {
	t.Parallel()

	mapping := func(from, to string) string {
		return validDevices + "\n[[mappings]]\nfrom = \"" + from + "\"\nto = " + to + "\n"
	}

	cases := []struct {
		name, doc, sub string
	}{
		{"unknown key", mapping("KEY_1", `["KEY_FOO"]`), "mappings[0]"},
		{"unknown key name", mapping("KEY_1", `["KEY_FOO"]`), "KEY_FOO"},
		{"non-key from", mapping("REL_X", `["KEY_A"]`), "mappings[0]"},
		{"duplicate from", mapping("KEY_1", `["KEY_A"]`) + "\n[[mappings]]\nfrom = \"KEY_1\"\nto = [\"KEY_B\"]\n", "duplicate"},
		{"empty to", mapping("KEY_1", `[]`), "mappings[0]"},
		{"modifier not last", mapping("KEY_1", `["KEY_A", "KEY_B"]`), "modifier"},
		{"duplicate in to", mapping("KEY_1", `["KEY_LEFTSHIFT", "KEY_LEFTSHIFT", "KEY_A"]`), "duplicate"},
		{"too many", mapping("KEY_1", `["KEY_LEFTSHIFT","KEY_LEFTCTRL","KEY_LEFTALT","KEY_LEFTMETA","KEY_RIGHTSHIFT","KEY_A"]`), "mappings[0]"},
		{"no devices", validMapping, "devices"},
		{"zero vendor", "[[devices]]\nname=\"d\"\nvendor=0\nproduct=1\n" + validMapping, "vendor"},
		{"zero product", "[[devices]]\nname=\"d\"\nvendor=1\nproduct=0\n" + validMapping, "product"},
		{"vendor range", "[[devices]]\nname=\"d\"\nvendor=0x10000\nproduct=1\n" + validMapping, "vendor"},
		{"unknown field", validDevices + validMapping + "\nfoo = 1\n", "foo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parseErr(t, tc.doc, tc.sub)
		})
	}
}

func TestLoadMissing(t *testing.T) {
	t.Parallel()

	_, err := config.Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(validDevices+validMapping), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestMappingString(t *testing.T) {
	t.Parallel()

	m := config.Mapping{
		From:      evdev.KEY_4,
		Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT},
		Key:       evdev.KEY_MINUS,
	}
	if got := m.String(); got != "KEY_4 -> KEY_LEFTSHIFT+KEY_MINUS" {
		t.Errorf("String = %q", got)
	}

	if got := m.Outputs(); !slices.Equal(got, []evdev.EvCode{evdev.KEY_LEFTSHIFT, evdev.KEY_MINUS}) {
		t.Errorf("Outputs = %v", got)
	}
}

func TestNoMappingsValid(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse([]byte(validDevices))
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Mappings) != 0 {
		t.Errorf("mappings = %v", cfg.Mappings)
	}
}

func TestAdminUID(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse([]byte("admin_uid = 1000\n" + validDevices))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.AdminUID == nil || *cfg.AdminUID != 1000 {
		t.Errorf("admin = %v", cfg.AdminUID)
	}

	cfg, err = config.Parse([]byte(validDevices))
	if err != nil || cfg.AdminUID != nil {
		t.Errorf("absent admin: %v %v", cfg, err)
	}

	for _, v := range []string{"-1", "4294967295"} {
		parseErr(t, "admin_uid = "+v+"\n"+validDevices, "admin_uid")
	}
}

func TestToRequiresKeyboardKey(t *testing.T) {
	t.Parallel()

	parseErr(t, validDevices+"\n[[mappings]]\nfrom = \"KEY_1\"\nto = [\"BTN_LEFT\"]\n", "mappings[0]")
}

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()

	want := []config.DeviceMatcher{
		{Name: "Darkstar wired", Vendor: 0x1b1c, Product: 0x1bb2},
		{Name: "Darkstar Slipstream receiver", Vendor: 0x1b1c, Product: 0x1bdc},
	}
	if !slices.Equal(cfg.Devices, want) || len(cfg.Mappings) != 0 || cfg.AdminUID != nil {
		t.Errorf("default = %+v", cfg)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	repo, err := config.Load("../../configs/config.toml")
	if err != nil {
		t.Fatal(err)
	}

	admin := uint32(1000)
	withAdmin := config.Default()
	withAdmin.AdminUID = &admin

	for name, cfg := range map[string]*config.Config{"default": config.Default(), "repo": repo, "admin": withAdmin} {
		data, err := config.Marshal(cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !strings.HasPrefix(string(data), "# Managed by skb") {
			t.Errorf("%s: missing header", name)
		}

		back, err := config.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, data)
		}

		if !reflect.DeepEqual(cfg, back) {
			t.Errorf("%s: round trip differs\n%+v\n%+v", name, cfg, back)
		}
	}

	data, _ := config.Marshal(withAdmin)
	if !strings.Contains(string(data), "admin_uid = 1000") {
		t.Errorf("no admin_uid in %s", data)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	cases := map[string][]config.Mapping{
		"duplicate from": {{From: evdev.KEY_1, Key: evdev.KEY_A}, {From: evdev.KEY_1, Key: evdev.KEY_B}},
		"empty to":       {{From: evdev.KEY_1}},
		"non-modifier":   {{From: evdev.KEY_1, Modifiers: []evdev.EvCode{evdev.KEY_A}, Key: evdev.KEY_B}},
		"too many": {{
			From: evdev.KEY_1, Key: evdev.KEY_A,
			Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT, evdev.KEY_RIGHTSHIFT, evdev.KEY_LEFTCTRL, evdev.KEY_RIGHTCTRL, evdev.KEY_LEFTALT},
		}},
	}

	for name, mappings := range cases {
		cfg := config.Default()
		cfg.Mappings = mappings

		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestWithMapping(t *testing.T) {
	t.Parallel()

	base := config.Default()
	base.Mappings = []config.Mapping{
		{From: evdev.KEY_1, Key: evdev.KEY_6},
		{From: evdev.KEY_2, Key: evdev.KEY_7},
	}

	added, err := base.WithMapping(config.Mapping{From: evdev.KEY_3, Key: evdev.KEY_F5}, 0)
	if err != nil || len(added.Mappings) != 3 || len(base.Mappings) != 2 {
		t.Fatalf("add: %v %v", added, err)
	}

	updated, err := base.WithMapping(config.Mapping{From: evdev.KEY_1, Key: evdev.KEY_F5}, evdev.KEY_1)
	if err != nil || len(updated.Mappings) != 2 || updated.Mappings[0].Key != evdev.KEY_F5 || base.Mappings[0].Key != evdev.KEY_6 {
		t.Fatalf("update: %v %v", updated, err)
	}

	moved, err := base.WithMapping(config.Mapping{From: evdev.KEY_3, Key: evdev.KEY_F5}, evdev.KEY_1)
	if err != nil || len(moved.Mappings) != 2 || moved.Mappings[0].From != evdev.KEY_3 {
		t.Fatalf("move: %v %v", moved, err)
	}

	if _, err := base.WithMapping(config.Mapping{From: evdev.KEY_2, Key: evdev.KEY_F5}, evdev.KEY_1); err == nil {
		t.Error("move onto existing source succeeded")
	}

	if _, err := base.WithMapping(config.Mapping{From: evdev.KEY_3}, 0); err == nil {
		t.Error("invalid mapping accepted")
	}
}

func TestWithoutMapping(t *testing.T) {
	t.Parallel()

	base := config.Default()
	base.Mappings = []config.Mapping{{From: evdev.KEY_1, Key: evdev.KEY_6}}

	got, err := base.WithoutMapping(evdev.KEY_1)
	if err != nil || len(got.Mappings) != 0 || len(base.Mappings) != 1 {
		t.Fatalf("remove: %v %v", got, err)
	}

	if _, err := base.WithoutMapping(evdev.KEY_2); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestSave(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := config.Default()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("stat: %v %v", st, err)
	}

	cfg.Mappings = []config.Mapping{{From: evdev.KEY_1, Key: evdev.KEY_6}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	back, err := config.Load(path)
	if err != nil || len(back.Mappings) != 1 {
		t.Fatalf("load: %v %v", back, err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestSaveFailureKeepsOriginal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := config.Save(path, config.Default()); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(filepath.Clean(path))

	if err := os.Chmod(dir, 0o555); err != nil { //nolint:gosec // read-only directory for the failure case
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore for cleanup

	cfg := config.Default()
	cfg.Mappings = []config.Mapping{{From: evdev.KEY_1, Key: evdev.KEY_6}}

	if os.Geteuid() != 0 {
		if err := config.Save(path, cfg); err == nil {
			t.Fatal("expected error")
		}
	}

	after, _ := os.ReadFile(filepath.Clean(path))
	if !bytes.Equal(before, after) {
		t.Error("original modified")
	}
}

func TestLoadOrDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cfg, exists, err := config.LoadOrDefault(filepath.Join(dir, "none.toml"))
	if err != nil || exists || len(cfg.Devices) != 2 {
		t.Fatalf("missing: %v %v %v", cfg, exists, err)
	}

	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("foo = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := config.LoadOrDefault(bad); err == nil {
		t.Fatal("invalid file accepted")
	}

	if data, _ := os.ReadFile(filepath.Clean(bad)); string(data) != "foo = 1\n" {
		t.Error("invalid file overwritten")
	}
}

func TestFromRejectsPointerButtons(t *testing.T) {
	t.Parallel()

	parseErr(t, validDevices+"\n[[mappings]]\nfrom = \"BTN_LEFT\"\nto = [\"KEY_A\"]\n", "mappings[0]")

	cfg := config.Default()
	if _, err := cfg.WithMapping(config.Mapping{From: evdev.BTN_RIGHT, Key: evdev.KEY_A}, 0); err == nil {
		t.Error("BTN_RIGHT accepted as source")
	}

	// The extra mouse buttons remain mappable.
	if _, err := cfg.WithMapping(config.Mapping{From: evdev.BTN_SIDE, Key: evdev.KEY_A}, 0); err != nil {
		t.Errorf("BTN_SIDE rejected: %v", err)
	}
}
