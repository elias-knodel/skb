package daemon_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/daemon"
	"github.com/elias-knodel/skb/internal/device"
	"github.com/elias-knodel/skb/internal/ipc"
)

const (
	wiredKbd    = "/dev/input/event5"
	wiredMouse  = "/dev/input/event6"
	k100        = "/dev/input/event3"
	receiverKbd = "/dev/input/event8"
)

func testConfig() *config.Config {
	return &config.Config{
		Devices: []config.DeviceMatcher{
			{Name: "Darkstar wired", Vendor: 0x1b1c, Product: 0x1bb2},
			{Name: "Darkstar Slipstream receiver", Vendor: 0x1b1c, Product: 0x1bdc},
		},
		Mappings: []config.Mapping{
			{From: evdev.KEY_1, Key: evdev.KEY_6},
			{From: evdev.KEY_2, Key: evdev.KEY_7},
			{From: evdev.KEY_3, Key: evdev.KEY_F5},
			{From: evdev.KEY_4, Modifiers: []evdev.EvCode{evdev.KEY_LEFTSHIFT}, Key: evdev.KEY_MINUS},
		},
	}
}

func kbdInfo(path string, product uint16) device.Info {
	return device.Info{
		Path: path,
		Name: "Corsair Keyboard",
		ID:   device.ID{Bus: evdev.BUS_USB, Vendor: 0x1b1c, Product: product, Version: 1},
		Caps: map[evdev.EvType][]evdev.EvCode{
			evdev.EV_KEY: {evdev.KEY_A, evdev.KEY_1, evdev.KEY_2, evdev.KEY_3, evdev.KEY_4},
			evdev.EV_MSC: {evdev.MSC_SCAN},
		},
	}
}

func mouseInfo(path string) device.Info {
	return device.Info{
		Path: path,
		Name: "Corsair Pointer",
		ID:   device.ID{Bus: evdev.BUS_USB, Vendor: 0x1b1c, Product: 0x1bb2, Version: 1},
		Caps: map[evdev.EvType][]evdev.EvCode{evdev.EV_KEY: {evdev.BTN_LEFT, evdev.BTN_SIDE}},
	}
}

func k100Info() device.Info {
	return device.Info{
		Path: k100,
		Name: "K100",
		ID:   device.ID{Bus: evdev.BUS_USB, Vendor: 0x1b1c, Product: 0x1b7c, Version: 1},
		Caps: map[evdev.EvType][]evdev.EvCode{evdev.EV_KEY: {evdev.KEY_1}},
	}
}

type harness struct {
	be     *fakeBackend
	w      *fakeWatcher
	d      *daemon.Daemon
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, be *fakeBackend) *harness {
	t.Helper()

	return startCfg(t, be, testConfig(), nil)
}

func startCfg(t *testing.T, be *fakeBackend, cfg *config.Config, saver func(*config.Config) error) *harness {
	t.Helper()

	return startOpts(t, be, cfg, saver, nil)
}

// startOpts is startCfg with a hook to adjust the daemon options.
func startOpts(
	t *testing.T, be *fakeBackend, cfg *config.Config, saver func(*config.Config) error, mod func(*daemon.Options),
) *harness {
	t.Helper()

	h := &harness{be: be, w: newFakeWatcher(), done: make(chan error, 1)}
	opts := daemon.Options{
		Backend:     be,
		Watcher:     h.w,
		Config:      cfg,
		ConfigSaver: saver,
		ConfigPath:  "/etc/skb/config.toml",
		Version:     "test",
		Logger:      slog.New(slog.DiscardHandler),
		Debounce:    0,
		BackoffBase: 400 * time.Millisecond,
		BackoffMax:  800 * time.Millisecond,
	}

	if mod != nil {
		mod(&opts)
	}

	h.d = daemon.New(opts)

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() { h.done <- h.d.Run(ctx) }()

	t.Cleanup(func() { h.stop(t) })

	return h
}

func (h *harness) stop(t *testing.T) {
	t.Helper()
	h.cancel()

	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}

		h.done <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}

func (h *harness) device(path string) (ipc.Device, bool) {
	for _, d := range h.d.Status().Devices {
		if d.Path == path {
			return d, true
		}
	}

	return ipc.Device{}, false
}

func baseBackend() (*fakeBackend, *fakeSource, *fakeSource, *fakeSource) {
	be := newFakeBackend()
	kbd := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	mouse := newFakeSource(mouseInfo(wiredMouse))
	other := newFakeSource(k100Info())
	be.setSource(wiredKbd, kbd)
	be.setSource(wiredMouse, mouse)
	be.setSource(k100, other)

	return be, kbd, mouse, other
}

