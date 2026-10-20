package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"
)

// Palette hex values for the progress gradient.
const (
	gradientFrom = "#9D7CFF"
	gradientTo   = "#FF79C6"
)

var (
	colPurple = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#9D7CFF"}
	colPink   = lipgloss.AdaptiveColor{Light: "#C2185B", Dark: "#FF79C6"}
	colCyan   = lipgloss.AdaptiveColor{Light: "#00838F", Dark: "#8BE9FD"}
	colGreen  = lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#50FA7B"}
	colRed    = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF5555"}
	colYellow = lipgloss.AdaptiveColor{Light: "#9E6A00", Dark: "#F1FA8C"}
	colDim    = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6C6C6C"}
	colBorder = lipgloss.AdaptiveColor{Light: "#C0C0C0", Dark: "#44475A"}
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(colGreen)
	warnStyle  = lipgloss.NewStyle().Foreground(colYellow)
	errStyle   = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(colDim)
	flashStyle = lipgloss.NewStyle().Foreground(colGreen)
	accent     = lipgloss.NewStyle().Foreground(colPurple).Bold(true)

	pillStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(colPurple).Bold(true).Padding(0, 1)

	cardStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	errCardStyle    = cardStyle.BorderForeground(colRed)
	learnBoxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPurple).Padding(1, 2)
	confirmBoxStyle = learnBoxStyle
	deleteBoxStyle  = learnBoxStyle.BorderForeground(colRed)
	errBoxStyle     = learnBoxStyle.BorderForeground(colRed)

	tableHeaderStyle   = lipgloss.NewStyle().Foreground(colDim).Bold(true).Padding(0, 1)
	tableCellStyle     = lipgloss.NewStyle().Padding(0, 1)
	tableSelectedStyle = lipgloss.NewStyle().Bold(true).Background(colBorder)
	sourceStyle        = lipgloss.NewStyle().Foreground(colPink)
	targetStyle        = lipgloss.NewStyle().Foreground(colCyan)
	arrowStyle         = lipgloss.NewStyle().Foreground(colDim)

	helpKeyStyle  = lipgloss.NewStyle().Foreground(colPink)
	helpDescStyle = lipgloss.NewStyle().Foreground(colDim)
	helpSepStyle  = lipgloss.NewStyle().Foreground(colDim)
)

// badgeStyle renders a short status label in color c.
func badgeStyle(c lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c).Bold(true)
}

func newHelp() help.Model {
	h := help.New()
	h.ShortSeparator = " • "
	h.Styles.ShortKey, h.Styles.FullKey = helpKeyStyle, helpKeyStyle
	h.Styles.ShortDesc, h.Styles.FullDesc = helpDescStyle, helpDescStyle
	h.Styles.ShortSeparator, h.Styles.FullSeparator, h.Styles.Ellipsis = helpSepStyle, helpSepStyle, helpSepStyle

	return h
}
