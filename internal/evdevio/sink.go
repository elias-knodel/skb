package evdevio

import (
	"errors"
	"fmt"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/device"
)

type sink struct {
	dev *evdev.InputDevice
}

func createSink(name string, id device.ID, caps map[evdev.EvType][]evdev.EvCode) (*sink, error) {
	dev, err := evdev.CreateDevice(device.VirtualName(name), evdev.InputID{
		BusType: id.Bus,
		Vendor:  id.Vendor,
		Product: id.Product,
		Version: id.Version,
	}, caps)
	if err != nil {
		return nil, fmt.Errorf("create virtual device: %w", err)
	}

	return &sink{dev: dev}, nil
}

// Write emits the events one by one.
func (s *sink) Write(events []evdev.InputEvent) error {
	for i := range events {
		if err := s.dev.WriteOne(&events[i]); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
	}

	return nil
}

// Close destroys the virtual device and closes its file.
func (s *sink) Close() error {
	return errors.Join(evdev.DestroyDevice(s.dev), s.dev.Close())
}
