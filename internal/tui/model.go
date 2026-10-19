// Package tui renders the daemon status as a Bubble Tea terminal UI and
// drives the learn, edit, delete and setup workflows.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	status    ipc.Status
	statusErr error
	loaded    bool
	cursor    int
	hint      string

	setup     SetupState
	probed    bool
	setupAuto bool

	// session identifies the running learn command; results of older ones are dropped.
	session   int
	cancel    context.CancelFunc
	remaining int
	busyTries int

	from    string
	to      []string
	oldFrom string

	errText string
}

// New returns a model for cfg.
func New(cfg Config) Model {
	if cfg.Tick == nil {
		cfg.Tick = tea.Tick
	}

	return Model{cfg: cfg}
}

// Init triggers the first status fetch.
func (m Model) Init() tea.Cmd {
	return m.fetchCmd()
}

func (m Model) fetchCmd() tea.Cmd {
	client := m.cfg.Client

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		st, err := client.Status(ctx)
		if err != nil {
			return statusErrMsg{err: err}
		}

		return statusMsg(st)
	}
}

func (m Model) tickCmd() tea.Cmd {
	return m.cfg.Tick(m.cfg.Interval, func(time.Time) tea.Msg { return tickMsg{} })
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
	case statusMsg:
		m.status, m.statusErr, m.loaded = ipc.Status(msg), nil, true
		m.cursor = min(m.cursor, max(len(m.status.Mappings)-1, 0))

		return m, m.tickCmd()
	case statusErrMsg:
		m.statusErr, m.loaded = msg.err, true

		if !m.probed {
			return m, tea.Batch(m.tickCmd(), m.probeCmd())
		}

		return m, m.tickCmd()
	case tickMsg:
		return m, m.fetchCmd()
	case probeMsg:
		return m.onProbe(msg), nil
	case execDoneMsg:
		if msg.err != nil {
			m.errText, m.view = "command failed: "+msg.err.Error(), viewError

			return m, nil
		}

		return m, tea.Batch(m.probeCmd(), m.fetchCmd())
	case learnTickMsg:
		return m.onLearnTick(msg)
	case learnRetryMsg:
		return m.onLearnRetry(msg)
	case sourceMsg:
		return m.onSource(msg)
	case targetMsg:
		return m.onTarget(msg)
	case savedMsg:
		return m.onMutated(msg.mappings, msg.err)
	case deletedMsg:
		return m.onMutated(msg.mappings, msg.err)
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

// onMutated finishes a save or delete.
func (m Model) onMutated(mappings []ipc.Mapping, err error) (tea.Model, tea.Cmd) {
	if err != nil {
		return m.fail(err), nil
	}

	m.status.Mappings = mappings
	m.cursor = min(m.cursor, max(len(mappings)-1, 0))
	m.view = viewStatus

	return m, m.fetchCmd()
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
		return m.viewLearn("Step 1/3: press the side button on the mouse")
	case viewLearnTarget:
		return m.viewLearn("Step 2/3: press the key or combination on the keyboard")
	case viewConfirm:
		return m.confirmView()
	case viewDeleteConfirm:
		return m.deleteView()
	case viewSetup:
		return m.setupView()
	case viewError:
		return titleStyle.Render("skb") + "\n\n" + errStyle.Render(m.errText) + "\n\n" +
			dimStyle.Render("press any key") + "\n"
	default:
		return m.statusView()
	}
}
