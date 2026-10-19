package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/device"
	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/keys"
	"github.com/elias-knodel/skb/internal/learn"
)

var _ ipc.Handler = (*Daemon)(nil)

// learnState coordinates learn sessions with the workers.
type learnState struct {
	mu sync.Mutex
	// active reports a running session; armed makes workers capture the next key.
	active atomic.Bool
	armed  atomic.Bool
	// captured carries the learned source key from a worker to the session.
	captured chan evdev.EvCode
}

// deliver hands a captured key to the session without blocking.
func (l *learnState) deliver(code evdev.EvCode) {
	select {
	case l.captured <- code:
	default:
	}
}

// begin claims the session; ok is false when another one runs.
func (l *learnState) begin() bool {
	return l.mu.TryLock()
}

func (l *learnState) end() {
	l.armed.Store(false)
	l.active.Store(false)
	l.mu.Unlock()
}

func apiErr(code, msg string) *ipc.Error { return &ipc.Error{Code: code, Message: msg} }

// errCancelled is returned when the requesting connection goes away.
func errCancelled() error { return apiErr(ipc.CodeCancelled, "cancelled") }

// AdminUID returns the configured admin uid, if any.
func (d *Daemon) AdminUID() *uint32 { return d.cfg.Load().AdminUID }

func mappingList(cfg *config.Config) []ipc.Mapping {
	out := make([]ipc.Mapping, 0, len(cfg.Mappings))

	for _, m := range cfg.Mappings {
		to := make([]string, 0, len(m.Modifiers)+1)
		for _, code := range m.Outputs() {
			to = append(to, keys.Name(code))
		}

		out = append(out, ipc.Mapping{From: keys.Name(m.From), To: to})
	}

	return out
}

// LearnSource waits for the next key press on a grabbed device. The press
// and its release are withheld from the virtual device.
func (d *Daemon) LearnSource(ctx context.Context, timeout time.Duration) (string, error) {
	if !d.learn.begin() {
		return "", apiErr(ipc.CodeBusy, "a learn session is already running")
	}

	defer d.learn.end()

	if !d.hasActiveWorker() {
		return "", apiErr(ipc.CodeNoDevice, "no grabbed device")
	}

	select { // drop a stale capture
	case <-d.learn.captured:
	default:
	}

	d.log.Info("learn source started")
	d.learn.armed.Store(true)
	d.learn.active.Store(true)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case code := <-d.learn.captured:
		d.log.Info("learn source finished", "outcome", "captured")

		return keys.Name(code), nil
	case <-timer.C:
		d.log.Info("learn source finished", "outcome", "timeout")

		return "", apiErr(ipc.CodeTimeout, "no key pressed")
	case <-ctx.Done():
		d.log.Info("learn source finished", "outcome", "cancelled")

		return "", errCancelled()
	}
}

func (d *Daemon) hasActiveWorker() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, e := range d.entries {
		if e.state == stateActive {
			return true
		}
	}

	return false
}

// LearnTarget reads every keyboard that is not grabbed, without grabbing it,
// and returns the first key or combination pressed.
func (d *Daemon) LearnTarget(ctx context.Context, timeout time.Duration) ([]string, error) {
	if !d.learn.begin() {
		return nil, apiErr(ipc.CodeBusy, "a learn session is already running")
	}

	defer d.learn.end()

	sources := d.openLearnSources()
	if len(sources) == 0 {
		return nil, apiErr(ipc.CodeNoDevice, "no keyboard available")
	}

	d.log.Info("learn target started")
	d.learn.active.Store(true)

	events := make(chan evdev.InputEvent)
	done := make(chan struct{})

	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Go(func() {
			for {
				ev, err := src.ReadEvent()
				if err != nil {
					return
				}

				select {
				case events <- ev:
				case <-done:
					return
				}
			}
		})
	}

	stop := func() {
		close(done)

		for _, src := range sources {
			src.Interrupt()
			_ = src.Close()
		}

		wg.Wait()
	}
	defer stop()

	capture := learn.NewComboCapture()
	timer := time.NewTimer(timeout)

	defer timer.Stop()

	for {
		select {
		case ev := <-events:
			if to, finished := capture.Feed(ev); finished {
				d.log.Info("learn target finished", "outcome", "captured")

				return codeNames(to), nil
			}
		case <-timer.C:
			d.log.Info("learn target finished", "outcome", "timeout")

			return nil, apiErr(ipc.CodeTimeout, "no key pressed")
		case <-ctx.Done():
			d.log.Info("learn target finished", "outcome", "cancelled")

			return nil, errCancelled()
		}
	}
}

