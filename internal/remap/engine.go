// Package remap implements the pure event remapping engine.
package remap

import (
	"slices"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
)

// Key event values.
const (
	valueRelease int32 = 0
	valuePress   int32 = 1
	valueRepeat  int32 = 2
)

// Engine rewrites a stream of input events. It is not safe for concurrent
// use; run one engine per device.
type Engine struct {
	mappings map[evdev.EvCode]config.Mapping
	// refs counts the holders of each output key; events are emitted on 0->1 and 1->0.
	refs map[evdev.EvCode]int
	// active lists pressed mapped source keys in press order.
	active []evdev.EvCode
	// passthrough holds unmapped keys currently pressed by the device.
	passthrough map[evdev.EvCode]bool
	dropping    bool
}

// New returns an engine for the given mappings.
func New(mappings []config.Mapping) *Engine {
	m := make(map[evdev.EvCode]config.Mapping, len(mappings))
	for _, mapping := range mappings {
		m[mapping.From] = mapping
	}

	return &Engine{
		mappings:    m,
		refs:        make(map[evdev.EvCode]int),
		passthrough: make(map[evdev.EvCode]bool),
	}
}

// Process translates one input event into zero or more output events.
func (e *Engine) Process(in evdev.InputEvent) []evdev.InputEvent {
	if in.Type == evdev.EV_SYN && in.Code == evdev.SYN_DROPPED {
		// State is lost with the dropped events; release everything held.
		e.dropping = true

		return e.ReleaseAll()
	}

	if e.dropping {
		if in.Type == evdev.EV_SYN && in.Code == evdev.SYN_REPORT {
			e.dropping = false
		}

		return nil
	}

	switch in.Type {
	case evdev.EV_KEY:
		return e.processKey(in)
	case evdev.EV_SYN, evdev.EV_REL, evdev.EV_ABS, evdev.EV_MSC, evdev.EV_SW:
		return []evdev.InputEvent{in}
	default:
		return nil
	}
}

// ReleaseAll releases every output key still held and resets the engine.
func (e *Engine) ReleaseAll() []evdev.InputEvent {
	var out []evdev.InputEvent

	ref := evdev.InputEvent{Type: evdev.EV_KEY}

	for _, from := range e.active {
		m := e.mappings[from]
		out = append(out, e.releaseOutputs(ref, m, false)...)
	}

	e.active = nil

	remaining := make([]evdev.EvCode, 0, len(e.refs))
	for code, n := range e.refs {
		if n > 0 {
			remaining = append(remaining, code)
		}
	}

	slices.Sort(remaining)

	for _, code := range remaining {
		out = append(out, keyEvent(ref, code, valueRelease))
	}

	clear(e.refs)
	clear(e.passthrough)

	if len(out) == 0 {
		return nil
	}

	return append(out, synEvent(ref))
}

func (e *Engine) processKey(in evdev.InputEvent) []evdev.InputEvent {
	if m, ok := e.mappings[in.Code]; ok {
		return e.processMapped(in, m)
	}

	switch in.Value {
	case valuePress:
		if e.passthrough[in.Code] {
			return nil
		}

		e.passthrough[in.Code] = true

		return e.press(in, in.Code)
	case valueRepeat:
		if !e.passthrough[in.Code] {
			return nil
		}

		return []evdev.InputEvent{in}
	case valueRelease:
		if !e.passthrough[in.Code] {
			return nil
		}

		delete(e.passthrough, in.Code)

		return e.release(in, in.Code)
	default:
		return nil
	}
}

func (e *Engine) processMapped(in evdev.InputEvent, m config.Mapping) []evdev.InputEvent {
	pressed := slices.Contains(e.active, m.From)

	switch in.Value {
	case valuePress:
		if pressed {
			return nil
		}

		e.active = append(e.active, m.From)

		var out []evdev.InputEvent
		for _, mod := range m.Modifiers {
			out = append(out, e.press(in, mod)...)
		}

		if len(out) > 0 {
			out = append(out, synEvent(in))
		}

		return append(out, e.press(in, m.Key)...)
	case valueRepeat:
		if !pressed {
			return nil
		}

		return []evdev.InputEvent{keyEvent(in, m.Key, valueRepeat)}
	case valueRelease:
		if !pressed {
			return nil
		}

		e.active = slices.DeleteFunc(e.active, func(c evdev.EvCode) bool { return c == m.From })

		return e.releaseOutputs(in, m, true)
	default:
		return nil
	}
}

// releaseOutputs releases the main key, then the modifiers in reverse order.
// With frame set, a SYN_REPORT separates the main key from emitted modifier releases.
func (e *Engine) releaseOutputs(ref evdev.InputEvent, m config.Mapping, frame bool) []evdev.InputEvent {
	out := e.release(ref, m.Key)

	var mods []evdev.InputEvent
	for _, mod := range slices.Backward(m.Modifiers) {
		mods = append(mods, e.release(ref, mod)...)
	}

	if len(mods) > 0 {
		if frame && len(out) > 0 {
			out = append(out, synEvent(ref))
		}

		out = append(out, mods...)
	}

	return out
}

func (e *Engine) press(ref evdev.InputEvent, code evdev.EvCode) []evdev.InputEvent {
	e.refs[code]++
	if e.refs[code] != 1 {
		return nil
	}

	return []evdev.InputEvent{keyEvent(ref, code, valuePress)}
}

func (e *Engine) release(ref evdev.InputEvent, code evdev.EvCode) []evdev.InputEvent {
	if e.refs[code] == 0 {
		return nil
	}

	e.refs[code]--
	if e.refs[code] != 0 {
		return nil
	}

	delete(e.refs, code)

	return []evdev.InputEvent{keyEvent(ref, code, valueRelease)}
}

func keyEvent(ref evdev.InputEvent, code evdev.EvCode, value int32) evdev.InputEvent {
	return evdev.InputEvent{Time: ref.Time, Type: evdev.EV_KEY, Code: code, Value: value}
}

func synEvent(ref evdev.InputEvent) evdev.InputEvent {
	return evdev.InputEvent{Time: ref.Time, Type: evdev.EV_SYN, Code: evdev.SYN_REPORT}
}
