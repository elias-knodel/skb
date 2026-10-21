package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// check renders a checklist line; bad selects the style of a failed check.
func check(ok bool, okText, badText string, bad lipgloss.Style) string {
	if ok {
		return okStyle.Render("✔ " + okText)
	}

	return bad.Render("✘ " + badText)
}

func (m Model) setupView() string {
	lines := []string{titleStyle.Render("Setup"), ""}

	if !m.probed {
		lines = append(lines, dimStyle.Render("checking…"))
	} else {
		lines = append(lines,
			check(m.setup.UnitInstalled, "service unit installed", "service unit not installed", errStyle),
			check(m.setup.ServiceActive, "service active", "service not active", errStyle),
			check(m.setup.SocketOK, "daemon reachable", "daemon not reachable", errStyle),
			check(!m.setup.InputRemapperActive, "input-remapper inactive", "input-remapper active", warnStyle),
		)

		if m.setup.Err != "" {
			lines = append(lines, "", errStyle.Render(m.setup.Err))
		}
	}

	return m.screen(cardStyle.Width(m.contentWidth()-2).Render(strings.Join(lines, "\n")), "")
}

func (m Model) setupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	uid := strconv.FormatUint(uint64(m.cfg.UID), 10)

	switch msg.String() {
	case "q", keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.view = viewStatus
	case "?":
		m.help.ShowAll = !m.help.ShowAll
	case "r":
		return m.refresh(true)
	case "i":
		return m.runPrivileged("install", "--admin-uid", uid)
	case "x":
		return m.runPrivileged("install", "--admin-uid", uid, "--disable-input-remapper")
	case "u":
		return m.runPrivileged("uninstall")
	}

	return m, nil
}

// stderrLimit bounds the captured installer output.
const stderrLimit = 8 << 10

// tailBuffer keeps only the last limit bytes written to it.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}

	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.buf) }

// runPrivileged releases the terminal for the polkit prompt and runs skb with elevated rights.
func (m Model) runPrivileged(args ...string) (tea.Model, tea.Cmd) {
	cmd := m.cfg.Command(args...)

	// stdin and stdout stay on the terminal for the polkit prompt.
	stderr := &tailBuffer{limit: stderrLimit}
	cmd.Stderr = stderr

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execDoneMsg{err: err, stderr: stderr.String()}
	})
}
