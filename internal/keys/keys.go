// Package keys converts between evdev key names and codes.
package keys

import (
	"fmt"
	"slices"

	"github.com/holoplot/go-evdev"
)

// Parse returns the code for a case-sensitive KEY_* or BTN_* name.
func Parse(name string) (evdev.EvCode, error) {
	code, ok := evdev.KEYFromString[name]
	if !ok {
		return 0, fmt.Errorf("unknown key name %q", name)
	}

	return code, nil
}

// Name returns the symbolic name of code, or KEY_<n> when it has none.
func Name(code evdev.EvCode) string {
	if name, ok := evdev.KEYToString[code]; ok {
		return name
	}

	return fmt.Sprintf("KEY_%d", code)
}

// IsModifier reports whether code is a left or right Ctrl, Shift, Alt or Meta key.
func IsModifier(code evdev.EvCode) bool {
	switch code {
	case evdev.KEY_LEFTCTRL, evdev.KEY_RIGHTCTRL,
		evdev.KEY_LEFTSHIFT, evdev.KEY_RIGHTSHIFT,
		evdev.KEY_LEFTALT, evdev.KEY_RIGHTALT,
		evdev.KEY_LEFTMETA, evdev.KEY_RIGHTMETA:
		return true
	default:
		return false
	}
}

// Keyboard key code ranges: the classic block and the extended block that
// starts at KEY_OK. The BTN_* range in between is excluded.
const (
	keyboardLowMax    evdev.EvCode = 0xff
	keyboardHighMin   evdev.EvCode = 0x160
	keyboardHighMax   evdev.EvCode = 0x2bf
	keyboardFirstCode evdev.EvCode = 1
)

// modifierOrder is the canonical modifier order.
var modifierOrder = []evdev.EvCode{
	evdev.KEY_LEFTCTRL, evdev.KEY_RIGHTCTRL,
	evdev.KEY_LEFTSHIFT, evdev.KEY_RIGHTSHIFT,
	evdev.KEY_LEFTALT, evdev.KEY_RIGHTALT,
	evdev.KEY_LEFTMETA, evdev.KEY_RIGHTMETA,
}

// IsKeyboardKey reports whether code lies in the keyboard key ranges, as
// opposed to buttons.
func IsKeyboardKey(code evdev.EvCode) bool {
	return (code >= keyboardFirstCode && code <= keyboardLowMax) ||
		(code >= keyboardHighMin && code <= keyboardHighMax)
}

// KeyboardKeys returns all named keyboard key codes in ascending order.
func KeyboardKeys() []evdev.EvCode {
	out := make([]evdev.EvCode, 0, len(evdev.KEYToString))

	for code := range evdev.KEYToString {
		if IsKeyboardKey(code) {
			out = append(out, code)
		}
	}

	slices.Sort(out)

	return out
}

// SortModifiers returns a copy of mods in canonical modifier order; other
// codes sort after the modifiers by value.
func SortModifiers(mods []evdev.EvCode) []evdev.EvCode {
	out := slices.Clone(mods)

	rank := func(c evdev.EvCode) int {
		if i := slices.Index(modifierOrder, c); i >= 0 {
			return i
		}

		return len(modifierOrder) + int(c)
	}

	slices.SortFunc(out, func(a, b evdev.EvCode) int { return rank(a) - rank(b) })

	return out
}

// IsPrimaryPointerButton reports whether code is BTN_LEFT, BTN_RIGHT or
// BTN_MIDDLE, which must never be learned or remapped.
func IsPrimaryPointerButton(code evdev.EvCode) bool {
	return code >= evdev.BTN_LEFT && code <= evdev.BTN_MIDDLE
}
