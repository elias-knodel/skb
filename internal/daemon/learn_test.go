package daemon_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/config"
	"github.com/elias-knodel/skb/internal/device"
	"github.com/elias-knodel/skb/internal/ipc"
)

var errSave = errors.New("disk full")

type saver struct {
	mu    sync.Mutex
	calls []*config.Config
	err   error
}

func (s *saver) save(c *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}

	s.calls = append(s.calls, c)

	return nil
}

func (s *saver) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}

func wantErrCode(t *testing.T, err error, code string) {
	t.Helper()

	var e *ipc.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %q", err, code)
	}
}

// grabbedKeyboard starts a daemon with an empty mapping set and one grabbed keyboard.
func grabbedKeyboard(t *testing.T, sv *saver) (*harness, *fakeBackend, *fakeSource) {
	t.Helper()

	be := newFakeBackend()
	kbd := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	be.setSource(wiredKbd, kbd)

	h := startCfg(t, be, config.Default(), sv.save)

	waitFor(t, func() bool { return kbd.isGrabbed() && len(be.sinkList()) == 1 })

	return h, be, kbd
}

func setMapping(t *testing.T, h *harness, from string, to ...string) {
	t.Helper()

	if _, err := h.d.SetMapping(context.Background(), ipc.SetMappingArgs{From: from, To: to}); err != nil {
		t.Fatal(err)
	}
}

func TestZeroMappingsPassThrough(t *testing.T) {
	t.Parallel()

	_, be, kbd := grabbedKeyboard(t, &saver{})
	sink := be.sinkList()[0]

	kbd.send(evdev.EV_KEY, evdev.KEY_1, 1)
	waitFor(t, func() bool { return len(sink.triples()) == 1 })

	if got := sink.triples()[0]; got != [3]int{evdev.EV_KEY, evdev.KEY_1, 1} {
		t.Errorf("event = %v", got)
	}
}

func TestSetMappingHotReload(t *testing.T) {
	t.Parallel()

	sv := &saver{}
	h, be, kbd := grabbedKeyboard(t, sv)
	sink := be.sinkList()[0]

	setMapping(t, h, "KEY_1", "KEY_F5")

	if sv.count() != 1 {
		t.Fatalf("saver calls = %d", sv.count())
	}

	kbd.send(evdev.EV_KEY, evdev.KEY_1, 1)
	waitFor(t, func() bool { return len(sink.triples()) == 1 })

	if got := sink.triples()[0]; got != [3]int{evdev.EV_KEY, evdev.KEY_F5, 1} {
		t.Errorf("event = %v", got)
	}

	if len(be.sinkList()) != 1 || kbd.grabCount() != 1 {
		t.Errorf("sinks=%d grabs=%d", len(be.sinkList()), kbd.grabCount())
	}

	if st := h.d.Status(); len(st.Mappings) != 1 || st.Mappings[0].From != "KEY_1" {
		t.Errorf("mappings = %v", st.Mappings)
	}
}

func TestSetMappingSaveFailure(t *testing.T) {
	t.Parallel()

	sv := &saver{err: errSave}
	h, _, _ := grabbedKeyboard(t, sv)

	_, err := h.d.SetMapping(context.Background(), ipc.SetMappingArgs{From: "KEY_1", To: []string{"KEY_F5"}})
	wantErrCode(t, err, ipc.CodeInternal)

	if len(h.d.Status().Mappings) != 0 {
		t.Error("mapping activated despite save failure")
	}
}

func TestSetMappingInvalid(t *testing.T) {
	t.Parallel()

	h, _, _ := grabbedKeyboard(t, &saver{})

	for _, args := range []ipc.SetMappingArgs{
		{From: "KEY_FOO", To: []string{"KEY_F5"}},
		{From: "KEY_1", To: []string{"KEY_A", "KEY_B"}},
		{From: "KEY_1", To: nil},
		{From: "KEY_1", To: []string{"BTN_LEFT"}},
	} {
		_, err := h.d.SetMapping(context.Background(), args)
		wantErrCode(t, err, ipc.CodeInvalid)
	}

	_, err := h.d.SetMapping(context.Background(), ipc.SetMappingArgs{From: "KEY_1", To: []string{"KEY_A"}, OldFrom: "KEY_2"})
	wantErrCode(t, err, ipc.CodeNotFound)
}

