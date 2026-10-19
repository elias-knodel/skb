// Package evdevio contains the hardware adapters: inotify watching, evdev
// sources and uinput sinks.
package evdevio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	eventPrefix   = "event"
	inotifyHeader = unix.SizeofInotifyEvent
	readBufSize   = 4096
)

// Watcher reports changes of event nodes in a directory via inotify.
type Watcher struct {
	file    *os.File
	changes chan struct{}
	done    chan struct{}
	once    sync.Once
	err     error
}

// NewWatcher watches dir for creation, removal and attribute changes of event nodes.
func NewWatcher(dir string) (*Watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("inotify init: %w", err)
	}

	mask := uint32(unix.IN_CREATE | unix.IN_DELETE | unix.IN_ATTRIB | unix.IN_MOVED_TO | unix.IN_MOVED_FROM)
	if _, err := unix.InotifyAddWatch(fd, dir, mask); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}

	// A non-blocking fd is registered with the runtime poller, so Close unblocks Read.
	w := &Watcher{
		file:    os.NewFile(uintptr(fd), "inotify"),
		changes: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

	go w.loop()

	return w, nil
}

// Changes returns the coalesced change signals; it is closed by Close.
func (w *Watcher) Changes() <-chan struct{} {
	return w.changes
}

// Close stops the watcher and closes the Changes channel.
func (w *Watcher) Close() error {
	w.once.Do(func() {
		w.err = w.file.Close()

		<-w.done
	})

	return w.err
}

func (w *Watcher) loop() {
	defer close(w.done)
	defer close(w.changes)

	buf := make([]byte, readBufSize)

	for {
		n, err := w.file.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return
			}

			continue
		}

		if relevant(buf[:n]) {
			select {
			case w.changes <- struct{}{}:
			default:
			}
		}
	}
}

// relevant reports whether the inotify records mention an event node.
func relevant(buf []byte) bool {
	for len(buf) >= inotifyHeader {
		nameLen := int(binary.NativeEndian.Uint32(buf[12:16]))

		end := inotifyHeader + nameLen
		if end > len(buf) {
			return false
		}

		name := strings.TrimRight(string(buf[inotifyHeader:end]), "\x00")
		if strings.HasPrefix(name, eventPrefix) {
			return true
		}

		buf = buf[end:]
	}

	return false
}
