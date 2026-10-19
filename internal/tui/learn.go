package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/ipc"
)

// startAction handles the add, edit and delete keys of the status view.
func (m Model) startAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "a":
		m.from, m.oldFrom, m.to, m.busyTries = "", "", nil, 0

		return m.startSource()
	case "e":
		sel, ok := m.selected()
		if !ok {
			return m, nil
		}

		m.from, m.oldFrom, m.to, m.busyTries = sel.From, sel.From, nil, 0

		return m.startTarget()
	default: // "d"
		sel, ok := m.selected()
		if !ok {
			return m, nil
		}

		m.from = sel.From
		m.view = viewDeleteConfirm

		return m, nil
	}
}

func (m Model) startSource() (tea.Model, tea.Cmd) {
	m, ctx := m.newSession()
	m.view = viewLearnSource

	client, session := m.cfg.Client, m.session

	learnCmd := func() tea.Msg {
		key, err := client.LearnSource(ctx, learnTimeout)

		return sourceMsg{session: session, key: key, err: err}
	}

	return m, tea.Batch(learnCmd, m.learnTick())
}

func (m Model) startTarget() (tea.Model, tea.Cmd) {
	m, ctx := m.newSession()
	m.view = viewLearnTarget

	client, session := m.cfg.Client, m.session

	learnCmd := func() tea.Msg {
		to, err := client.LearnTarget(ctx, learnTimeout)

		return targetMsg{session: session, to: to, err: err}
	}

	return m, tea.Batch(learnCmd, m.learnTick())
}

// newSession cancels the previous learn command and opens a new session.
func (m Model) newSession() (Model, context.Context) {
	m = m.stopLearn()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.remaining = int(learnTimeout / time.Second)

	return m, ctx
}

func (m Model) onSource(msg sourceMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.session || m.view != viewLearnSource {
		return m, nil
	}

	if next, cmd, ok := m.retryBusy(msg.err, false); ok {
		return next, cmd
	}

	if msg.err != nil {
		return m.fail(msg.err), nil
	}

	m.from, m.busyTries = msg.key, 0

	return m.startTarget()
}

func (m Model) onTarget(msg targetMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.session || m.view != viewLearnTarget {
		return m, nil
	}

	if next, cmd, ok := m.retryBusy(msg.err, true); ok {
		return next, cmd
	}

	if msg.err != nil {
		return m.fail(msg.err), nil
	}

	m = m.stopLearn()
	m.to = msg.to
	m.view = viewConfirm

	return m, nil
}

// learnKey handles keys while a learn command runs. Esc aborts, so it cannot
// be learned as a target; all other keys are ignored.
func (m Model) learnKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m = m.stopLearn()
		m.view = viewStatus
	case keyCtrlC:
		m = m.stopLearn()

		return m, tea.Quit
	}

	return m, nil
}

func (m Model) viewLearn(step string) string {
	return titleStyle.Render("skb") + "\n\n" + step + "\n" +
		dimStyle.Render(fmt.Sprintf("%d s left - esc cancels", m.remaining)) + "\n"
}

func (m Model) confirmView() string {
	return titleStyle.Render("skb") + "\n\n" +
		"Step 3/3: " + m.from + " -> " + strings.Join(m.to, "+") + "\n\n" +
		dimStyle.Render("enter save  t relearn target  esc discard") + "\n"
}

func (m Model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC, "q":
		return m, tea.Quit
	case keyEsc:
		m.view = viewStatus
	case "t":
		m.busyTries = 0

		return m.startTarget()
	case "enter":
		client := m.cfg.Client
		args := ipc.SetMappingArgs{From: m.from, To: m.to, OldFrom: m.oldFrom}

		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
			defer cancel()

			mappings, err := client.SetMapping(ctx, args)

			return savedMsg{mappings: mappings, err: err}
		}
	}

	return m, nil
}

func (m Model) deleteView() string {
	return titleStyle.Render("skb") + "\n\n" + "Delete mapping for " + m.from + "?\n\n" +
		dimStyle.Render("y delete  n cancel") + "\n"
}

func (m Model) deleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC, "q":
		return m, tea.Quit
	case "n", keyEsc:
		m.view = viewStatus
	case "y":
		client, from := m.cfg.Client, m.from

		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
			defer cancel()

			mappings, err := client.DeleteMapping(ctx, from)

			return deletedMsg{mappings: mappings, err: err}
		}
	}

	return m, nil
}

const (
	busyRetries    = 5
	busyRetryDelay = 200 * time.Millisecond
)

// learnTick schedules the next countdown step of the current session.
func (m Model) learnTick() tea.Cmd {
	session := m.session

	return m.cfg.Tick(time.Second, func(time.Time) tea.Msg { return learnTickMsg{session: session} })
}

func (m Model) learning() bool {
	return m.view == viewLearnSource || m.view == viewLearnTarget
}

func (m Model) onLearnTick(msg learnTickMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.session || !m.learning() {
		return m, nil
	}

	m.remaining = max(m.remaining-1, 0)

	if m.remaining == 0 {
		return m, nil
	}

	return m, m.learnTick()
}

// retryBusy schedules a retry when the daemon still cancels the previous
// learn session. The error is shown once the retries are used up.
func (m Model) retryBusy(err error, target bool) (Model, tea.Cmd, bool) {
	ie, ok := errors.AsType[*ipc.Error](err)
	if !ok || ie.Code != ipc.CodeBusy || m.busyTries >= busyRetries {
		return m, nil, false
	}

	m.busyTries++

	session := m.session

	return m, m.cfg.Tick(busyRetryDelay, func(time.Time) tea.Msg {
		return learnRetryMsg{session: session, target: target}
	}), true
}

func (m Model) onLearnRetry(msg learnRetryMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.session || !m.learning() {
		return m, nil
	}

	if msg.target {
		return m.startTarget()
	}

	return m.startSource()
}