func TestDeleteMapping(t *testing.T) {
	t.Parallel()

	h, be, kbd := grabbedKeyboard(t, &saver{})
	sink := be.sinkList()[0]

	_, err := h.d.DeleteMapping(context.Background(), "KEY_1")
	wantErrCode(t, err, ipc.CodeNotFound)

	setMapping(t, h, "KEY_1", "KEY_F5")

	remaining, err := h.d.DeleteMapping(context.Background(), "KEY_1")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("delete: %v %v", remaining, err)
	}

	kbd.send(evdev.EV_KEY, evdev.KEY_1, 1)
	waitFor(t, func() bool { return len(sink.triples()) == 1 })

	if got := sink.triples()[0]; got != [3]int{evdev.EV_KEY, evdev.KEY_1, 1} {
		t.Errorf("event = %v", got)
	}
}

func TestReloadReleasesHeldOutputs(t *testing.T) {
	t.Parallel()

	h, be, kbd := grabbedKeyboard(t, &saver{})
	sink := be.sinkList()[0]

	setMapping(t, h, "KEY_4", "KEY_LEFTSHIFT", "KEY_MINUS")

	kbd.send(evdev.EV_KEY, evdev.KEY_4, 1)
	waitFor(t, func() bool { return len(sink.triples()) == 3 })

	// A different mapping set replaces the engine on the next event.
	setMapping(t, h, "KEY_1", "KEY_F5")
	kbd.send(evdev.EV_KEY, evdev.KEY_A, 1)

	waitFor(t, func() bool { return len(sink.triples()) >= 7 })

	got := sink.triples()[3:]
	want := [][3]int{
		{evdev.EV_KEY, evdev.KEY_MINUS, 0},
		{evdev.EV_KEY, evdev.KEY_LEFTSHIFT, 0},
		{evdev.EV_SYN, evdev.SYN_REPORT, 0},
		{evdev.EV_KEY, evdev.KEY_A, 1},
	}

	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestLearnSource(t *testing.T) {
	t.Parallel()

	h, be, kbd := grabbedKeyboard(t, &saver{})
	sink := be.sinkList()[0]

	type result struct {
		key string
		err error
	}

	res := make(chan result, 1)

	go func() {
		key, err := h.d.LearnSource(context.Background(), 2*time.Second)
		res <- result{key, err}
	}()

	waitFor(t, func() bool { return h.d.Status().Learning })

	kbd.send(evdev.EV_KEY, evdev.KEY_2, 1)

	r := <-res
	if r.err != nil || r.key != "KEY_2" {
		t.Fatalf("result = %+v", r)
	}

	kbd.send(evdev.EV_KEY, evdev.KEY_2, 0)
	kbd.send(evdev.EV_KEY, evdev.KEY_A, 1)
	kbd.send(evdev.EV_KEY, evdev.KEY_A, 0)
	waitFor(t, func() bool { return len(sink.triples()) == 2 })

	if got := sink.triples()[0]; got != [3]int{evdev.EV_KEY, evdev.KEY_A, 1} {
		t.Errorf("KEY_2 leaked to the sink: %v", sink.triples())
	}

	if h.d.Status().Learning {
		t.Error("still learning")
	}

	// After the session KEY_2 is remapped normally.
	setMapping(t, h, "KEY_2", "KEY_F6")
	kbd.send(evdev.EV_KEY, evdev.KEY_2, 1)
	waitFor(t, func() bool { return len(sink.triples()) == 3 })

	if got := sink.triples()[2]; got != [3]int{evdev.EV_KEY, evdev.KEY_F6, 1} {
		t.Errorf("event = %v", got)
	}
}

func TestLearnSourceTimeoutBusyCancel(t *testing.T) {
	t.Parallel()

	h, _, _ := grabbedKeyboard(t, &saver{})

	_, err := h.d.LearnSource(context.Background(), 50*time.Millisecond)
	wantErrCode(t, err, ipc.CodeTimeout)

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)

	go func() {
		_, err := h.d.LearnSource(ctx, 5*time.Second)
		first <- err
	}()

	waitFor(t, func() bool { return h.d.Status().Learning })

	_, err = h.d.LearnSource(context.Background(), time.Second)
	wantErrCode(t, err, ipc.CodeBusy)

	_, err = h.d.LearnTarget(context.Background(), time.Second)
	wantErrCode(t, err, ipc.CodeBusy)

	cancel()
	wantErrCode(t, <-first, ipc.CodeCancelled)
}

