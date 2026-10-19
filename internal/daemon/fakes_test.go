package daemon_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/daemon"
	"github.com/elias-knodel/skb/internal/device"
)

var errFakeInterrupted = errors.New("fake interrupted")

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(2 * time.Millisecond)
	}

	t.Fatal("condition not met within 2s")
}

type fakeSource struct {
	info   device.Info
	events chan evdev.InputEvent
	errs   chan error
	done   chan struct{}

	mu         sync.Mutex
	doneClosed bool
	grabErr    error
	grabbed    bool
	grabs      int
	closed     bool
}

func newFakeSource(info device.Info) *fakeSource {
	return &fakeSource{
		info:   info,
		events: make(chan evdev.InputEvent, 64),
		errs:   make(chan error, 1),
		done:   make(chan struct{}),
	}
}

func (s *fakeSource) Info() device.Info { return s.info }

func (s *fakeSource) ReadEvent() (evdev.InputEvent, error) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()

	select {
	case ev := <-s.events:
		return ev, nil
	case err := <-s.errs:
		return evdev.InputEvent{}, err
	case <-done:
		return evdev.InputEvent{}, errFakeInterrupted
	}
}

func (s *fakeSource) Grab() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.grabErr != nil {
		return s.grabErr
	}

	s.grabbed = true
	s.grabs++

	return nil
}

func (s *fakeSource) Interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.doneClosed {
		s.doneClosed = true

		close(s.done)
	}
}

// reopen prepares the source for another Open: fresh interrupt channel, not closed.
func (s *fakeSource) reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.doneClosed {
		s.done = make(chan struct{})
		s.doneClosed = false
	}

	s.closed = false
}

func (s *fakeSource) grabCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.grabs
}

func (s *fakeSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

func (s *fakeSource) send(typ evdev.EvType, code evdev.EvCode, value int32) {
	s.events <- evdev.InputEvent{Type: typ, Code: code, Value: value}
}

func (s *fakeSource) isGrabbed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.grabbed
}

func (s *fakeSource) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

type fakeSink struct {
	mu       sync.Mutex
	name     string
	caps     map[evdev.EvType][]evdev.EvCode
	id       device.ID
	events   []evdev.InputEvent
	writeErr error
	closed   bool
}

func (s *fakeSink) Write(events []evdev.InputEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writeErr != nil {
		return s.writeErr
	}

	s.events = append(s.events, events...)

	return nil
}

func (s *fakeSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

// triples returns the written events as (type, code, value).
func (s *fakeSink) triples() [][3]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([][3]int, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, [3]int{int(e.Type), int(e.Code), int(e.Value)})
	}

	return out
}

func (s *fakeSink) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

type fakeBackend struct {
	mu       sync.Mutex
	sources  map[string]*fakeSource
	openErrs map[string]error
	opens    map[string]int
	sinks    []*fakeSink
	ids      map[string]daemon.NodeID
	nextIno  uint64
	writeErr error
}

var _ daemon.Backend = (*fakeBackend)(nil)

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		sources:  make(map[string]*fakeSource),
		openErrs: make(map[string]error),
		opens:    make(map[string]int),
		ids:      make(map[string]daemon.NodeID),
	}
}

func (b *fakeBackend) setSource(path string, s *fakeSource) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sources[path] = s
	b.nextIno++
	b.ids[path] = daemon.NodeID{Ino: b.nextIno}
}

func (b *fakeBackend) removePath(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.sources, path)
	delete(b.ids, path)
}

func (b *fakeBackend) setOpenErr(path string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err == nil {
		delete(b.openErrs, path)

		return
	}

	b.openErrs[path] = err
}

func (b *fakeBackend) openCount(path string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.opens[path]
}

func (b *fakeBackend) sinkList() []*fakeSink {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]*fakeSink(nil), b.sinks...)
}

func (b *fakeBackend) ListPaths() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	paths := make([]string, 0, len(b.sources))
	for p := range b.sources {
		paths = append(paths, p)
	}

	return paths, nil
}

func (b *fakeBackend) Open(path string) (daemon.Source, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.opens[path]++

	if err := b.openErrs[path]; err != nil {
		return nil, err
	}

	src, ok := b.sources[path]
	if !ok {
		return nil, errors.New("no such device")
	}

	src.reopen()

	return src, nil
}

func (b *fakeBackend) CreateSink(
	name string,
	id device.ID,
	caps map[evdev.EvType][]evdev.EvCode,
) (daemon.Sink, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := &fakeSink{name: name, id: id, caps: caps, writeErr: b.writeErr}
	b.sinks = append(b.sinks, s)

	return s, nil
}

type fakeWatcher struct {
	ch   chan struct{}
	once sync.Once
}

func newFakeWatcher() *fakeWatcher { return &fakeWatcher{ch: make(chan struct{}, 1)} }

func (w *fakeWatcher) Changes() <-chan struct{} { return w.ch }

func (w *fakeWatcher) Close() error {
	w.once.Do(func() { close(w.ch) })

	return nil
}

func (w *fakeWatcher) signal() {
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// Stat support: setSource assigns a fresh node identity.
func (b *fakeBackend) Stat(path string) (daemon.NodeID, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id, ok := b.ids[path]
	if !ok {
		return daemon.NodeID{}, errors.New("no such node")
	}

	return id, nil
}

func (b *fakeBackend) setWriteErr(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.writeErr = err
}
