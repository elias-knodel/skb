package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/device"
	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/learn"
	"github.com/elias-knodel/skb/internal/remap"
)

const busyHint = "device busy - is input-remapper still grabbing it?"

// Options configures a Daemon.
type Options struct {
	Backend    Backend
	Watcher    Watcher
	Config     *config.Config
	ConfigPath string
	// ConfigSaver persists a changed configuration; nil disables persistence.
	ConfigSaver func(*config.Config) error
	Version     string
	Logger      *slog.Logger
	// Debounce is the quiet period awaited after a watcher signal before rescanning.
	Debounce time.Duration
	// BackoffBase and BackoffMax bound the retry delay after a failure; 1s and 30s when zero.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// StableAfter is the run time after which a worker counts as healthy; 30s when zero.
	StableAfter time.Duration
}

type entryState int

const (
	// stateIgnored marks a device that does not match any entry.
	stateIgnored entryState = iota
	// statePassive marks a matched device that is observed but not grabbed.
	statePassive
	// stateActive marks a grabbed device with a running worker.
	stateActive
	// stateFailed marks a matched device that could not be set up; it is retried.
	stateFailed
)

type entry struct {
	id          NodeID
	state       entryState
	dev         ipc.Device
	w           *worker
	interrupted bool
}

// Daemon grabs matching devices and forwards their remapped events.
type Daemon struct {
	opts    Options
	log     *slog.Logger
	started time.Time
	exited  chan struct{}
	wg      sync.WaitGroup

	// cfg is the live configuration; workers pick up replacements on their next event.
	cfg atomic.Pointer[config.Config]
	// cfgMu serializes configuration changes.
	cfgMu sync.Mutex

	learn learnState

	mu      sync.Mutex
	entries map[string]*entry
	// backoff holds per-path failure state; it survives entry removal.
	backoff map[string]*backoff
}

// New returns a daemon for the given options.
func New(opts Options) *Daemon {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	opts.BackoffBase = cmp.Or(opts.BackoffBase, time.Second)
	opts.BackoffMax = cmp.Or(opts.BackoffMax, 30*time.Second)
	opts.StableAfter = cmp.Or(opts.StableAfter, 30*time.Second)

	d := &Daemon{
		opts:    opts,
		log:     logger,
		started: time.Now(),
		exited:  make(chan struct{}, 1),
		entries: make(map[string]*entry),
		backoff: make(map[string]*backoff),
	}
	d.cfg.Store(opts.Config)
	d.learn.captured = make(chan evdev.EvCode, 1)

	return d
}

// Run reconciles devices on start and on every hotplug signal until ctx is
// cancelled. It returns after all workers have released their devices.
//
// There is no periodic poll: inotify reacts immediately to wired/wireless
// switching and costs nothing while idle. Every signal triggers a full,
// idempotent reconcile, and IN_ATTRIB covers the window before udev applies
// device permissions.
func (d *Daemon) Run(ctx context.Context) error {
	defer d.shutdown()

	var (
		timer  *time.Timer
		timerC <-chan time.Time
	)

	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	// step reconciles and arms the timer for the earliest pending backoff retry.
	step := func() {
		d.reconcile()

		if timer != nil {
			timer.Stop()
		}

		timerC = nil

		if wait, ok := d.nextRetry(); ok {
			timer = time.NewTimer(wait)
			timerC = timer.C
		}
	}

	step()

	changes := d.opts.Watcher.Changes()

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-changes:
			if !ok {
				changes = nil

				d.log.Warn("device watcher closed")

				continue
			}

			if !d.debounce(ctx, changes) {
				return nil
			}

			step()
		case <-d.exited:
			step()
		case <-timerC:
			step()
		}
	}
}

// debounce drains further signals until the quiet period elapses.
// It returns false when ctx ends first.
func (d *Daemon) debounce(ctx context.Context, changes <-chan struct{}) bool {
	if d.opts.Debounce <= 0 {
		return true
	}

	timer := time.NewTimer(d.opts.Debounce)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-changes:
			timer.Reset(d.opts.Debounce)
		case <-timer.C:
			return true
		}
	}
}

func (d *Daemon) shutdown() {
	d.mu.Lock()

	for _, e := range d.entries {
		if e.w != nil {
			e.w.src.Interrupt()
		}
	}

	d.mu.Unlock()

	d.wg.Wait()
}

