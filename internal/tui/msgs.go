package tui

import "github.com/elias-knodel/skb/internal/ipc"

type (
	statusMsg    ipc.Status
	statusErrMsg struct{ err error }
	tickMsg      struct{}
	probeMsg     struct{ state SetupState }
	execDoneMsg  struct{ err error }

	sourceMsg struct {
		session int
		key     string
		err     error
	}

	targetMsg struct {
		session int
		to      []string
		err     error
	}

	savedMsg struct {
		mappings []ipc.Mapping
		err      error
	}

	deletedMsg struct {
		mappings []ipc.Mapping
		err      error
	}
)

type (
	// learnTickMsg advances the countdown of one learn session.
	learnTickMsg struct{ session int }
	// learnRetryMsg restarts a learn call that found the daemon busy.
	learnRetryMsg struct {
		session int
		target  bool
	}
)