func TestStartupGrabsOnlyKeyboard(t *testing.T) {
	t.Parallel()

	be, kbd, mouse, other := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return len(h.d.Status().Devices) == 2 && other.isClosed() })

	if !kbd.isGrabbed() || mouse.isGrabbed() || other.isGrabbed() {
		t.Errorf("grab state kbd=%v mouse=%v k100=%v", kbd.isGrabbed(), mouse.isGrabbed(), other.isGrabbed())
	}

	sinks := be.sinkList()
	if len(sinks) != 1 {
		t.Fatalf("sinks = %d", len(sinks))
	}

	if !strings.HasPrefix(device.VirtualName(sinks[0].name), device.VirtualNamePrefix) {
		t.Errorf("sink name = %q", sinks[0].name)
	}

	for _, code := range []evdev.EvCode{evdev.KEY_6, evdev.KEY_7, evdev.KEY_F5, evdev.KEY_LEFTSHIFT, evdev.KEY_MINUS} {
		if !slices.Contains(sinks[0].caps[evdev.EV_KEY], code) {
			t.Errorf("sink caps lack %d", code)
		}
	}

	if sinks[0].id.Bus != evdev.BUS_VIRTUAL {
		t.Errorf("sink id = %+v", sinks[0].id)
	}

	kd, _ := h.device(wiredKbd)
	md, _ := h.device(wiredMouse)

	if !kd.Grabbed || md.Grabbed || kd.Matcher != "Darkstar wired" {
		t.Errorf("status kbd=%+v mouse=%+v", kd, md)
	}

	if !strings.HasPrefix(kd.VirtualName, device.VirtualNamePrefix) {
		t.Errorf("virtual name = %q", kd.VirtualName)
	}
}

func TestForwardsAndRemaps(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return len(be.sinkList()) == 1 && kbd.isGrabbed() })

	kbd.send(evdev.EV_MSC, evdev.MSC_SCAN, 0x7001e)
	kbd.send(evdev.EV_KEY, evdev.KEY_1, 1)
	kbd.send(evdev.EV_SYN, evdev.SYN_REPORT, 0)

	sink := be.sinkList()[0]
	want := [][3]int{
		{evdev.EV_MSC, evdev.MSC_SCAN, 0x7001e},
		{evdev.EV_KEY, evdev.KEY_6, 1},
		{evdev.EV_SYN, evdev.SYN_REPORT, 0},
	}

	waitFor(t, func() bool { return len(sink.triples()) == len(want) })

	if got := sink.triples(); !slices.Equal(got, want) {
		t.Errorf("events = %v", got)
	}

	_ = h
}

func TestHotplugWiredAndWireless(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	recv := newFakeSource(kbdInfo(receiverKbd, 0x1bdc))
	be.setSource(receiverKbd, recv)
	h.w.signal()

	waitFor(t, func() bool { return recv.isGrabbed() })

	if !kbd.isGrabbed() || len(be.sinkList()) != 2 {
		t.Errorf("kbd grabbed=%v sinks=%d", kbd.isGrabbed(), len(be.sinkList()))
	}
}

func TestRemovalReleasesKeys(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	kbd.send(evdev.EV_KEY, evdev.KEY_4, 1)
	sink := be.sinkList()[0]

	waitFor(t, func() bool { return len(sink.triples()) == 3 })

	be.removePath(wiredKbd)
	kbd.errs <- syscall.ENODEV

	waitFor(t, func() bool { return sink.isClosed() && kbd.isClosed() })

	got := sink.triples()[3:]
	want := [][3]int{
		{evdev.EV_KEY, evdev.KEY_MINUS, 0},
		{evdev.EV_KEY, evdev.KEY_LEFTSHIFT, 0},
		{evdev.EV_SYN, evdev.SYN_REPORT, 0},
	}

	if !slices.Equal(got, want) {
		t.Errorf("release events = %v", got)
	}

	waitFor(t, func() bool { _, ok := h.device(wiredKbd); return !ok })
}

func TestGrabErrorRetried(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	busy := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	busy.grabErr = syscall.EBUSY
	be.setSource(wiredKbd, busy)

	h := start(t, be)

	waitFor(t, func() bool {
		d, ok := h.device(wiredKbd)

		return ok && strings.Contains(d.Error, "input-remapper")
	})

	waitFor(t, func() bool { return busy.isClosed() && be.sinkList()[0].isClosed() })

	if d, _ := h.device(wiredKbd); d.Grabbed {
		t.Error("device reported as grabbed")
	}

	free := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	be.setSource(wiredKbd, free)
	h.w.signal()

	waitFor(t, func() bool { return free.isGrabbed() })
	waitFor(t, func() bool { d, _ := h.device(wiredKbd); return d.Grabbed && d.Error == "" })
}

