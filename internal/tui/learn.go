package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/config"
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

		m.from, m.to, m.label = sel.From, sel.To, sel.Label
		m.view, m.pending = viewDeleteConfirm, false

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

	return m, tea.Batch(learnCmd, m.learnTick(), m.spinTick())
}

func (m Model) startTarget() (tea.Model, tea.Cmd) {
	m, ctx := m.newSession()
	m.label, m.labelSet = "", false
	m.view = viewLearnTarget

	client, session := m.cfg.Client, m.session

	learnCmd := func() tea.Msg {
		to, err := client.LearnTarget(ctx, learnTimeout)

		return targetMsg{session: session, to: to, err: err}
	}

	return m, tea.Batch(learnCmd, m.learnTick(), m.spinTick())
}

// newSession cancels the previous learn command and opens a new session.
func (m Model) newSession() (Model, context.Context) {
	m = m.stopLearn()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.remaining = int(learnTimeout / time.Second)
	m.spinFrame = 0

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
	m.armed, m.pending = false, false

	session := m.session

	return m, m.cfg.Tick(inputGuard, func(time.Time) tea.Msg { return confirmArmMsg{session: session} })
}

// onConfirmArm ends the input guard if it belongs to the current confirm view.
func (m Model) onConfirmArm(msg confirmArmMsg) Model {
	if msg.session == m.session && m.view == viewConfirm {
		m.armed = true
	}

	return m
}

// learnKey handles keys while a learn command runs. Esc aborts, so it cannot
// be learned as a target; ctrl+c is ignored during the target step because
// it may still be in flight from the learned key.
func (m Model) learnKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.view == viewLearnTarget {
		m = m.captureLabel(msg)
	}

	switch msg.String() {
	case keyEsc:
		m = m.stopLearn()
		m.view = viewStatus
	case keyCtrlC:
		if m.view == viewLearnTarget {
			return m, nil
		}

		m = m.stopLearn()

		return m, tea.Quit
	}

	return m, nil
}

func stepDots(step int) string {
	dots := make([]string, 3)

	for i := range dots {
		if i < step {
			dots[i] = accent.Render("●")
		} else {
			dots[i] = dimStyle.Render("○")
		}
	}

	return strings.Join(dots, " ")
}

// mappingLine renders the learned mapping.
func mappingLine(from string, to []string, label string) string {
	line := sourceStyle.Bold(true).Render(from) + "  " + arrowStyle.Render("→") + "  " +
		targetStyle.Bold(true).Render(strings.Join(to, "+"))

	if len(to) > 0 {
		line += "\n" + dimStyle.Render(keyLabel(from)+" → "+targetLabel(to, label))
	}

	return line
}

func (m Model) viewLearn(step int, text string) string {
	var b strings.Builder

	b.WriteString(stepDots(step) + "\n\n")
	b.WriteString(titleStyle.Render(fmt.Sprintf("Step %d/3", step)) + dimStyle.Render(" · ") + text + "\n")

	if step == 2 {
		b.WriteString(dimStyle.Render("Source  ") + sourceStyle.Render(m.from) + "\n")
	}

	frames := spinner.MiniDot.Frames
	ratio := float64(m.remaining) / float64(learnTimeout/time.Second)

	b.WriteString("\n" + accent.Render(frames[m.spinFrame%len(frames)]) + " " + m.bar.ViewAs(ratio) + " " +
		dimStyle.Render(fmt.Sprintf("%d s", m.remaining)))

	return m.screen(learnBoxStyle.Render(b.String()), "")
}

func (m Model) confirmView() string {
	content := stepDots(3) + "\n\n" + titleStyle.Render("Step 3/3 · Save this mapping?") + "\n\n" +
		mappingLine(m.from, m.to, m.label)

	if m.pending {
		content += "\n\n" + dimStyle.Render("Saving…")
	}

	return m.screen(confirmBoxStyle.Render(content), "")
}

func (m Model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.armed {
		m = m.captureLabel(msg)

		return m, nil
	}

	s := msg.String()

	if m.pending {
		if s == keyCtrlC {
			return m, tea.Quit
		}

		return m, nil
	}

	switch s {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, "q":
		m.view = viewStatus
	case "t":
		m.busyTries = 0

		return m.startTarget()
	case "enter":
		m.pending = true

		client := m.cfg.Client
		args := ipc.SetMappingArgs{From: m.from, To: m.to, OldFrom: m.oldFrom, Label: m.label}

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
	content := titleStyle.Render("Delete mapping?") + "\n\n" + mappingLine(m.from, m.to, m.label)

	if m.pending {
		content += "\n\n" + dimStyle.Render("Deleting…")
	}

	return m.screen(deleteBoxStyle.Render(content), "")
}

func (m Model) deleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()

	if s == keyCtrlC {
		return m, tea.Quit
	}

	if m.pending {
		return m, nil
	}

	switch s {
	case "n", keyEsc, "q":
		m.view = viewStatus
	case "y":
		m.pending = true

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

// spinTick schedules the next spinner frame of the current session.
func (m Model) spinTick() tea.Cmd {
	session := m.session

	return m.cfg.Tick(spinFPS, func(time.Time) tea.Msg { return spinTickMsg{session: session} })
}

func (m Model) onSpinTick(msg spinTickMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.session || !m.learning() {
		return m, nil
	}

	m.spinFrame++

	return m, m.spinTick()
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

// captureLabel records the character of the first terminal key of the target
// session. The learned key reaches the terminal as well, possibly after the
// daemon answered. Only plain printable runes count; it never triggers actions.
func (m Model) captureLabel(msg tea.KeyMsg) Model {
	if m.labelSet {
		return m
	}

	m.labelSet = true

	if msg.Type != tea.KeyRunes || msg.Alt || msg.Paste {
		return m
	}

	if s := string(msg.Runes); s != "" && config.ValidateLabel(s) == nil {
		m.label = s
	}

	return m
}
