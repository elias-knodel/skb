package tui_test

import (
	"testing"

	"github.com/elias-knodel/skb/internal/tui"
)

func TestKeyLabel(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"KEY_LEFTSHIFT": "Shift",
		"KEY_RIGHTCTRL": "Right Ctrl",
		"KEY_LEFTMETA":  "Super",
		"KEY_MINUS":     "Minus",
		"KEY_EQUAL":     "Equal",
		"KEY_SPACE":     "Space",
		"KEY_ENTER":     "Enter",
		"BTN_SIDE":      "Mouse 4",
		"BTN_EXTRA":     "Mouse 5",
		"KEY_F5":        "F5",
		"KEY_BACKSPACE": "Backspace",
		"BTN_FORWARD":   "Forward",
		"UNKNOWN":       "Unknown",
	}

	for in, want := range tests {
		if got := tui.KeyLabel(in); got != want {
			t.Errorf("KeyLabel(%q) = %q, want %q", in, got, want)
		}
	}

	if got := tui.ComboLabel([]string{"KEY_LEFTSHIFT", "KEY_MINUS"}); got != "Shift + Minus" {
		t.Errorf("ComboLabel = %q", got)
	}
}
