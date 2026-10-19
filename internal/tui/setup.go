package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func yesNo(v bool) string {
	if v {
		return okStyle.Render("yes")
	}

	return warnStyle.Render("no")
}

func (m Model) setupView() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Setup") + "\n\n")

	if !m.probed {
		b.WriteString(dimStyle.Render("checking...") + "\n")
	} else {
		unit := warnStyle.Render("not installed")
		if m.setup.UnitInstalled {
			unit = okStyle.Render("installed")
		}

		b.WriteString("  service unit:         " + unit + "\n")
		b.WriteString("  service active:       " + yesNo(m.setup.ServiceActive) + "\n")
		b.WriteString("  daemon reachable:     " + yesNo(m.setup.SocketOK) + "\n")
		b.WriteString("  input-remapper active: " + yesNo(m.setup.InputRemapperActive) + "\n")

		if m.setup.Err != "" {
			b.WriteString("\n" + errStyle.Render(m.setup.Err) + "\n")
		}
	}

	b.WriteString("\n" + dimStyle.Render("i install & start  x install + disable input-remapper  u uninstall  esc back") + "\n")

	return b.String()
}

func (m Model) setupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	uid := strconv.FormatUint(uint64(m.cfg.UID), 10)

	switch msg.String() {
	case "q", keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.view = viewStatus
	case "r":
		return m, m.probeCmd()
	case "i":
		return m.runPrivileged("install", "--admin-uid", uid)
	case "x":
		return m.runPrivileged("install", "--admin-uid", uid, "--disable-input-remapper")
	case "u":
		return m.runPrivileged("uninstall")
	}

	return m, nil
}

// runPrivileged releases the terminal for the polkit prompt and runs skb with elevated rights.
func (m Model) runPrivileged(args ...string) (tea.Model, tea.Cmd) {
	cmd := m.cfg.Command(args...)

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{err: err} })
}
