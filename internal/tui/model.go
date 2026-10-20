// Package tui renders the daemon status as a Bubble Tea terminal UI and
// drives the learn, edit, delete and setup workflows.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/elias-knodel/skb/internal/ipc"
)

const (
	keyEsc   = "esc"
	keyCtrlC = "ctrl+c"
)

const (
	fetchTimeout = 2 * time.Second
	probeTimeout = 3 * time.Second
	learnTimeout = 10 * time.Second

	// inputGuard ignores keys right after the confirm view appears, so keys
	// still in flight from the learn step cannot trigger an action.
	inputGuard    = 400 * time.Millisecond
	flashDuration = 3 * time.Second
	// spinFPS is the frame interval of spinner.MiniDot.
	spinFPS = time.Second / 12

	defaultWidth = 80
	maxWidth     = 100
	// wideWidth is the content width from which the key label column is shown.
	wideWidth = 80
	// stderrLines is the number of installer stderr lines shown on failure.
	stderrLines = 10
)

// Client is the daemon API used by the UI.
type Client interface {
	Status(ctx context.Context) (ipc.Status, error)
	LearnSource(ctx context.Context, timeout time.Duration) (string, error)
	LearnTarget(ctx context.Context, timeout time.Duration) ([]string, error)
	SetMapping(ctx context.Context, args ipc.SetMappingArgs) ([]ipc.Mapping, error)
	DeleteMapping(ctx context.Context, from string) ([]ipc.Mapping, error)
}

// SetupState describes the installation state of the service.
type SetupState struct {
	SocketOK            bool
	UnitInstalled       bool
	ServiceActive       bool
	InputRemapperActive bool
	Err                 string
}

// Prober inspects the installation state.
type Prober interface {
	Probe(ctx context.Context) SetupState
}

// CommandFactory builds the privileged skb invocation for the given arguments.
type CommandFactory func(args ...string) *exec.Cmd

// Config wires the model to its collaborators.
type Config struct {
	Client  Client
	Prober  Prober
	Command CommandFactory
	// UID is the invoking user's uid, passed as --admin-uid on install.
	UID      uint32
	Interval time.Duration
	// Tick schedules a message after d; tea.Tick when nil.
	Tick func(d time.Duration, f func(time.Time) tea.Msg) tea.Cmd
	// Now returns the current time for the uptime display; time.Now when nil.
	Now func() time.Time
}

type viewID int

const (
	viewStatus viewID = iota
	viewLearnSource
	viewLearnTarget
	viewConfirm
	viewDeleteConfirm
	viewSetup
	viewError
)

// Model is the Bubble Tea model.
type Model struct {
	cfg  Config
	view viewID

	width, height int

	status    ipc.Status
	statusErr error
	loaded    bool
	cursor    int
	hint      string

	// fetchSeq numbers status fetches; results older than appliedSeq are dropped.
	fetchSeq, appliedSeq int

	setup     SetupState
	probed    bool
	setupAuto bool

	// session identifies the running learn command; results of older ones are dropped.
	session   int
	cancel    context.CancelFunc
	remaining int
	busyTries int
	spinFrame int

	// armed ends the input guard of the confirm view; pending marks a save or delete in flight.
	armed   bool
	pending bool

	flash    string
	flashSeq int

	from    string
	to      []string
	oldFrom string

	// label is the character typed for the learned target; labelSet marks the
	// first key of the target session as consumed.
	label    string
	labelSet bool

	errText string

	help help.Model
	bar  progress.Model
}

// New returns a model for cfg.
func New(cfg Config) Model {
	if cfg.Tick == nil {
		cfg.Tick = tea.Tick
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	m := Model{
		cfg:  cfg,
		help: newHelp(),
		bar:  progress.New(progress.WithGradient(gradientFrom, gradientTo), progress.WithoutPercentage()),
	}

	return m.layout()
}

// contentWidth is the width of the rendered content.
func (m Model) contentWidth() int {
	w := m.width
	if w <= 0 {
		w = defaultWidth
	}

	return min(w, maxWidth)
}

// layout derives widget sizes from the window size.
func (m Model) layout() Model {
	m.help.Width = m.width
	m.bar.Width = max(min(m.contentWidth()-10, 40), 10)

	return m
}

// Init triggers the first status fetch and starts the poll chain.
func (m Model) Init() tea.Cmd {
	_, cmd := m.fetch(true)

	return cmd
}

// fetch starts a status fetch. Only poll fetches continue the poll chain.
func (m Model) fetch(poll bool) (Model, tea.Cmd) {
	m.fetchSeq++

	client, seq := m.cfg.Client, m.fetchSeq

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		st, err := client.Status(ctx)
		if err != nil {
			return statusErrMsg{err: err, poll: poll, seq: seq}
		}

		return statusMsg{st: st, poll: poll, seq: seq}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return m.cfg.Tick(m.cfg.Interval, func(time.Time) tea.Msg { return tickMsg{} })
}

// pollCmd reschedules the poll chain for results that belong to it.
func (m Model) pollCmd(poll bool) tea.Cmd {
	if !poll {
		return nil
	}

	return m.tickCmd()
}

func (m Model) probeCmd() tea.Cmd {
	prober := m.cfg.Prober

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()

		return probeMsg{state: prober.Probe(ctx)}
	}
}

