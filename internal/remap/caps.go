package remap

import (
	"slices"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/keys"
)

// OutputCaps derives the virtual device capabilities from the source
// capabilities plus every keyboard key. Only forwarded event types are kept.
func OutputCaps(source map[evdev.EvType][]evdev.EvCode) map[evdev.EvType][]evdev.EvCode {
	out := map[evdev.EvType][]evdev.EvCode{evdev.EV_KEY: nil}

	for _, typ := range []evdev.EvType{evdev.EV_KEY, evdev.EV_REL, evdev.EV_ABS, evdev.EV_MSC, evdev.EV_SW} {
		if codes, ok := source[typ]; ok {
			out[typ] = slices.Clone(codes)
		}
	}

	// All keyboard keys are advertised up front, so a mapping added at runtime
	// never requires recreating the virtual device.
	out[evdev.EV_KEY] = append(out[evdev.EV_KEY], keys.KeyboardKeys()...)

	for typ, codes := range out {
		slices.Sort(codes)
		out[typ] = slices.Compact(codes)
	}

	return out
}