func TestOpenErrorDoesNotStopOthers(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	be.setOpenErr(wiredMouse, errors.New("permission denied"))

	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	// A later retry succeeds once the error clears.
	be.setOpenErr(wiredMouse, nil)
	h.w.signal()

	waitFor(t, func() bool { _, ok := h.device(wiredMouse); return ok })
}

func TestActivePathsNotReopened(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	h.w.signal()

	extra := newFakeSource(kbdInfo(receiverKbd, 0x1bdc))
	be.setSource(receiverKbd, extra)
	h.w.signal()

	waitFor(t, func() bool { return extra.isGrabbed() })

	for _, p := range []string{wiredKbd, wiredMouse, k100} {
		if n := be.openCount(p); n != 1 {
			t.Errorf("%s opened %d times", p, n)
		}
	}
}

func TestVanishedPathStopsWorker(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	be.removePath(wiredKbd)
	h.w.signal()

	waitFor(t, func() bool { return kbd.isClosed() && be.sinkList()[0].isClosed() })
	waitFor(t, func() bool { _, ok := h.device(wiredKbd); return !ok })
}

func TestShutdownReleasesAndCloses(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	h := start(t, be)

	waitFor(t, func() bool { return kbd.isGrabbed() })

	kbd.send(evdev.EV_KEY, evdev.KEY_4, 1)
	sink := be.sinkList()[0]

	waitFor(t, func() bool { return len(sink.triples()) == 3 })

	h.stop(t)

	if !sink.isClosed() || !kbd.isClosed() {
		t.Error("sink or source left open")
	}

	got := sink.triples()[3:]
	if len(got) != 3 || got[0] != [3]int{evdev.EV_KEY, evdev.KEY_MINUS, 0} {
		t.Errorf("release events = %v", got)
	}
}

func TestStatusMappings(t *testing.T) {
	t.Parallel()

	be, _, _, _ := baseBackend()
	h := start(t, be)

	st := h.d.Status()

	var found bool

	for _, m := range st.Mappings {
		if m.From == "KEY_4" && slices.Equal(m.To, []string{"KEY_LEFTSHIFT", "KEY_MINUS"}) {
			found = true
		}
	}

	if !found || st.Version != "test" || st.ConfigPath != "/etc/skb/config.toml" {
		t.Errorf("status = %+v", st)
	}
}

func TestWorkerFailureBacksOff(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	be.setWriteErr(syscall.EIO)

	// Enough events that every restarted worker fails again.
	for range 20 {
		kbd.send(evdev.EV_KEY, evdev.KEY_A, 1)
	}

	start(t, be)

	waitFor(t, func() bool {
		sinks := be.sinkList()

		return len(sinks) >= 1 && sinks[0].isClosed()
	})

	if n := be.openCount(wiredKbd); n != 1 {
		t.Fatalf("opened %d times before backoff elapsed", n)
	}

	waitFor(t, func() bool { return be.openCount(wiredKbd) >= 2 })
}

func TestFailedDeviceRetriedOnTimer(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	busy := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	busy.grabErr = syscall.EBUSY
	be.setSource(wiredKbd, busy)

	h := start(t, be)

	waitFor(t, func() bool { d, _ := h.device(wiredKbd); return d.Error != "" })

	// No watcher signal: the retry must come from the backoff timer.
	busy.mu.Lock()
	busy.grabErr = nil
	busy.mu.Unlock()

	waitFor(t, func() bool { d, _ := h.device(wiredKbd); return d.Grabbed })
}

func TestPathReuseReclassifies(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	be.setSource(k100, newFakeSource(k100Info()))

	h := start(t, be)

	waitFor(t, func() bool { return be.openCount(k100) == 1 })

	replacement := newFakeSource(kbdInfo(k100, 0x1bb2))
	be.setSource(k100, replacement)
	h.w.signal()

	waitFor(t, func() bool { return replacement.isGrabbed() })
}

func TestUnplugIsNotAFailure(t *testing.T) {
	t.Parallel()

	be, kbd, _, _ := baseBackend()
	// A 10 s backoff would exceed the waitFor ceiling, so a retry within it proves none was applied.
	h := startOpts(t, be, testConfig(), nil, func(o *daemon.Options) {
		o.BackoffBase = 10 * time.Second
		o.BackoffMax = 10 * time.Second
	})

	waitFor(t, func() bool { return kbd.isGrabbed() })

	// The path is reused at once: the new node is present when the old worker exits.
	replacement := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	be.setSource(wiredKbd, replacement)
	kbd.errs <- syscall.ENODEV

	waitFor(t, func() bool { return kbd.isClosed() })
	h.w.signal()

	waitFor(t, func() bool { return replacement.isGrabbed() })
}
