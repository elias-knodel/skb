package evdevio_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/elias-knodel/skb/internal/evdevio"
)

func TestListPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{"event10", "event1", "mouse0"} {
		create(t, dir, name)
	}

	if err := os.Mkdir(filepath.Join(dir, "by-id"), 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := evdevio.Backend{Dir: dir}.ListPaths()
	if err != nil {
		t.Fatal(err)
	}

	want := []string{filepath.Join(dir, "event1"), filepath.Join(dir, "event10")}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}