// Update handles messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		return m.layout(), nil
	case statusMsg:
		if msg.seq < m.appliedSeq {
			return m, m.pollCmd(msg.poll)
		}

		m.appliedSeq = msg.seq
		m.status, m.statusErr, m.loaded = msg.st, nil, true
		m.cursor = min(m.cursor, max(len(m.status.Mappings)-1, 0))

		return m, m.pollCmd(msg.poll)
	case statusErrMsg:
		if msg.seq < m.appliedSeq {
			return m, m.pollCmd(msg.poll)
		}

		m.appliedSeq = msg.seq
		m.statusErr, m.loaded = msg.err, true

		if !m.probed {
			return m, tea.Batch(m.pollCmd(msg.poll), m.probeCmd())
		}

		return m, m.pollCmd(msg.poll)
	case tickMsg:
		return m.fetch(true)
	case probeMsg:
		return m.onProbe(msg), nil
	case execDoneMsg:
		return m.onExecDone(msg)
	case learnTickMsg:
		return m.onLearnTick(msg)
	case learnRetryMsg:
		return m.onLearnRetry(msg)
	case spinTickMsg:
		return m.onSpinTick(msg)
	case confirmArmMsg:
		return m.onConfirmArm(msg), nil
	case flashExpireMsg:
		if msg.seq == m.flashSeq {
			m.flash = ""
		}

		return m, nil
	case sourceMsg:
		return m.onSource(msg)
	case targetMsg:
		return m.onTarget(msg)
	case savedMsg:
		return m.onMutated(msg.mappings, msg.err, "Mapping saved")
	case deletedMsg:
		return m.onMutated(msg.mappings, msg.err, "Mapping deleted")
	case tea.KeyMsg:
		return m.onKey(msg)
	}

	return m, nil
}

func (m Model) onProbe(msg probeMsg) Model {
	m.setup, m.probed = msg.state, true

	// A missing daemon and no installed unit: start in setup.
	if m.statusErr != nil && !msg.state.UnitInstalled && m.view == viewStatus && !m.setupAuto {
		m.view, m.setupAuto = viewSetup, true
	}

	return m
}

func (m Model) onExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errText = "command failed: " + msg.err.Error()

		if tail := stderrTail(msg.stderr); tail != "" {
			m.errText += "\n\n" + tail
		}

		m.view = viewError

		return m, nil
	}

	m, fetch := m.fetch(false)

	return m, tea.Batch(m.probeCmd(), fetch)
}

// stderrTail returns the last lines of captured command output.
func stderrTail(s string) string {
	s = sanitize(s)
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > stderrLines {
		lines = lines[len(lines)-stderrLines:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// sanitize strips terminal escape sequences and non-printable runes except
// newline and tab.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			return r
		}

		return -1
	}, ansi.Strip(s))
}

// setFlash shows text until the returned timer fires.
func (m Model) setFlash(text string) (Model, tea.Cmd) {
	m.flashSeq++
	m.flash = text

	seq := m.flashSeq

	return m, m.cfg.Tick(flashDuration, func(time.Time) tea.Msg { return flashExpireMsg{seq: seq} })
}

