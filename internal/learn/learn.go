// Package learn contains the pure state machines that capture a source key
// and a target key combination.
package learn

import (
	"slices"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/keys"
)

// Key event values.
const (
	valueRelease int32 = 0
	valuePress   int32 = 1
	valueRepeat  int32 = 2
)

// SourceFilter captures the next key press of a grabbed device while armed
// and keeps it away from the output until it is released.
type SourceFilter struct {
	held      map[evdev.EvCode]bool
	swallowed map[evdev.EvCode]bool
}

// NewSourceFilter returns a filter with no keys held.
func NewSourceFilter() *SourceFilter {
	return &SourceFilter{
		held:      make(map[evdev.EvCode]bool),
		swallowed: make(map[evdev.EvCode]bool),
	}
}

// Filter classifies one event. pass reports whether the event continues to
// the remapping engine; captured reports that code is a newly learned key.
// Held keys are tracked while disarmed, so a key pressed before arming is
// never captured.
func (f *SourceFilter) Filter(ev evdev.InputEvent, armed bool) (pass bool, code evdev.EvCode, captured bool) {
	if ev.Type != evdev.EV_KEY || keys.IsPrimaryPointerButton(ev.Code) {
		return true, 0, false
	}

	switch ev.Value {
	case valuePress:
		wasHeld := f.held[ev.Code]
		f.held[ev.Code] = true

		if f.swallowed[ev.Code] {
			return false, 0, false
		}

		if armed && !wasHeld {
			f.swallowed[ev.Code] = true

			return false, ev.Code, true
		}

		return true, 0, false
	case valueRepeat:
		return !f.swallowed[ev.Code], 0, false
	case valueRelease:
		delete(f.held, ev.Code)

		if f.swallowed[ev.Code] {
			delete(f.swallowed, ev.Code)

			return false, 0, false
		}

		return true, 0, false
	default:
		return true, 0, false
	}
}

// ComboCapture turns key events into a modifier chord plus main key, or a
// single lone modifier.
type ComboCapture struct {
	// down holds the keys pressed during this session.
	down map[evdev.EvCode]bool
	// held holds the modifiers currently down, in press order.
	held []evdev.EvCode
	// seen counts the distinct modifiers pressed since the last full release.
	seen int
}

// NewComboCapture returns an empty capture.
func NewComboCapture() *ComboCapture {
	return &ComboCapture{down: make(map[evdev.EvCode]bool)}
}

// Feed processes one event. When done is true, to holds the captured codes:
// modifiers first, the main key last.
func (c *ComboCapture) Feed(ev evdev.InputEvent) (to []evdev.EvCode, done bool) {
	if ev.Type != evdev.EV_KEY || !keys.IsKeyboardKey(ev.Code) {
		return nil, false
	}

	switch ev.Value {
	case valuePress:
		c.down[ev.Code] = true

		if keys.IsModifier(ev.Code) {
			if !slices.Contains(c.held, ev.Code) {
				c.held = append(c.held, ev.Code)
				c.seen++
			}

			return nil, false
		}

		return append(keys.SortModifiers(c.held), ev.Code), true
	case valueRelease:
		if !c.down[ev.Code] {
			return nil, false
		}

		delete(c.down, ev.Code)

		if !keys.IsModifier(ev.Code) {
			return nil, false
		}

		if c.seen == 1 {
			return []evdev.EvCode{ev.Code}, true
		}

		c.held = slices.DeleteFunc(c.held, func(m evdev.EvCode) bool { return m == ev.Code })
		if len(c.held) == 0 {
			c.seen = 0
		}

		return nil, false
	default:
		return nil, false
	}
}
