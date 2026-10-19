package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/ipc"
)

// connection kinds by USB product ID.
const (
	productWired    = 0x1bb2
	productWireless = 0x1bdc
)

func connKind(product uint16) string {
	switch product {
	case productWired:
		return "wired"
	case productWireless:
		return "wireless"
	default:
		return "other"
	}
}

func (m Model) statusView() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("skb") + "\n\n")

	switch {
	case !m.loaded:
		b.WriteString(dimStyle.Render("connecting...") + "\n")
	case m.statusErr != nil && outdated(m.statusErr):
		b.WriteString(errStyle.Render("daemon outdated - reinstall via setup (s)") + "\n")
	case m.statusErr != nil:
		b.WriteString(errStyle.Render("daemon unreachable: "+m.statusErr.Error()) + "\n")
	default:
		m.writeStatus(&b)
	}

	if m.hint != "" {
		b.WriteString("\n" + warnStyle.Render(m.hint) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render(m.help()) + "\n")

	return b.String()
}

func (m Model) help() string {
	if m.statusErr == nil && m.loaded && !m.status.CallerIsAdmin {
		return "s setup  r refresh  q quit"
	}

	return "a add  e edit  d delete  s setup  r refresh  q quit"
}

func (m Model) writeStatus(b *strings.Builder) {
	st := m.status

	fmt.Fprintf(b, "version %s, up since %s\n\n", st.Version, st.StartedAt.Format(time.DateTime))

	b.WriteString(titleStyle.Render("Devices") + "\n")

	if len(st.Devices) == 0 {
		b.WriteString(dimStyle.Render("  none matched") + "\n")
	}

	for _, d := range st.Devices {
		state := warnStyle.Render("not grabbed")
		if d.Grabbed {
			state = okStyle.Render("grabbed")
		}

		fmt.Fprintf(b, "  %s  %s  %s  0x%04x:0x%04x  %s\n", d.Path, d.Name, connKind(d.Product), d.Vendor, d.Product, state)

		if d.VirtualName != "" {
			fmt.Fprintf(b, "    -> %s\n", d.VirtualName)
		}

		if d.Error != "" {
			b.WriteString("    " + errStyle.Render(d.Error) + "\n")

			if strings.Contains(d.Error, "busy") {
				b.WriteString("    " + warnStyle.Render("input-remapper may hold this device; disable it in setup (s, x)") + "\n")
			}
		}
	}

	b.WriteString("\n" + titleStyle.Render("Mappings") + "\n")

	if len(st.Mappings) == 0 {
		b.WriteString(dimStyle.Render("  none - press a to add one") + "\n")
	}

	for i, mp := range st.Mappings {
		mark := " "
		if i == m.cursor {
			mark = cursorMark
		}

		fmt.Fprintf(b, " %s %s -> %s\n", mark, mp.From, strings.Join(mp.To, "+"))
	}
}

func (m Model) selected() (ipc.Mapping, bool) {
	if m.cursor < 0 || m.cursor >= len(m.status.Mappings) {
		return ipc.Mapping{}, false
	}

	return m.status.Mappings[m.cursor], true
}

// onKey routes key presses by view.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.view {
	case viewStatus:
		return m.statusKey(msg)
	case viewLearnSource, viewLearnTarget:
		return m.learnKey(msg)
	case viewConfirm:
		return m.confirmKey(msg)
	case viewDeleteConfirm:
		return m.deleteKey(msg)
	case viewSetup:
		return m.setupKey(msg)
	case viewError:
		m.view = viewStatus

		return m, nil
	}

	return m, nil
}

func (m Model) statusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.hint = ""

	switch msg.String() {
	case "q", keyCtrlC:
		return m, tea.Quit
	case "r":
		return m, m.fetchCmd()
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.status.Mappings)-1, 0))
	case "s":
		m.view = viewSetup

		return m, m.probeCmd()
	case "a", "e", "d":
		if !m.status.CallerIsAdmin {
			m.hint = m.notAdminText()

			return m, nil
		}

		return m.startAction(msg.String())
	}

	return m, nil
}
