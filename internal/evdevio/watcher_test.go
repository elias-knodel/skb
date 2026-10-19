package evdevio_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elias-knodel/skb/internal/evdevio"
)

func newWatcher(t *testing.T) (*evdevio.Watcher, string) {
	t.Helper()

	dir := t.TempDir()

	w, err := evdevio.NewWatcher(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })

	return w, dir
}

func expectSignal(t *testing.T, w *evdevio.Watcher, within time.Duration) bool {
	t.Helper()

	select {
	case _, ok := <-w.Changes():
		return ok
	case <-time.After(within):
		return false
	}
}

func create(t *testing.T, dir, name string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestWatcherCreate(t *testing.T) {
	t.Parallel()

	w, dir := newWatcher(t)
	create(t, dir, "event7")

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal for event7")
	}
}

func TestWatcherIgnoresOtherNodes(t *testing.T) {
	t.Parallel()

	w, dir := newWatcher(t)
	create(t, dir, "js0")

	if expectSignal(t, w, 200*time.Millisecond) {
		t.Fatal("unexpected signal for js0")
	}
}

func TestWatcherRemove(t *testing.T) {
	t.Parallel()

	w, dir := newWatcher(t)
	path := create(t, dir, "event7")

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal for create")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal for remove")
	}
}

func TestWatcherAttrib(t *testing.T) {
	t.Parallel()

	w, dir := newWatcher(t)
	path := create(t, dir, "event7")

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal for create")
	}

	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal for chmod")
	}
}

func TestWatcherClose(t *testing.T) {
	t.Parallel()

	w, _ := newWatcher(t)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case _, ok := <-w.Changes():
		if ok {
			t.Fatal("unexpected signal")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed")
	}
}

func TestWatcherBurst(t *testing.T) {
	t.Parallel()

	w, dir := newWatcher(t)

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := range 50 {
			create(t, dir, "event"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("burst blocked")
	}

	if !expectSignal(t, w, time.Second) {
		t.Fatal("no signal after burst")
	}
}
