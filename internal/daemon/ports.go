// Package daemon orchestrates device discovery, grabbing, remapping and hotplug.
package daemon

import (
	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/device"
)

// Source is an opened input device.
type Source interface {
	// Info describes the device.
	Info() device.Info
	// ReadEvent blocks until the next event, an error, or Interrupt.
	ReadEvent() (evdev.InputEvent, error)
	// Grab takes exclusive access to the device.
	Grab() error
	// Interrupt unblocks a pending ReadEvent. It is safe to call concurrently.
	Interrupt()
	// Close releases the device, ungrabbing it if necessary.
	Close() error
}

// Sink is a virtual output device.
type Sink interface {
	// Write emits the events in order.
	Write(events []evdev.InputEvent) error
	// Close destroys the virtual device.
	Close() error
}

// NodeID identifies a device node instance; devtmpfs allocates a new inode per node.
type NodeID struct {
	Ino, Rdev uint64
}

// Backend provides access to input devices.
type Backend interface {
	// ListPaths returns the current input event node paths.
	ListPaths() ([]string, error)
	// Stat returns the identity of the node at path.
	Stat(path string) (NodeID, error)
	// Open opens the device at path.
	Open(path string) (Source, error)
	// CreateSink creates a virtual device derived from the source name.
	CreateSink(name string, id device.ID, caps map[evdev.EvType][]evdev.EvCode) (Sink, error)
}

// Watcher signals possible changes of the set of input nodes.
type Watcher interface {
	// Changes delivers a coalesced signal per change; it is closed by Close.
	Changes() <-chan struct{}
	// Close stops the watcher.
	Close() error
}
