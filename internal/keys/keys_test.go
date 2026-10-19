package keys_test

import (
	"slices"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/keys"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := map[string]evdev.EvCode{
		"KEY_1":         2,
		"KEY_2":         3,
		"KEY_3":         4,
		"KEY_4":         5,
		"KEY_6":         7,
		"KEY_7":         8,
		"KEY_F5":        63,
		"KEY_LEFTSHIFT": 42,
		"KEY_MINUS":     12,
	}

	for name, want := range cases {
		got, err := keys.Parse(name)
		if err != nil {
			t.Fatalf("Parse(%q): %v", name, err)
		}

		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", name, got, want)
		}

		if back := keys.Name(got); back != name {
			t.Errorf("Name(Parse(%q)) = %q", name, back)
		}
	}
}

func TestParseButton(t *testing.T) {
	t.Parallel()

	if _, err := keys.Parse("BTN_SIDE"); err != nil {
		t.Fatalf("BTN_SIDE rejected: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "key_1", "KEY_FOO", "REL_X"} {
		if _, err := keys.Parse(name); err == nil {
			t.Errorf("Parse(%q) succeeded", name)
		}
	}
}

func TestNameFallback(t *testing.T) {
	t.Parallel()

	if got := keys.Name(0x2fe); got != "KEY_766" {
		t.Errorf("Name(0x2fe) = %q", got)
	}
}

func TestIsModifier(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"KEY_LEFTCTRL", "KEY_RIGHTCTRL", "KEY_LEFTSHIFT", "KEY_RIGHTSHIFT",
		"KEY_LEFTALT", "KEY_RIGHTALT", "KEY_LEFTMETA", "KEY_RIGHTMETA",
	} {
		code, err := keys.Parse(name)
		if err != nil {
			t.Fatal(err)
		}

		if !keys.IsModifier(code) {
			t.Errorf("%s is not a modifier", name)
		}
	}

	minus, _ := keys.Parse("KEY_MINUS")
	if keys.IsModifier(minus) {
		t.Error("KEY_MINUS reported as modifier")
	}
}

func TestIsKeyboardKey(t *testing.T) {
	t.Parallel()

	for _, code := range []evdev.EvCode{
		evdev.KEY_ESC, evdev.KEY_F5, evdev.KEY_MINUS, evdev.KEY_LEFTSHIFT, evdev.KEY_OK, evdev.KEY_MICMUTE,
	} {
		if !keys.IsKeyboardKey(code) {
			t.Errorf("%d must be a keyboard key", code)
		}
	}

	for _, code := range []evdev.EvCode{
		0, evdev.BTN_LEFT, evdev.BTN_SIDE, evdev.BTN_TRIGGER_HAPPY1, 0x300,
	} {
		if keys.IsKeyboardKey(code) {
			t.Errorf("%d must not be a keyboard key", code)
		}
	}
}

func TestKeyboardKeys(t *testing.T) {
	t.Parallel()

	all := keys.KeyboardKeys()
	if !slices.IsSorted(all) || len(slices.Compact(slices.Clone(all))) != len(all) {
		t.Fatal("not sorted and unique")
	}

	for _, code := range all {
		if !keys.IsKeyboardKey(code) {
			t.Errorf("%d is not a keyboard key", code)
		}

		if _, ok := evdev.KEYToString[code]; !ok {
			t.Errorf("%d has no name", code)
		}
	}

	if !slices.Contains(all, evdev.KEY_6) || slices.Contains(all, evdev.BTN_LEFT) {
		t.Error("unexpected membership")
	}
}

func TestSortModifiers(t *testing.T) {
	t.Parallel()

	in := []evdev.EvCode{evdev.KEY_RIGHTALT, evdev.KEY_LEFTSHIFT, evdev.KEY_LEFTCTRL}
	want := []evdev.EvCode{evdev.KEY_LEFTCTRL, evdev.KEY_LEFTSHIFT, evdev.KEY_RIGHTALT}

	if got := keys.SortModifiers(in); !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if in[0] != evdev.KEY_RIGHTALT {
		t.Error("input mutated")
	}
}

func TestIsPrimaryPointerButton(t *testing.T) {
	t.Parallel()

	for _, code := range []evdev.EvCode{evdev.BTN_LEFT, evdev.BTN_RIGHT, evdev.BTN_MIDDLE} {
		if !keys.IsPrimaryPointerButton(code) {
			t.Errorf("%d must be a primary pointer button", code)
		}
	}

	for _, code := range []evdev.EvCode{evdev.KEY_1, evdev.BTN_SIDE, evdev.BTN_EXTRA, evdev.BTN_TASK, 0x10f} {
		if keys.IsPrimaryPointerButton(code) {
			t.Errorf("%d must not be a primary pointer button", code)
		}
	}
}
