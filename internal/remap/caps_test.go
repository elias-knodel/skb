package remap_test

import (
	"slices"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/remap"
)

func TestOutputCaps(t *testing.T) {
	t.Parallel()

	src := map[evdev.EvType][]evdev.EvCode{
		evdev.EV_KEY: {evdev.KEY_A, evdev.KEY_1, evdev.KEY_A},
		evdev.EV_REL: {evdev.REL_X},
		evdev.EV_ABS: {evdev.ABS_VOLUME},
		evdev.EV_MSC: {evdev.MSC_SCAN},
		evdev.EV_REP: {0, 1},
		evdev.EV_LED: {0, 1},
		evdev.EV_SYN: {0},
	}

	got := remap.OutputCaps(src)

	for _, typ := range []evdev.EvType{evdev.EV_REP, evdev.EV_LED, evdev.EV_SYN} {
		if _, ok := got[typ]; ok {
			t.Errorf("type %d must be excluded", typ)
		}
	}

	if !slices.IsSorted(got[evdev.EV_KEY]) || len(slices.Compact(slices.Clone(got[evdev.EV_KEY]))) != len(got[evdev.EV_KEY]) {
		t.Error("key codes not sorted and unique")
	}

	for _, code := range []evdev.EvCode{evdev.KEY_F24, evdev.KEY_6, evdev.KEY_LEFTSHIFT, evdev.KEY_A} {
		if !slices.Contains(got[evdev.EV_KEY], code) {
			t.Errorf("caps lack key %d", code)
		}
	}

	if slices.Contains(got[evdev.EV_KEY], evdev.BTN_LEFT) {
		t.Error("BTN_LEFT must not be added")
	}

	if !slices.Equal(got[evdev.EV_REL], []evdev.EvCode{evdev.REL_X}) ||
		!slices.Equal(got[evdev.EV_ABS], []evdev.EvCode{evdev.ABS_VOLUME}) ||
		!slices.Equal(got[evdev.EV_MSC], []evdev.EvCode{evdev.MSC_SCAN}) {
		t.Errorf("caps = %v", got)
	}
}

func TestOutputCapsKeepsSourceButtons(t *testing.T) {
	t.Parallel()

	got := remap.OutputCaps(map[evdev.EvType][]evdev.EvCode{evdev.EV_KEY: {evdev.BTN_LEFT}})
	if !slices.Contains(got[evdev.EV_KEY], evdev.BTN_LEFT) {
		t.Error("source button dropped")
	}
}
