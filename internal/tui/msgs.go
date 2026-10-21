package tui

import (
	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/update"
)

type (
	// statusMsg carries a status fetch; poll marks results of the poll chain
	// and seq orders fetches.
	statusMsg struct {
		st   ipc.Status
		poll bool
		seq  int
	}

	statusErrMsg struct {
		err  error
		poll bool
		seq  int
	}

	tickMsg  struct{}
	probeMsg struct{ state SetupState }

	// updateMsg carries the result of the release check.
	updateMsg struct{ res update.Result }

	// execDoneMsg reports a finished privileged command with its captured stderr.
	execDoneMsg struct {
		err    error
		stderr string
	}

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
	// refreshTickMsg advances the spinner frame of one manual refresh.
	refreshTickMsg struct{ seq int }
	// spinTickMsg advances the spinner frame of one learn session.
	spinTickMsg struct{ session int }
	// confirmArmMsg ends the input guard of the confirm view.
	confirmArmMsg struct{ session int }
	// flashExpireMsg clears the flash message with the given sequence number.
	flashExpireMsg struct{ seq int }
)