// Status returns a snapshot of the daemon state.
func (d *Daemon) Status() ipc.Status {
	st := ipc.Status{
		Version:    d.opts.Version,
		StartedAt:  d.started,
		ConfigPath: d.opts.ConfigPath,
		Devices:    []ipc.Device{},
		Mappings:   mappingList(d.cfg.Load()),
		AdminUID:   d.AdminUID(),
		Learning:   d.learn.active.Load(),
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, e := range d.entries {
		if e.state != stateIgnored {
			st.Devices = append(st.Devices, e.dev)
		}
	}

	slices.SortFunc(st.Devices, func(a, b ipc.Device) int {
		switch {
		case a.Path < b.Path:
			return -1
		case a.Path > b.Path:
			return 1
		default:
			return 0
		}
	})

	return st
}

// reconcile brings the set of handled devices in line with the input nodes.
func (d *Daemon) reconcile() {
	paths, err := d.opts.Backend.ListPaths()
	if err != nil {
		d.log.Error("list input nodes", "error", err)

		return
	}

	present := make(map[string]bool, len(paths))
	for _, p := range paths {
		present[p] = true
	}

	d.dropVanished(present)

	for _, path := range paths {
		d.mu.Lock()
		e, known := d.entries[path]
		d.mu.Unlock()

		if known && e.state == stateActive {
			continue
		}

		// A replaced node (path reuse) invalidates the cached classification.
		if known {
			if id, err := d.opts.Backend.Stat(path); err == nil && id != e.id {
				d.forget(path)

				known = false
			}
		}

		if known && e.state != stateFailed {
			continue
		}

		if d.blocked(path) {
			continue
		}

		d.handle(path)
	}
}

// dropVanished forgets entries without a node and interrupts their workers.
func (d *Daemon) dropVanished(present map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for path := range d.backoff {
		if !present[path] {
			delete(d.backoff, path)
		}
	}

	for path, e := range d.entries {
		if present[path] {
			continue
		}

		if e.w == nil {
			delete(d.entries, path)

			continue
		}

		if !e.interrupted {
			e.interrupted = true

			e.w.src.Interrupt()
		}
	}
}

// handle opens, classifies and, when required, grabs one device.
// forget drops the cached entry and failure state of path.
func (d *Daemon) forget(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.entries, path)
	delete(d.backoff, path)
}

func (d *Daemon) handle(path string) {
	// Stat before Open, so a swap during classification is detected on the next pass.
	id, _ := d.opts.Backend.Stat(path)
	src, err := d.opts.Backend.Open(path)
	if err != nil {
		d.log.Warn("open input node", "path", path, "error", err)

		return
	}

	info := src.Info()
	decision := device.Classify(info, d.cfg.Load().Devices)

	switch {
	case !decision.Matched:
		_ = src.Close()
		d.setEntry(path, &entry{id: id, state: stateIgnored})
	case !decision.Grab:
		_ = src.Close()
		d.setEntry(path, &entry{id: id, state: statePassive, dev: deviceStatus(info, decision)})
		d.log.Info("device matched, nothing to grab", "path", path, "name", info.Name, "reason", decision.Reason)
	default:
		d.activate(path, id, src, info, decision)
	}
}

func (d *Daemon) activate(path string, id NodeID, src Source, info device.Info, decision device.Decision) {
	status := deviceStatus(info, decision)

	fail := func(err error) {
		status.Error = err.Error()
		d.setEntry(path, &entry{id: id, state: stateFailed, dev: status})
		d.markFailure(path, 0)
		d.log.Error("cannot grab device", "path", path, "name", info.Name, "error", err)
	}

	sink, err := d.opts.Backend.CreateSink(
		info.Name, device.VirtualID(), remap.OutputCaps(info.Caps),
	)
	if err != nil {
		_ = src.Close()

		fail(fmt.Errorf("create virtual device: %w", err))

		return
	}

	if err := src.Grab(); err != nil {
		_ = sink.Close()
		_ = src.Close()

		if errors.Is(err, syscall.EBUSY) {
			err = fmt.Errorf("%w: %s", err, busyHint)
		}

		fail(err)

		return
	}

	status.Grabbed = true
	status.VirtualName = device.VirtualName(info.Name)
	status.Since = time.Now()

	cfg := d.cfg.Load()
	w := &worker{
		d: d, path: path, src: src, sink: sink,
		cfg: cfg, engine: remap.New(cfg.Mappings), filter: learn.NewSourceFilter(),
	}

	d.setEntry(path, &entry{id: id, state: stateActive, dev: status, w: w})
	d.log.Info("device grabbed", "path", path, "name", info.Name, "matcher", decision.Matcher)

	d.wg.Go(func() {
		d.workerDone(path, w, w.run(d.log))
	})
}

func (d *Daemon) setEntry(path string, e *entry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries[path] = e
}

// workerDone removes the finished worker's entry and requests a reconcile.
func (d *Daemon) workerDone(path string, w *worker, exitErr error) {
	d.mu.Lock()

	var (
		failed bool
		ran    time.Duration
	)

	if e, ok := d.entries[path]; ok && e.w == w {
		// Unplugging (ENODEV) is expected and must not delay a device reusing the path.
		failed = !e.interrupted && !errors.Is(exitErr, syscall.ENODEV)
		ran = time.Since(e.dev.Since)

		delete(d.entries, path)
	}

	d.mu.Unlock()

	// An unrequested stop must not restart in a tight loop.
	if failed {
		d.markFailure(path, ran)
	}

	select {
	case d.exited <- struct{}{}:
	default:
	}
}

func deviceStatus(info device.Info, decision device.Decision) ipc.Device {
	return ipc.Device{
		Path:    info.Path,
		Name:    info.Name,
		Vendor:  info.ID.Vendor,
		Product: info.ID.Product,
		Matcher: decision.Matcher,
	}
}
