package tui

import "strings"

var keyLabels = map[string]string{
	"KEY_LEFTSHIFT":  "Shift",
	"KEY_RIGHTSHIFT": "Right Shift",
	"KEY_LEFTCTRL":   "Ctrl",
	"KEY_RIGHTCTRL":  "Right Ctrl",
	"KEY_LEFTALT":    "Alt",
	"KEY_RIGHTALT":   "Right Alt",
	"KEY_LEFTMETA":   "Super",
	"KEY_RIGHTMETA":  "Right Super",
	"KEY_SPACE":      "Space",
	"KEY_ENTER":      "Enter",
	"BTN_SIDE":       "Mouse 4",
	"BTN_EXTRA":      "Mouse 5",
}

// keyLabel returns a human-readable name for an evdev key name.
func keyLabel(name string) string {
	if l, ok := keyLabels[name]; ok {
		return l
	}

	n := strings.TrimPrefix(strings.TrimPrefix(name, "KEY_"), "BTN_")
	if n == "" {
		return name
	}

	return strings.ToUpper(n[:1]) + strings.ToLower(n[1:])
}

// comboLabel joins the labels of a key combination.
func comboLabel(names []string) string {
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = keyLabel(n)
	}

	return strings.Join(labels, " + ")
}

// targetLabel returns the typed label of a target when known, else the
// layout-independent key names.
func targetLabel(to []string, label string) string {
	if label != "" {
		return label
	}

	return comboLabel(to)
}
