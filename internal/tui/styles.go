package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	cursorMark = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Render(">")
)
