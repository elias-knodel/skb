package daemon

import "time"

// backoff tracks consecutive failures of one path.
type backoff struct {
	failures int
	next     time.Time
}

// markFailure records a failure of path. A run of at least StableAfter
// resets the failure count first, so a long-lived worker restarts quickly.
func (d *Daemon) markFailure(path string, ran time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.backoff[path]
	if !ok {
		b = &backoff{}
		d.backoff[path] = b
	}

	if ran >= d.opts.StableAfter {
		b.failures = 0
	}

	b.failures++

	delay := d.opts.BackoffBase
	for i := 1; i < b.failures && delay < d.opts.BackoffMax; i++ {
		delay *= 2
	}

	b.next = time.Now().Add(min(delay, d.opts.BackoffMax))
}

// blocked reports whether path is still inside its backoff window.
func (d *Daemon) blocked(path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.backoff[path]

	return ok && time.Now().Before(b.next)
}

// nextRetry returns the wait until the earliest pending backoff expires.
func (d *Daemon) nextRetry() (time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var (
		wait  time.Duration
		found bool
	)

	for _, b := range d.backoff {
		if remaining := time.Until(b.next); remaining > 0 && (!found || remaining < wait) {
			wait, found = remaining, true
		}
	}

	return wait, found
}
