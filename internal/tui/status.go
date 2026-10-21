package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/elias-knodel/skb/internal/ipc"
)

// connection kinds by USB product ID.
const (
	productWired    = 0x1bb2
	productWireless = 0x1bdc
)

const (
	hintUnreachable = "daemon unreachable - press s for setup or r to retry"
	// minMapLines is the number of mapping lines kept visible on small terminals.
	minMapLines = 4

	hintOutdated = "daemon outdated - reinstall via setup (s)"
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

// daemonUp reports a loaded status without a fetch error.
func (m Model) daemonUp() bool { return m.loaded && m.statusErr == nil }

// canMutate reports whether mappings may be changed.
func (m Model) canMutate() bool { return m.daemonUp() && m.status.CallerIsAdmin }

func (m Model) statusView() string {
	var body string

	switch {
	case !m.loaded:
		body = dimStyle.Render("connecting…")
	case m.statusErr != nil:
		body = m.unreachableCard()
	default:
		body = m.statusBody()
	}

	return m.screen(body, m.extraBlock())
}

// extraBlock renders the flash and hint lines below the body, wrapped to the
// content width.
func (m Model) extraBlock() string {
	var extra []string

	if m.flash != "" {
		extra = append(extra, flashStyle.Render("✔ "+m.flash))
	}

	if m.hint != "" {
		extra = append(extra, warnStyle.Render(m.hint))
	}

	if len(extra) == 0 {
		return ""
	}

	return lipgloss.NewStyle().Width(m.contentWidth()).Render(strings.Join(extra, "\n"))
}

func (m Model) unreachableCard() string {
	text, hint := "daemon unreachable: "+m.statusErr.Error(), "press s for setup or r to retry"
	if outdated(m.statusErr) {
		text, hint = hintOutdated, ""
	}

	content := errStyle.Render(text)
	if hint != "" {
		content += "\n" + dimStyle.Render(hint)
	}

	return errCardStyle.Width(m.contentWidth() - 2).Render(content)
}

func (m Model) headerView() string {
	parts := []string{pillStyle.Render(" skb ")}

	if m.daemonUp() {
		info := "version " + m.status.Version
		if up := m.uptime(); up != "" {
			info += " · up " + up
		}

		parts = append(parts, dimStyle.Render(info))
	}

	switch {
	case !m.loaded:
		parts = append(parts, dimStyle.Render("● connecting"))
	case m.statusErr != nil && outdated(m.statusErr):
		parts = append(parts, badgeStyle(colRed).Render("● outdated"))
	case m.statusErr != nil:
		parts = append(parts, badgeStyle(colRed).Render("● unreachable"))
	default:
		parts = append(parts, badgeStyle(colGreen).Render("● running"))
	}

	if m.refreshing {
		frames := spinner.MiniDot.Frames
		parts = append(parts, accent.Render(frames[m.refreshFrame%len(frames)])+dimStyle.Render(" refreshing"))
	}

	header := strings.Join(parts, " ")
	if m.update.Available {
		header += "\n" + warnStyle.Render("update available: "+sanitize(m.update.Latest)) +
			dimStyle.Render(" · "+sanitize(m.update.URL))
	}

	return header
}

func (m Model) uptime() string {
	started := m.status.StartedAt
	if started.IsZero() {
		return ""
	}

	d := max(m.cfg.Now().Sub(started).Round(time.Minute), 0)
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func (m Model) statusBody() string {
	w := m.contentWidth()

	var devices strings.Builder

	devices.WriteString(titleStyle.Render("Devices") + "\n")

	if len(m.status.Devices) == 0 {
		devices.WriteString(dimStyle.Render("  none matched"))
	}

	for i, d := range m.status.Devices {
		if i > 0 {
			devices.WriteString("\n")
		}

		devices.WriteString(deviceCard(d, w))
	}

	dev, rows := devices.String(), 0

	if m.height > 0 {
		avail := m.bodyAvail(m.extraBlock())

		// Keep a few mapping lines visible by cutting the device list first.
		minMaps := min(len(m.status.Mappings)+1, minMapLines)
		dev = fitHeight(dev, avail-2-minMaps)
		rows = max(avail-lipgloss.Height(dev)-2, 2)
	}

	return dev + "\n\n" + titleStyle.Render("Mappings") + "\n" + m.mappingsView(rows)
}

func deviceCard(d ipc.Device, w int) string {
	badge := badgeStyle(colYellow).Render("not grabbed")

	switch {
	case d.Error != "":
		badge = badgeStyle(colRed).Render("error")
	case d.Grabbed:
		badge = badgeStyle(colGreen).Render("grabbed")
	}

	lines := []string{
		titleStyle.Render(d.Name) + "  " + badge + "  " + dimStyle.Render(connKind(d.Product)),
		dimStyle.Render(fmt.Sprintf("%s · 0x%04x:0x%04x", d.Path, d.Vendor, d.Product)),
	}

	if d.VirtualName != "" {
		lines = append(lines, dimStyle.Render("→ "+d.VirtualName))
	}

	if d.Error != "" {
		lines = append(lines, errStyle.Render(d.Error))

		if strings.Contains(d.Error, "busy") {
			lines = append(lines, warnStyle.Render("input-remapper may hold this device; disable it in setup (s, x)"))
		}
	}

	return cardStyle.Width(w - 2).Render(strings.Join(lines, "\n"))
}

// mappingsView renders the mappings table; maxLines <= 0 means unlimited.
func (m Model) mappingsView(maxLines int) string {
	mps := m.status.Mappings

	if len(mps) == 0 {
		text := "  none"
		if m.canMutate() {
			text = "  none - press a to add one"
		}

		return dimStyle.Render(text)
	}

	visible := len(mps)

	// One line for the table header; two more for the scroll indicators.
	if maxLines > 0 && len(mps) > maxLines-1 {
		visible = max(maxLines-3, 1)
	}

	offset := max(min(m.cursor-visible/2, len(mps)-visible), 0)
	labels := m.contentWidth() >= wideWidth

	rows := make([][]string, 0, visible)

	for i := offset; i < offset+visible; i++ {
		mark := " "
		if i == m.cursor {
			mark = "›"
		}

		row := []string{mark, mps[i].From, "→", strings.Join(mps[i].To, "+")}
		if labels {
			row = append(row, keyLabel(mps[i].From)+" → "+targetLabel(mps[i].To, mps[i].Label))
		}

		rows = append(rows, row)
	}

	headers := []string{"", "Source", "", "Target"}
	if labels {
		headers = append(headers, "Name")
	}

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return tableHeaderStyle
			}

			s := tableCellStyle

			switch col {
			case 0:
				s = s.Foreground(colPurple)
			case 1:
				s = s.Foreground(colPink)
			case 2:
				s = s.Foreground(colDim)
			case 3:
				s = s.Foreground(colCyan)
			default:
				s = s.Foreground(colDim)
			}

			if row == m.cursor-offset {
				s = s.Inherit(tableSelectedStyle)
			}

			return s
		})

	var b strings.Builder

	if offset > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("↑ %d more", offset)) + "\n")
	}

	b.WriteString(t.String())

	if rest := len(mps) - offset - visible; rest > 0 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("↓ %d more", rest)))
	}

	return b.String()
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
		if msg.String() == keyCtrlC {
			return m, tea.Quit
		}

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
		m, cmd := m.refresh(false)

		return m, cmd
	case "?":
		m.help.ShowAll = !m.help.ShowAll
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.status.Mappings)-1, 0))
	case "s":
		m.view = viewSetup

		return m, m.probeCmd()
	case "a", "e", "d":
		switch {
		case !m.daemonUp():
			m.hint = hintUnreachable
			if m.statusErr != nil && outdated(m.statusErr) {
				m.hint = hintOutdated
			}

			return m, nil
		case !m.status.CallerIsAdmin:
			m.hint = m.notAdminText()

			return m, nil
		}

		return m.startAction(msg.String())
	}

	return m, nil
}
