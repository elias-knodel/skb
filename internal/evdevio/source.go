package evdevio

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/device"
)

// ErrInterrupted is returned by ReadEvent after Interrupt.
var ErrInterrupted = errors.New("read interrupted")

const (
	grabWait = time.Second
	grabStep = 20 * time.Millisecond
)

// source is an evdev device that can be read, grabbed and interrupted.
//
// go-evdev exposes no descriptor, and every ioctl wrapper calls os.File.Fd(),
// which switches the descriptor to blocking mode. NonBlock restores
// non-blocking mode, after which a pending read is parked in the Go poller and
// is released by closing the file. ReadEvent therefore sets it up front and no
// ioctl wrapper (State, Grab, Ungrab) may run while a reader is active.
type source struct {
	dev  *evdev.InputDevice
	info device.Info

	mu          sync.Mutex
	closed      bool
	grabbed     bool
	nonblocking bool
}

func openSource(path string) (*source, error) {
	dev, err := evdev.OpenWithFlags(path, os.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	info, err := readInfo(dev, path)
	if err != nil {
		_ = dev.Close()

		return nil, err
	}

	return &source{dev: dev, info: info}, nil
}

func readInfo(dev *evdev.InputDevice, path string) (device.Info, error) {
	name, err := dev.Name()
	if err != nil {
		return device.Info{}, fmt.Errorf("read name of %s: %w", path, err)
	}

	id, err := dev.InputID()
	if err != nil {
		return device.Info{}, fmt.Errorf("read id of %s: %w", path, err)
	}

	caps := make(map[evdev.EvType][]evdev.EvCode)
	for _, typ := range dev.CapableTypes() {
		caps[typ] = dev.CapableEvents(typ)
	}

	return device.Info{
		Path: path,
		Name: name,
		ID:   device.ID{Bus: id.BusType, Vendor: id.Vendor, Product: id.Product, Version: id.Version},
		Caps: caps,
	}, nil
}

func (s *source) Info() device.Info { return s.info }

// ReadEvent blocks until the next event. It returns ErrInterrupted once
// Interrupt has been called.
func (s *source) ReadEvent() (evdev.InputEvent, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()

	if closed {
		return evdev.InputEvent{}, ErrInterrupted
	}

	if !s.nonblocking {
		if err := s.dev.NonBlock(); err != nil {
			return evdev.InputEvent{}, fmt.Errorf("set non-blocking: %w", err)
		}

		s.nonblocking = true
	}

	ev, err := s.dev.ReadOne()
	if err != nil {
		if errors.Is(err, os.ErrClosed) {
			return evdev.InputEvent{}, ErrInterrupted
		}

		return evdev.InputEvent{}, fmt.Errorf("read event: %w", err)
	}

	return *ev, nil
}

// Grab waits briefly for held keys to be released, then grabs the device.
// Grabbing with keys down would leave them stuck in the compositor.
func (s *source) Grab() error {
	for deadline := time.Now().Add(grabWait); time.Now().Before(deadline) && s.keysPressed(); {
		time.Sleep(grabStep)
	}

	if err := s.dev.Grab(); err != nil {
		return fmt.Errorf("grab %s: %w", s.info.Path, err)
	}

	s.mu.Lock()
	s.grabbed = true
	s.mu.Unlock()

	return nil
}

func (s *source) keysPressed() bool {
	state, err := s.dev.State(evdev.EV_KEY)
	if err != nil {
		return false
	}

	for _, down := range state {
		if down {
			return true
		}
	}

	return false
}

// Interrupt unblocks a pending ReadEvent by closing the device. The kernel
// drops the grab together with the file.
func (s *source) Interrupt() {
	_ = s.release(false)
}

// Close ungrabs and closes the device.
func (s *source) Close() error {
	return s.release(true)
}

func (s *source) release(ungrab bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	var err error

	if ungrab && s.grabbed {
		if uerr := s.dev.Ungrab(); uerr != nil && !errors.Is(uerr, syscall.ENODEV) {
			err = uerr
		}
	}

	return errors.Join(err, s.dev.Close())
}
