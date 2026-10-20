package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/ipc"
)

// Timing constants and message constructors exposed to the external tests.
const (
	InputGuard    = inputGuard
	FlashDuration = flashDuration
	SpinInterval  = spinFPS
)

// ExecDone builds the message of a finished privileged command.
func ExecDone(err error, stderr string) tea.Msg { return execDoneMsg{err: err, stderr: stderr} }

// Label helpers exposed to the external tests.
var (
	KeyLabel   = keyLabel
	ComboLabel = comboLabel
)

// TailWriterString writes in to a tail buffer of the given limit and returns its content.
func TailWriterString(limit int, in string) string {
	b := &tailBuffer{limit: limit}
	_, _ = b.Write([]byte(in))

	return b.String()
}

// StatusMsg and StatusErrMsg build fetch results with an explicit sequence number.
func StatusMsg(st ipc.Status, poll bool, seq int) tea.Msg {
	return statusMsg{st: st, poll: poll, seq: seq}
}

func StatusErrMsg(err error, poll bool, seq int) tea.Msg {
	return statusErrMsg{err: err, poll: poll, seq: seq}
}
