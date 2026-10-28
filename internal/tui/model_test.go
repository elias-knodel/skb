package tui_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/tui"
)

type fakeClient struct {
	mu sync.Mutex

	mappings []ipc.Mapping

	status    ipc.Status
	statusErr error

	sourceKey   string
	sourceErr   error
	sourceBlock bool
	sourceBusy  int
	sourceDone  chan struct{}
	lastTimeout time.Duration
	sourceCalls int

	target    []string
	targetErr error

	setErr    error
	lastSet   ipc.SetMappingArgs
	setCalls  int
	delCalls  int
	lastDel   string
	deleteErr error
}

func (c *fakeClient) Status(context.Context) (ipc.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	st := c.status
	if c.mappings != nil {
		st.Mappings = c.mappings
	}

	return st, c.statusErr
}

func (c *fakeClient) LearnSource(ctx context.Context, d time.Duration) (string, error) {
	c.mu.Lock()
	c.sourceCalls++
	c.lastTimeout = d
	block, key, err, done := c.sourceBlock, c.sourceKey, c.sourceErr, c.sourceDone

	if c.sourceBusy > 0 {
		c.sourceBusy--
		c.mu.Unlock()

		return "", &ipc.Error{Code: ipc.CodeBusy, Message: "busy"}
	}

	c.mu.Unlock()

	if block {
		<-ctx.Done()
		close(done)

		return "", ctx.Err()
	}

	return key, err
}

func (c *fakeClient) LearnTarget(context.Context, time.Duration) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.target, c.targetErr
}

func (c *fakeClient) SetMapping(_ context.Context, a ipc.SetMappingArgs) ([]ipc.Mapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setCalls++
	c.lastSet = a

	if c.setErr != nil {
		return nil, c.setErr
	}

	if c.mappings == nil {
		c.mappings = c.status.Mappings
	}

	merged := slices.DeleteFunc(slices.Clone(c.mappings), func(mp ipc.Mapping) bool {
		return mp.From == a.OldFrom || mp.From == a.From
	})
	merged = append(merged, ipc.Mapping{From: a.From, To: a.To, Label: a.Label})
	c.mappings = merged

	return merged, nil
}

func (c *fakeClient) DeleteMapping(_ context.Context, from string) ([]ipc.Mapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.delCalls++
	c.lastDel = from

	if c.deleteErr != nil {
		return nil, c.deleteErr
	}

	if c.mappings == nil {
		c.mappings = c.status.Mappings
	}

	c.mappings = slices.DeleteFunc(slices.Clone(c.mappings), func(mp ipc.Mapping) bool { return mp.From == from })

	return c.mappings, nil
}

type fakeProber struct{ state tui.SetupState }

func (p fakeProber) Probe(context.Context) tui.SetupState { return p.state }

type commandRecorder struct {
	calls [][]string
	last  *exec.Cmd
}

func (r *commandRecorder) factory(args ...string) *exec.Cmd {
	r.calls = append(r.calls, args)

	r.last = exec.CommandContext(context.Background(), "/bin/true")

	return r.last
}

func adminStatus() ipc.Status {
	return ipc.Status{
		Version:       "v1",
		CallerUID:     1000,
		CallerIsAdmin: true,
		Devices: []ipc.Device{
			{Path: "/dev/input/event7", Name: "Corsair Keyboard", Vendor: 0x1b1c, Product: 0x1bb2, Grabbed: true, Matcher: "Darkstar wired"},
			{Path: "/dev/input/event8", Name: "Corsair Receiver", Vendor: 0x1b1c, Product: 0x1bdc, Error: "grab: device or resource busy: device busy - is input-remapper still grabbing it?"},
		},
		Mappings: []ipc.Mapping{{From: "KEY_4", To: []string{"KEY_LEFTSHIFT", "KEY_MINUS"}}},
	}
}

type tickRec struct {
	d time.Duration
	f func(time.Time) tea.Msg
}

type rig struct {
	t      *testing.T
	m      tui.Model
	client *fakeClient
	cmds   *commandRecorder
	ticks  []tickRec
}

// tick records a timer request and yields no message when run.
func (r *rig) tick(d time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
	r.ticks = append(r.ticks, tickRec{d, f})

	return func() tea.Msg { return nil }
}

// fire pops the latest recorded timer of duration d and returns its message.
func (r *rig) fire(d time.Duration) tea.Msg {
	r.t.Helper()

	for i, rec := range slices.Backward(r.ticks) {
		if rec.d == d {
			f := rec.f
			r.ticks = append(r.ticks[:i], r.ticks[i+1:]...)

			return f(time.Time{})
		}
	}

	r.t.Fatalf("no timer of %v recorded", d)

	return nil
}

func newRig(t *testing.T, st ipc.Status) *rig {
	t.Helper()

	return newRigWith(t, st, nil)
}

// newRigWith is newRig with a hook that adjusts the model configuration.
func newRigWith(t *testing.T, st ipc.Status, adjust func(*tui.Config)) *rig {
	t.Helper()

	client := &fakeClient{status: st, sourceKey: "KEY_1", target: []string{"KEY_LEFTSHIFT", "KEY_MINUS"}}
	cmds := &commandRecorder{}
	r := &rig{t: t, client: client, cmds: cmds}
	cfg := tui.Config{
		Client:   client,
		Prober:   fakeProber{state: tui.SetupState{UnitInstalled: true}},
		Command:  cmds.factory,
		UID:      1000,
		Interval: time.Hour,
		Tick:     r.tick,
	}
	if adjust != nil {
		adjust(&cfg)
	}

	r.m = tui.New(cfg)
	r.update(tea.WindowSizeMsg{Width: 100, Height: 50})

	return r
}

