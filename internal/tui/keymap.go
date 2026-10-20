package tui

import "github.com/charmbracelet/bubbles/key"

// Bindings are display only; handlers match on msg.String().
var (
	bindAdd     = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add"))
	bindEdit    = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit"))
	bindDelete  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	bindNav     = key.NewBinding(key.WithKeys("up", "down", "k", "j"), key.WithHelp("↑/↓", "select"))
	bindSetup   = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "setup"))
	bindRefresh = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	bindQuit    = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	bindHelp    = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))

	bindSave      = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save"))
	bindRelearn   = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "re-learn target"))
	bindDiscard   = key.NewBinding(key.WithKeys("q", "esc"), key.WithHelp("q/esc", "discard"))
	bindYes       = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete"))
	bindNo        = key.NewBinding(key.WithKeys("n", "esc", "q"), key.WithHelp("n/esc/q", "cancel"))
	bindCancel    = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	bindAnyKey    = key.NewBinding(key.WithKeys("any"), key.WithHelp("any key", "return"))
	bindCtrlCQuit = key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit"))

	bindInstall   = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "install & start"))
	bindInstallX  = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "install + disable input-remapper"))
	bindUninstall = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "uninstall"))
	bindRecheck   = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "re-check"))
	bindBack      = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
)

// keyMap implements help.KeyMap for the current view.
type keyMap struct {
	short []key.Binding
	full  [][]key.Binding
}

// ShortHelp implements help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding { return k.short }

// FullHelp implements help.KeyMap.
func (k keyMap) FullHelp() [][]key.Binding {
	if k.full == nil {
		return [][]key.Binding{k.short}
	}

	return k.full
}

func (m Model) keyMap() keyMap {
	switch m.view {
	case viewStatus:
		return m.statusKeyMap()
	case viewConfirm:
		return keyMap{short: []key.Binding{bindSave, bindRelearn, bindDiscard}}
	case viewDeleteConfirm:
		return keyMap{short: []key.Binding{bindYes, bindNo}}
	case viewSetup:
		return keyMap{
			short: []key.Binding{bindInstall, bindInstallX, bindUninstall, bindRecheck, bindBack, bindQuit},
			full: [][]key.Binding{
				{bindInstall, bindInstallX, bindUninstall},
				{bindRecheck, bindBack},
				{bindQuit, bindHelp},
			},
		}
	case viewError:
		return keyMap{short: []key.Binding{bindAnyKey, bindCtrlCQuit}}
	default:
		return keyMap{short: []key.Binding{bindCancel}}
	}
}

func (m Model) statusKeyMap() keyMap {
	app := []key.Binding{bindSetup, bindRefresh, bindQuit, bindHelp}

	if !m.canMutate() {
		return keyMap{short: app, full: [][]key.Binding{app}}
	}

	short := []key.Binding{bindAdd}
	actions := []key.Binding{bindAdd}

	if len(m.status.Mappings) > 0 {
		short = append(short, bindEdit, bindDelete, bindNav)
		actions = append(actions, bindEdit, bindDelete)
	}

	short = append(short, app...)

	return keyMap{
		short: short,
		full:  [][]key.Binding{actions, {bindNav}, app},
	}
}
