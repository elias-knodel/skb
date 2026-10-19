package learn_test

import (
	"slices"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/learn"
)

func key(code evdev.EvCode, v int32) evdev.InputEvent {
	return evdev.InputEvent{Type: evdev.EV_KEY, Code: code, Value: v}
}

func TestSourceFilterDisarmedPassesAll(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()
	for _, v := range []int32{1, 2, 0} {
		pass, _, captured := f.Filter(key(evdev.KEY_1, v), false)
		if !pass || captured {
			t.Errorf("value %d: pass=%v captured=%v", v, pass, captured)
		}
	}
}

func TestSourceFilterCapturesAndSwallows(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()

	pass, code, captured := f.Filter(key(evdev.KEY_1, 1), true)
	if pass || !captured || code != evdev.KEY_1 {
		t.Fatalf("press: pass=%v code=%d captured=%v", pass, code, captured)
	}

	// Follow-ups stay swallowed even after the filter is disarmed.
	for _, v := range []int32{2, 0} {
		if pass, _, captured := f.Filter(key(evdev.KEY_1, v), false); pass || captured {
			t.Errorf("value %d: pass=%v captured=%v", v, pass, captured)
		}
	}

	// After the release the key behaves normally again.
	if pass, _, _ := f.Filter(key(evdev.KEY_1, 1), false); !pass {
		t.Error("press after release must pass while disarmed")
	}
}

func TestSourceFilterHeldBeforeArming(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()
	f.Filter(key(evdev.KEY_2, 1), false)

	if pass, _, captured := f.Filter(key(evdev.KEY_2, 2), true); !pass || captured {
		t.Error("repeat of a pre-held key must pass")
	}

	if pass, _, captured := f.Filter(key(evdev.KEY_2, 0), true); !pass || captured {
		t.Error("release of a pre-held key must pass")
	}
}

func TestSourceFilterPassesNonKeyEvents(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()

	for _, ev := range []evdev.InputEvent{
		{Type: evdev.EV_MSC, Code: evdev.MSC_SCAN, Value: 7},
		{Type: evdev.EV_SYN, Code: evdev.SYN_REPORT},
	} {
		if pass, _, captured := f.Filter(ev, true); !pass || captured {
			t.Errorf("event %+v: pass=%v captured=%v", ev, pass, captured)
		}
	}
}

func TestSourceFilterCapturesAgainAfterRelease(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()
	f.Filter(key(evdev.KEY_3, 1), true)
	f.Filter(key(evdev.KEY_3, 0), true)

	if _, _, captured := f.Filter(key(evdev.KEY_3, 1), true); !captured {
		t.Error("second press not captured")
	}
}

type step struct {
	ev   evdev.InputEvent
	to   []evdev.EvCode
	done bool
}

func run(t *testing.T, steps []step) {
	t.Helper()

	c := learn.NewComboCapture()

	for i, s := range steps {
		to, done := c.Feed(s.ev)
		if done != s.done || !slices.Equal(to, s.to) {
			t.Fatalf("step %d: got %v/%v, want %v/%v", i, to, done, s.to, s.done)
		}
	}
}

func TestComboSingleKey(t *testing.T) {
	t.Parallel()

	run(t, []step{{key(evdev.KEY_F5, 1), []evdev.EvCode{evdev.KEY_F5}, true}})
}

func TestComboModifierPlusKey(t *testing.T) {
	t.Parallel()

	run(t, []step{
		{key(evdev.KEY_LEFTSHIFT, 1), nil, false},
		{key(evdev.KEY_MINUS, 1), []evdev.EvCode{evdev.KEY_LEFTSHIFT, evdev.KEY_MINUS}, true},
	})
}

func TestComboModifiersAreSorted(t *testing.T) {
	t.Parallel()

	run(t, []step{
		{key(evdev.KEY_RIGHTALT, 1), nil, false},
		{key(evdev.KEY_LEFTCTRL, 1), nil, false},
		{key(evdev.KEY_A, 1), []evdev.EvCode{evdev.KEY_LEFTCTRL, evdev.KEY_RIGHTALT, evdev.KEY_A}, true},
	})
}

func TestComboLoneModifier(t *testing.T) {
	t.Parallel()

	run(t, []step{
		{key(evdev.KEY_LEFTSHIFT, 1), nil, false},
		{key(evdev.KEY_LEFTSHIFT, 0), []evdev.EvCode{evdev.KEY_LEFTSHIFT}, true},
	})
}

func TestComboTwoModifiersWithoutKeyThenKey(t *testing.T) {
	t.Parallel()

	run(t, []step{
		{key(evdev.KEY_LEFTCTRL, 1), nil, false},
		{key(evdev.KEY_LEFTSHIFT, 1), nil, false},
		{key(evdev.KEY_LEFTCTRL, 0), nil, false},
		{key(evdev.KEY_LEFTSHIFT, 0), nil, false},
		{key(evdev.KEY_F5, 1), []evdev.EvCode{evdev.KEY_F5}, true},
	})
}

func TestComboIgnoresNoise(t *testing.T) {
	t.Parallel()

	run(t, []step{
		{key(evdev.KEY_A, 0), nil, false},
		{key(evdev.BTN_LEFT, 1), nil, false},
		{key(evdev.KEY_LEFTSHIFT, 2), nil, false},
		{evdev.InputEvent{Type: evdev.EV_REL, Code: evdev.REL_X, Value: 1}, nil, false},
		{key(evdev.KEY_B, 1), []evdev.EvCode{evdev.KEY_B}, true},
	})
}

func TestSourceFilterNeverCapturesPointerButtons(t *testing.T) {
	t.Parallel()

	f := learn.NewSourceFilter()

	for code := evdev.EvCode(evdev.BTN_LEFT); code <= evdev.BTN_MIDDLE; code++ {
		pass, _, captured := f.Filter(key(code, 1), true)
		if !pass || captured {
			t.Errorf("button %#x: pass=%v captured=%v", code, pass, captured)
		}

		if pass, _, _ := f.Filter(key(code, 0), true); !pass {
			t.Errorf("button %#x release swallowed", code)
		}
	}

	// The extra mouse buttons stay learnable.
	if _, _, captured := f.Filter(key(evdev.BTN_SIDE, 1), true); !captured {
		t.Error("BTN_SIDE not captured")
	}

	if _, _, captured := f.Filter(key(evdev.KEY_1, 1), true); !captured {
		t.Error("KEY_1 not captured")
	}
}
