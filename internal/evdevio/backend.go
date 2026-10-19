package evdevio

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/daemon"
	"github.com/elias-knodel/skb/internal/device"
)

// DefaultDir is the directory holding the input event nodes.
const DefaultDir = "/dev/input"

var eventNode = regexp.MustCompile(`^event\d+$`)

// Backend implements daemon.Backend on top of evdev and uinput.
type Backend struct {
	// Dir is the input node directory; DefaultDir when empty.
	Dir string
}

var _ daemon.Backend = Backend{}

func (b Backend) dir() string {
	if b.Dir == "" {
		return DefaultDir
	}

	return b.Dir
}

// ListPaths returns the sorted event node paths. Nodes are not opened.
func (b Backend) ListPaths() ([]string, error) {
	entries, err := os.ReadDir(b.dir())
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", b.dir(), err)
	}

	var paths []string

	for _, e := range entries {
		if eventNode.MatchString(e.Name()) && !e.IsDir() {
			paths = append(paths, filepath.Join(b.dir(), e.Name()))
		}
	}

	slices.Sort(paths)

	return paths, nil
}

// Open opens the event node at path.
func (b Backend) Open(path string) (daemon.Source, error) {
	return openSource(path)
}

// CreateSink creates a uinput device.
func (b Backend) CreateSink(
	name string,
	id device.ID,
	caps map[evdev.EvType][]evdev.EvCode,
) (daemon.Sink, error) {
	return createSink(name, id, caps)
}

// Stat returns the inode and device number of the node at path.
func (b Backend) Stat(path string) (daemon.NodeID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return daemon.NodeID{}, fmt.Errorf("stat %s: %w", path, err)
	}

	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return daemon.NodeID{}, fmt.Errorf("stat %s: unsupported platform", path)
	}

	return daemon.NodeID{Ino: st.Ino, Rdev: st.Rdev}, nil
}