// openLearnSources opens all non-grabbed physical keyboards.
func (d *Daemon) openLearnSources() []Source {
	paths, err := d.opts.Backend.ListPaths()
	if err != nil {
		d.log.Error("list input nodes", "error", err)

		return nil
	}

	var sources []Source

	for _, path := range paths {
		d.mu.Lock()
		e, known := d.entries[path]
		active := known && e.state == stateActive
		d.mu.Unlock()

		if active {
			continue
		}

		src, err := d.opts.Backend.Open(path)
		if err != nil {
			continue
		}

		if !isPhysicalKeyboard(src.Info()) {
			_ = src.Close()

			continue
		}

		sources = append(sources, src)
	}

	return sources
}

func isPhysicalKeyboard(info device.Info) bool {
	if info.ID.Bus == evdev.BUS_VIRTUAL {
		return false
	}

	return slices.ContainsFunc(info.Caps[evdev.EV_KEY], keys.IsKeyboardKey)
}

func codeNames(codes []evdev.EvCode) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, keys.Name(c))
	}

	return out
}

// SetMapping validates, persists and activates a mapping. The configuration
// is saved before it is activated; on a save failure nothing changes.
func (d *Daemon) SetMapping(_ context.Context, args ipc.SetMappingArgs) ([]ipc.Mapping, error) {
	m, oldFrom, err := parseMapping(args)
	if err != nil {
		return nil, err
	}

	return d.mutate(func(cur *config.Config) (*config.Config, error) {
		return cur.WithMapping(m, oldFrom)
	})
}

// DeleteMapping removes the mapping for from.
func (d *Daemon) DeleteMapping(_ context.Context, from string) ([]ipc.Mapping, error) {
	code, err := keys.Parse(from)
	if err != nil {
		return nil, apiErr(ipc.CodeInvalid, err.Error())
	}

	return d.mutate(func(cur *config.Config) (*config.Config, error) {
		return cur.WithoutMapping(code)
	})
}

func parseMapping(args ipc.SetMappingArgs) (config.Mapping, evdev.EvCode, error) {
	from, err := keys.Parse(args.From)
	if err != nil {
		return config.Mapping{}, 0, apiErr(ipc.CodeInvalid, err.Error())
	}

	if len(args.To) == 0 {
		return config.Mapping{}, 0, apiErr(ipc.CodeInvalid, "to must not be empty")
	}

	codes := make([]evdev.EvCode, 0, len(args.To))

	for _, name := range args.To {
		code, err := keys.Parse(name)
		if err != nil {
			return config.Mapping{}, 0, apiErr(ipc.CodeInvalid, err.Error())
		}

		codes = append(codes, code)
	}

	last := len(codes) - 1
	m := config.Mapping{From: from, Key: codes[last]}

	if last > 0 {
		m.Modifiers = codes[:last]
	}

	var oldFrom evdev.EvCode

	if args.OldFrom != "" {
		if oldFrom, err = keys.Parse(args.OldFrom); err != nil {
			return config.Mapping{}, 0, apiErr(ipc.CodeInvalid, err.Error())
		}
	}

	return m, oldFrom, nil
}

// mutate derives, saves and activates a new configuration.
func (d *Daemon) mutate(change func(*config.Config) (*config.Config, error)) ([]ipc.Mapping, error) {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()

	next, err := change(d.cfg.Load())
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, apiErr(ipc.CodeNotFound, err.Error())
		}

		return nil, apiErr(ipc.CodeInvalid, err.Error())
	}

	if d.opts.ConfigSaver != nil {
		if err := d.opts.ConfigSaver(next); err != nil {
			d.log.Error("save config", "error", fmt.Sprint(err))

			return nil, apiErr(ipc.CodeInternal, "could not save the configuration")
		}
	}

	d.cfg.Store(next)

	return mappingList(next), nil
}