// onMutated finishes a save or delete; verb is the flash text on success.
func (m Model) onMutated(mappings []ipc.Mapping, err error, verb string) (tea.Model, tea.Cmd) {
	m.pending = false

	if err != nil {
		return m.fail(err), nil
	}

	m.status.Mappings = mappings

	// Poll results requested before this mutation are stale.
	m.appliedSeq = m.fetchSeq + 1

	if i := slices.IndexFunc(mappings, func(mp ipc.Mapping) bool { return mp.From == m.from }); i >= 0 && m.view == viewConfirm {
		m.cursor = i
	} else {
		m.cursor = min(m.cursor, max(len(mappings)-1, 0))
	}

	m.view = viewStatus

	m, flash := m.setFlash(verb)
	m, fetch := m.fetch(false)

	return m, tea.Batch(flash, fetch)
}

// fail shows err; cancellations just return to the status view.
func (m Model) fail(err error) Model {
	m = m.stopLearn()

	var ie *ipc.Error

	switch {
	case errors.As(err, &ie) && ie.Code == ipc.CodeCancelled, errors.Is(err, context.Canceled):
		m.view = viewStatus

		return m
	case errors.As(err, &ie) && ie.Code == ipc.CodeForbidden:
		m.errText = m.notAdminText()
	case errors.As(err, &ie) && ie.Code == ipc.CodeTimeout:
		m.errText = fmt.Sprintf("no key pressed within %d s", int(learnTimeout/time.Second))
	case errors.As(err, &ie):
		m.errText = ie.Message
	default:
		m.errText = err.Error()
	}

	m.view = viewError

	return m
}

func (m Model) notAdminText() string {
	return fmt.Sprintf("uid %d is not admin_uid - run setup", m.status.CallerUID)
}

// stopLearn cancels a running learn command and invalidates its session.
func (m Model) stopLearn() Model {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	m.session++

	return m
}

// outdated reports a daemon that does not speak this protocol version.
func outdated(err error) bool {
	var ie *ipc.Error

	return errors.Is(err, io.EOF) || errors.As(err, &ie) &&
		(ie.Code == ipc.CodeBadRequest || ie.Code == ipc.CodeUnsupportedVersion)
}

// View renders the current view.
func (m Model) View() string {
	switch m.view {
	case viewLearnSource:
		return m.viewLearn(1, "Press the side button on the mouse")
	case viewLearnTarget:
		return m.viewLearn(2, "Press the key or combination on the keyboard")
	case viewConfirm:
		return m.confirmView()
	case viewDeleteConfirm:
		return m.deleteView()
	case viewSetup:
		return m.setupView()
	case viewError:
		return m.errorView()
	default:
		return m.statusView()
	}
}

func (m Model) errorView() string {
	box := errBoxStyle.Width(m.contentWidth() - 2).
		Render(errStyle.Render("Error") + "\n\n" + m.errText)

	return m.screen(box, "")
}

// bodyAvail is the number of body lines left next to header, footer and the
// extra block; screen and the status body share it so they cannot diverge.
func (m Model) bodyAvail(extra string) int {
	avail := m.height - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footerView()) - 2
	if extra != "" {
		avail -= lipgloss.Height(extra) + 1
	}

	return avail
}

// screen assembles header, body, optional extra lines and footer. With a
// known height the frame never exceeds it: the extra block is dropped first,
// then body and footer, so the header always remains.
func (m Model) screen(body, extra string) string {
	header, footer := m.headerView(), m.footerView()

	if m.height > 0 {
		if m.bodyAvail(extra) < 1 {
			extra = ""
		}

		avail := m.bodyAvail(extra)
		if avail < 1 {
			out := header
			if lipgloss.Height(header)+lipgloss.Height(footer) <= m.height {
				out += "\n" + footer
			}

			lines := strings.Split(out, "\n")

			return strings.Join(lines[:min(m.height, len(lines))], "\n")
		}

		body = fitHeight(body, avail)
	}

	out := header + "\n\n" + body
	if extra != "" {
		out += "\n\n" + extra
	}

	return out + "\n\n" + footer
}

// fitHeight cuts s to at most n lines, marking the cut with an ellipsis.
func fitHeight(s string, n int) string {
	n = max(n, 1)

	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}

	return strings.Join(append(lines[:n-1], dimStyle.Render("…")), "\n")
}

// footerView renders the key help of the current view.
func (m Model) footerView() string {
	h := m.help

	if m.view != viewStatus && m.view != viewSetup {
		h.ShowAll = false
	}

	switch {
	case m.view == viewConfirm && !m.armed:
		return dimStyle.Render("waiting for keys to settle…")
	case m.view == viewConfirm && m.pending, m.view == viewDeleteConfirm && m.pending:
		return ""
	}

	return h.View(m.keyMap())
}