// exec runs a command synchronously and returns its messages, unwrapping batches.
func exec1(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()

	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, exec1(c)...)
		}

		return out
	}

	return []tea.Msg{msg}
}

func (r *rig) update(msg tea.Msg) tea.Cmd {
	r.t.Helper()

	next, cmd := r.m.Update(msg)

	model, ok := next.(tui.Model)
	if !ok {
		r.t.Fatal("unexpected model type")
	}

	r.m = model

	return cmd
}

// settle feeds msgs of cmd back into the model until no command is left,
// ignoring timers and process executions.
func (r *rig) settle(cmd tea.Cmd) {
	r.t.Helper()

	for _, msg := range exec1(cmd) {
		if msg == nil {
			continue
		}

		if _, quit := msg.(tea.QuitMsg); quit {
			return
		}

		r.settle(r.update(msg))
	}
}

func (r *rig) key(s string) tea.Cmd {
	r.t.Helper()

	switch s {
	case "esc":
		return r.update(tea.KeyMsg{Type: tea.KeyEsc})
	case "enter":
		return r.update(tea.KeyMsg{Type: tea.KeyEnter})
	case "ctrl+c":
		return r.update(tea.KeyMsg{Type: tea.KeyCtrlC})
	default:
		return r.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	}
}

func (r *rig) loaded() {
	r.t.Helper()
	r.settle(r.m.Init())
}

func (r *rig) view() string { return r.m.View() }

func (r *rig) wantView(subs ...string) {
	r.t.Helper()

	v := r.view()
	for _, s := range subs {
		if !strings.Contains(v, s) {
			r.t.Errorf("view lacks %q:\n%s", s, v)
		}
	}
}

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetHasDarkBackground(true)

	os.Exit(m.Run())
}

func TestInitFetchesStatus(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())

	msgs := exec1(r.m.Init())
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}

	r.update(msgs[0])
	r.wantLine("KEY_4", "→", "KEY_LEFTSHIFT+KEY_MINUS")
}

func TestStatusViewListsDevices(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.wantView("Corsair Keyboard", "wired", "wireless", "grabbed", "0x1b1c:0x1bb2")
}

func TestBusyDeviceShowsHint(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.wantView("input-remapper")
}

func TestUnreachableShowsError(t *testing.T) {
	t.Parallel()

	r := newRig(t, ipc.Status{})
	r.client.statusErr = errors.New("connection refused")
	r.loaded()
	r.wantView("daemon unreachable", "connection refused")
}

func TestOutdatedDaemon(t *testing.T) {
	t.Parallel()

	r := newRig(t, ipc.Status{})
	r.client.statusErr = &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed request"}
	r.loaded()
	r.wantView("outdated")
}

func TestQuitKeys(t *testing.T) {
	t.Parallel()

	for _, k := range []string{"q", "ctrl+c"} {
		r := newRig(t, adminStatus())
		r.loaded()

		cmd := r.key(k)
		if cmd == nil {
			t.Fatalf("%s: no cmd", k)
		}

		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: not a quit", k)
		}
	}
}

func TestStatusSchedulesTick(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())

	if cmd := r.update(exec1(r.m.Init())[0]); cmd == nil {
		t.Fatal("no tick scheduled")
	}
}

func TestNonAdminCannotLearn(t *testing.T) {
	t.Parallel()

	st := adminStatus()
	st.CallerIsAdmin = false
	st.CallerUID = 1234
	r := newRig(t, st)
	r.loaded()

	if cmd := r.key("a"); cmd != nil {
		t.Error("learn started for non-admin")
	}

	r.wantView("uid 1234 is not admin_uid")

	if r.client.sourceCalls != 0 {
		t.Error("LearnSource called")
	}
}

// noTick replaces timers with a command that yields no message.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
	return func() tea.Msg { return nil }
}

// runAsync runs a command, including the children of a batch, in goroutines
// and delivers every non-nil message.
func runAsync(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 8)

	run := func(c tea.Cmd) {
		if msg := c(); msg != nil {
			out <- msg
		}
	}

	go func() {
		msg := cmd()

		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			if msg != nil {
				out <- msg
			}

			return
		}

		for _, c := range batch {
			go run(c)
		}
	}()

	return out
}

// arm ends the input guard of the confirm view.
func (r *rig) arm() { r.update(r.fire(tui.InputGuard)) }

// wantLine asserts that one line of the view contains all substrings in order.
func (r *rig) wantLine(subs ...string) {
	r.t.Helper()

	for line := range strings.SplitSeq(r.view(), "\n") {
		pos, ok := 0, true

		for _, s := range subs {
			i := strings.Index(line[pos:], s)
			if i < 0 {
				ok = false

				break
			}

			pos += i + len(s)
		}

		if ok {
			return
		}
	}

	r.t.Errorf("no line contains %q in order:\n%s", subs, r.view())
}

func (r *rig) wantNoView(subs ...string) {
	r.t.Helper()

	v := r.view()
	for _, s := range subs {
		if strings.Contains(v, s) {
			r.t.Errorf("view contains %q:\n%s", s, v)
		}
	}
}

// countTicks counts pending timers of duration d.
func (r *rig) countTicks(d time.Duration) int {
	n := 0

	for _, rec := range r.ticks {
		if rec.d == d {
			n++
		}
	}

	return n
}
