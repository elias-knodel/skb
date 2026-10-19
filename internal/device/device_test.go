package device_test

import (
	"strings"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/device"
)

var (
	matchers = []config.DeviceMatcher{
		{Name: "Darkstar wired", Vendor: 0x1b1c, Product: 0x1bb2},
		{Name: "Darkstar Slipstream receiver", Vendor: 0x1b1c, Product: 0x1bdc},
	}
)

func info(name string, bus, vendor, product uint16, caps map[evdev.EvType][]evdev.EvCode) device.Info {
	return device.Info{
		Path: "/dev/input/event0",
		Name: name,
		ID:   device.ID{Bus: bus, Vendor: vendor, Product: product, Version: 1},
		Caps: caps,
	}
}

func keyboardCaps() map[evdev.EvType][]evdev.EvCode {
	return map[evdev.EvType][]evdev.EvCode{
		evdev.EV_KEY: {evdev.KEY_ESC, evdev.KEY_1, evdev.KEY_2, evdev.KEY_3, evdev.KEY_4, evdev.KEY_A},
	}
}

func TestClassifyKeyboardInterfaces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		product uint16
		matcher string
	}{
		{"wired", 0x1bb2, "Darkstar wired"},
		{"receiver", 0x1bdc, "Darkstar Slipstream receiver"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := device.Classify(info("Keyboard", evdev.BUS_USB, 0x1b1c, tc.product, keyboardCaps()), matchers)
			if !got.Matched || !got.Grab || got.Matcher != tc.matcher {
				t.Errorf("decision = %+v", got)
			}
		})
	}
}

func TestClassifyWithoutSourceKeys(t *testing.T) {
	t.Parallel()

	pointer := map[evdev.EvType][]evdev.EvCode{
		evdev.EV_KEY: {evdev.BTN_LEFT, evdev.BTN_RIGHT, evdev.BTN_SIDE},
	}
	rel := map[evdev.EvType][]evdev.EvCode{
		evdev.EV_REL: {evdev.REL_X, evdev.REL_Y},
	}

	for name, in := range map[string]device.Info{
		"pointer": info("Pointer", evdev.BUS_USB, 0x1b1c, 0x1bb2, pointer),
		"rel":     info("Mouse", evdev.BUS_USB, 0x1b1c, 0x1bdc, rel),
	} {
		got := device.Classify(in, matchers)
		if !got.Matched || got.Grab || got.Reason != "no keyboard keys" {
			t.Errorf("%s: decision = %+v", name, got)
		}
	}
}

func TestClassifyUnlisted(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]device.Info{
		"k100":         info("K100", evdev.BUS_USB, 0x1b1c, 0x1b7c, keyboardCaps()),
		"vendor only":  info("Other", evdev.BUS_USB, 0x1b1c, 0x0001, keyboardCaps()),
		"other vendor": info("Other", evdev.BUS_USB, 0x046d, 0x1bb2, keyboardCaps()),
	} {
		if got := device.Classify(in, matchers); got.Matched {
			t.Errorf("%s: decision = %+v", name, got)
		}
	}
}

func TestClassifyOwnVirtualDevice(t *testing.T) {
	t.Parallel()

	in := info(device.VirtualName("Keyboard"), evdev.BUS_VIRTUAL, 0x1b1c, 0x1bb2, keyboardCaps())

	got := device.Classify(in, matchers)
	if got.Matched || got.Reason != "own virtual device" {
		t.Errorf("decision = %+v", got)
	}
}

func TestVirtualName(t *testing.T) {
	t.Parallel()

	name := device.VirtualName("Corsair Receiver Keyboard")
	if !strings.HasPrefix(name, device.VirtualNamePrefix) {
		t.Errorf("name = %q", name)
	}

	long := device.VirtualName(strings.Repeat("x", 200))
	if !strings.HasPrefix(long, device.VirtualNamePrefix) || len(long) > 79 {
		t.Errorf("len = %d", len(long))
	}
}

func TestVirtualID(t *testing.T) {
	t.Parallel()

	id := device.VirtualID()
	if id.Bus != evdev.BUS_VIRTUAL || id.Vendor != 0 || id.Product != 0 {
		t.Errorf("id = %+v", id)
	}
}

func TestHasKey(t *testing.T) {
	t.Parallel()

	in := info("k", evdev.BUS_USB, 1, 1, keyboardCaps())
	if !in.HasKey(evdev.KEY_1) || in.HasKey(evdev.KEY_F5) {
		t.Error("HasKey mismatch")
	}
}

func TestClassifyGrabsWithoutMappings(t *testing.T) {
	t.Parallel()

	got := device.Classify(info("Keyboard", evdev.BUS_USB, 0x1b1c, 0x1bdc, keyboardCaps()), matchers)
	if !got.Grab {
		t.Errorf("decision = %+v", got)
	}
}
