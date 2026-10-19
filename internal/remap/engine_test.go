package remap_test

import (
	"slices"
	"testing"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/remap"
)

// ev is the comparable projection of an input event.
type ev struct {
	Type  evdev.EvType
	Code  evdev.EvCode
	Value int32
}

func key(code evdev.EvCode, v int32) ev { return ev{evdev.EV_KEY, code, v} }

var syn = ev{evdev.EV_SYN, evdev.SYN_REPORT, 0}

func mappings() []config.Mapping {
	return []config.Mapping{
		{From: evdev.KEY_1, Key: evdev.KEY_6},
		{From: evdev.KEY_2, Key: evdev.KEY_7},
		{From: evdev.KEY_3, Key: evdev.KEY_F5},
		{From: evdev.KEY_4, Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT}, Key: evdev.KEY_MINUS},
	}
}

func feed(e *remap.Engine, in ...ev) []ev {
	var out []ev

	for _, i := range in {
		for _, o := range e.Process(evdev.InputEvent{Type: i.Type, Code: i.Code, Value: i.Value}) {
			out = append(out, ev{o.Type, o.Code, o.Value})
		}
	}

	return out
}

func project(events []evdev.InputEvent) []ev {
	var out []ev
	for _, o := range events {
		out = append(out, ev{o.Type, o.Code, o.Value})
	}

	return out
}

func check(t *testing.T, got, want []ev) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestSimpleMapping(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, key(evdev.KEY_1, 1), syn), []ev{key(evdev.KEY_6, 1), syn})
	check(t, feed(e, key(evdev.KEY_1, 2)), []ev{key(evdev.KEY_6, 2)})
	check(t, feed(e, key(evdev.KEY_1, 0)), []ev{key(evdev.KEY_6, 0)})
}

func TestComboMapping(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, key(evdev.KEY_4, 1)), []ev{key(evdev.KEY_LEFTSHIFT, 1), syn, key(evdev.KEY_MINUS, 1)})
	check(t, feed(e, key(evdev.KEY_4, 2)), []ev{key(evdev.KEY_MINUS, 2)})
	check(t, feed(e, key(evdev.KEY_4, 0)), []ev{key(evdev.KEY_MINUS, 0), syn, key(evdev.KEY_LEFTSHIFT, 0)})
}

func TestPassthrough(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, key(evdev.KEY_A, 1), key(evdev.KEY_A, 2), key(evdev.KEY_A, 0)),
		[]ev{key(evdev.KEY_A, 1), key(evdev.KEY_A, 2), key(evdev.KEY_A, 0)})

	msc := ev{evdev.EV_MSC, evdev.MSC_SCAN, 0x7001e}
	check(t, feed(e, msc), []ev{msc})

	rel := ev{evdev.EV_REL, evdev.REL_HWHEEL, 1}
	check(t, feed(e, rel), []ev{rel})
}

func TestDroppedTypes(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, ev{evdev.EV_LED, 1, 1}, ev{evdev.EV_REP, 0, 250}), nil)
}

func TestUnbalanced(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, key(evdev.KEY_1, 0)), nil)
	check(t, feed(e, key(evdev.KEY_1, 2)), nil)
	check(t, feed(e, key(evdev.KEY_1, 1)), []ev{key(evdev.KEY_6, 1)})
	check(t, feed(e, key(evdev.KEY_1, 1)), nil)
}

func TestSharedModifier(t *testing.T) {
	t.Parallel()

	e := remap.New([]config.Mapping{
		{From: evdev.KEY_1, Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT}, Key: evdev.KEY_MINUS},
		{From: evdev.KEY_2, Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT}, Key: evdev.KEY_EQUAL},
	})

	check(t, feed(e, key(evdev.KEY_1, 1)), []ev{key(evdev.KEY_LEFTSHIFT, 1), syn, key(evdev.KEY_MINUS, 1)})
	check(t, feed(e, key(evdev.KEY_2, 1)), []ev{key(evdev.KEY_EQUAL, 1)})
	check(t, feed(e, key(evdev.KEY_1, 0)), []ev{key(evdev.KEY_MINUS, 0)})
	check(t, feed(e, key(evdev.KEY_2, 0)), []ev{key(evdev.KEY_EQUAL, 0), syn, key(evdev.KEY_LEFTSHIFT, 0)})
}

func TestPhysicalModifierStaysDown(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e, key(evdev.KEY_LEFTSHIFT, 1)), []ev{key(evdev.KEY_LEFTSHIFT, 1)})
	check(t, feed(e, key(evdev.KEY_4, 1)), []ev{key(evdev.KEY_MINUS, 1)})
	check(t, feed(e, key(evdev.KEY_4, 0)), []ev{key(evdev.KEY_MINUS, 0)})
	check(t, feed(e, key(evdev.KEY_LEFTSHIFT, 0)), []ev{key(evdev.KEY_LEFTSHIFT, 0)})
}

func TestSynDropped(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	check(t, feed(e,
		ev{evdev.EV_SYN, evdev.SYN_DROPPED, 0},
		key(evdev.KEY_1, 1),
		syn,
		key(evdev.KEY_A, 1),
	), []ev{key(evdev.KEY_A, 1)})
}

func TestReleaseAll(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	feed(e, key(evdev.KEY_4, 1), key(evdev.KEY_A, 1))

	check(t, project(e.ReleaseAll()), []ev{
		key(evdev.KEY_MINUS, 0), key(evdev.KEY_LEFTSHIFT, 0), key(evdev.KEY_A, 0), syn,
	})

	if got := e.ReleaseAll(); got != nil {
		t.Fatalf("second ReleaseAll = %v", got)
	}
}

func TestInputNotMutated(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	in := evdev.InputEvent{Type: evdev.EV_KEY, Code: evdev.KEY_1, Value: 1}
	e.Process(in)

	if in.Code != evdev.KEY_1 || in.Value != 1 || in.Type != evdev.EV_KEY {
		t.Fatalf("input mutated: %+v", in)
	}
}

func TestSynDroppedReleasesHeldKeys(t *testing.T) {
	t.Parallel()

	e := remap.New(mappings())
	feed(e, key(evdev.KEY_1, 1))

	check(t, feed(e, ev{evdev.EV_SYN, evdev.SYN_DROPPED, 0}), []ev{key(evdev.KEY_6, 0), syn})
	check(t, feed(e, key(evdev.KEY_1, 0), syn), nil)
	check(t, feed(e, key(evdev.KEY_1, 1)), []ev{key(evdev.KEY_6, 1)})
}
