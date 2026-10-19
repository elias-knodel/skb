package daemon

import (
	"log/slog"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/learn"
	"github.com/elias-knodel/skb/internal/remap"
)

// worker pumps events from one grabbed source through an engine into its sink.
type worker struct {
	d      *Daemon
	path   string
	src    Source
	sink   Sink
	cfg    *config.Config
	engine *remap.Engine
	filter *learn.SourceFilter
}

// run blocks until the source fails or is interrupted, then releases every
// held key and closes sink and source. It returns the error that ended the loop.
func (w *worker) run(log *slog.Logger) error {
	var exitErr error

	for {
		ev, err := w.src.ReadEvent()
		if err != nil {
			exitErr = err

			log.Info("device stopped", "path", w.path, "reason", err.Error())

			break
		}

		if err := w.reload(); err != nil {
			exitErr = err

			log.Error("write to virtual device", "path", w.path, "error", err)

			break
		}

		pass, code, captured := w.filter.Filter(ev, w.d.learn.armed.Load())
		if captured {
			w.d.learn.deliver(code)
		}

		if !pass {
			continue
		}

		out := w.engine.Process(ev)
		if len(out) == 0 {
			continue
		}

		if err := w.sink.Write(out); err != nil {
			exitErr = err

			log.Error("write to virtual device", "path", w.path, "error", err)

			break
		}
	}

	if release := w.engine.ReleaseAll(); len(release) > 0 {
		if err := w.sink.Write(release); err != nil {
			log.Warn("release held keys", "path", w.path, "error", err)
		}
	}

	_ = w.sink.Close()
	_ = w.src.Close()

	return exitErr
}

// reload swaps the engine when the live configuration was replaced. The old
// engine's held outputs are released first, so no key stays down.
func (w *worker) reload() error {
	cur := w.d.cfg.Load()
	if cur == w.cfg {
		return nil
	}

	if release := w.engine.ReleaseAll(); len(release) > 0 {
		if err := w.sink.Write(release); err != nil {
			return err //nolint:wrapcheck // logged by the caller
		}
	}

	w.cfg = cur
	w.engine = remap.New(cur.Mappings)

	return nil
}
