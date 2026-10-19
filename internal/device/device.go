// Package device classifies input devices against the configured matchers.
package device

import (
	"slices"
	"strings"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/keys"
)

// VirtualNamePrefix starts the name of every virtual device skb creates.
const VirtualNamePrefix = "skb virtual: "

// maxVirtualName is the uinput name limit (80 bytes) without the terminating NUL.
const maxVirtualName = 79

// ID is the bus, vendor, product and version identifier of an input device.
type ID struct {
	Bus, Vendor, Product, Version uint16
}

// Info describes an opened input device.
type Info struct {
	Path string
	Name string
	ID   ID
	Caps map[evdev.EvType][]evdev.EvCode
}

// HasKey reports whether the device advertises the EV_KEY code.
func (i Info) HasKey(code evdev.EvCode) bool {
	return slices.Contains(i.Caps[evdev.EV_KEY], code)
}

// Decision is the outcome of classifying a device.
type Decision struct {
	// Matched reports that vendor and product are configured.
	Matched bool
	// Grab reports that the device carries keyboard keys and must be grabbed.
	Grab bool
	// Matcher is the name of the matching configuration entry.
	Matcher string
	// Reason explains a negative decision.
	Reason string
}

// Classify decides whether and how skb handles the device.
func Classify(info Info, matchers []config.DeviceMatcher) Decision {
	if info.ID.Bus == evdev.BUS_VIRTUAL && strings.HasPrefix(info.Name, VirtualNamePrefix) {
		return Decision{Reason: "own virtual device"}
	}

	for _, m := range matchers {
		if m.Vendor != info.ID.Vendor || m.Product != info.ID.Product {
			continue
		}

		d := Decision{Matched: true, Matcher: m.Name}

		d.Grab = slices.ContainsFunc(info.Caps[evdev.EV_KEY], keys.IsKeyboardKey)
		if !d.Grab {
			d.Reason = "no keyboard keys"
		}

		return d
	}

	return Decision{Reason: "no matching device entry"}
}

// VirtualName derives the virtual device name from the source name.
func VirtualName(src string) string {
	name := VirtualNamePrefix + src
	if len(name) > maxVirtualName {
		name = name[:maxVirtualName]
	}

	return name
}

// VirtualID is the identifier of skb's virtual devices. It never matches a
// configured device, so skb does not re-grab its own output.
func VirtualID() ID {
	return ID{Bus: evdev.BUS_VIRTUAL, Version: 1}
}