func TestLearnSourceNoDevice(t *testing.T) {
	t.Parallel()

	h := startCfg(t, newFakeBackend(), config.Default(), nil)

	_, err := h.d.LearnSource(context.Background(), time.Second)
	wantErrCode(t, err, ipc.CodeNoDevice)
}

func TestLearnTarget(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	kbd := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	other := newFakeSource(k100Info())
	virt := newFakeSource(device.Info{
		Path: "/dev/input/event9",
		Name: device.VirtualName("x"),
		ID:   device.ID{Bus: evdev.BUS_VIRTUAL},
		Caps: map[evdev.EvType][]evdev.EvCode{evdev.EV_KEY: {evdev.KEY_A}},
	})

	be.setSource(wiredKbd, kbd)
	be.setSource(k100, other)
	be.setSource("/dev/input/event9", virt)

	h := startCfg(t, be, config.Default(), nil)

	waitFor(t, func() bool { return kbd.isGrabbed() && other.isClosed() && virt.isClosed() })

	virt.send(evdev.EV_KEY, evdev.KEY_A, 1)
	other.send(evdev.EV_KEY, evdev.KEY_LEFTSHIFT, 1)
	other.send(evdev.EV_KEY, evdev.KEY_MINUS, 1)

	to, err := h.d.LearnTarget(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(to, []string{"KEY_LEFTSHIFT", "KEY_MINUS"}) {
		t.Errorf("to = %v", to)
	}

	if other.grabCount() != 0 || !other.isClosed() || !virt.isClosed() {
		t.Errorf("grabs=%d closed=%v/%v", other.grabCount(), other.isClosed(), virt.isClosed())
	}
}

func TestLearnTargetTimeoutClosesReaders(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	kbd := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	other := newFakeSource(k100Info())
	be.setSource(wiredKbd, kbd)
	be.setSource(k100, other)

	h := startCfg(t, be, config.Default(), nil)

	waitFor(t, func() bool { return kbd.isGrabbed() && other.isClosed() })

	_, err := h.d.LearnTarget(context.Background(), 50*time.Millisecond)
	wantErrCode(t, err, ipc.CodeTimeout)

	if !other.isClosed() {
		t.Error("reader left open")
	}
}

func TestLearnTargetNoDevice(t *testing.T) {
	t.Parallel()

	be := newFakeBackend()
	kbd := newFakeSource(kbdInfo(wiredKbd, 0x1bb2))
	be.setSource(wiredKbd, kbd)

	h := startCfg(t, be, config.Default(), nil)
	waitFor(t, func() bool { return kbd.isGrabbed() })

	_, err := h.d.LearnTarget(context.Background(), time.Second)
	wantErrCode(t, err, ipc.CodeNoDevice)
}

func TestStatusAdminUID(t *testing.T) {
	t.Parallel()

	uid := uint32(1000)
	cfg := config.Default()
	cfg.AdminUID = &uid

	h := startCfg(t, newFakeBackend(), cfg, nil)

	st := h.d.Status()
	if st.AdminUID == nil || *st.AdminUID != 1000 || h.d.AdminUID() == nil || st.Learning {
		t.Errorf("status = %+v", st)
	}
}
