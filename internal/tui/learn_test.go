package tui_test

import (
	"errors"
	"testing"
	"time"

	"github.com/elias-knodel/skb/internal/ipc"
)

func TestAddFlow(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()

	cmd := r.key("a")
	r.wantView("Step 1/3")

	msgs := exec1(cmd)
	if r.client.lastTimeout != 10*time.Second {
		t.Errorf("timeout = %v", r.client.lastTimeout)
	}

	cmd = r.update(msgs[0])
	r.wantView("Step 2/3")

	r.settle(cmd)
	r.wantView("Step 3/3", "KEY_1 -> KEY_LEFTSHIFT+KEY_MINUS")

	r.settle(r.key("enter"))

	if r.client.lastSet.From != "KEY_1" || r.client.lastSet.OldFrom != "" ||
		len(r.client.lastSet.To) != 2 || r.client.lastSet.To[1] != "KEY_MINUS" {
		t.Errorf("set = %+v", r.client.lastSet)
	}

	r.wantView("Devices")
}

func TestEditSkipsSourceStep(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()

	cmd := r.key("e")
	r.wantView("Step 2/3")
	r.settle(cmd)
	r.wantView("Step 3/3", "KEY_4 -> KEY_LEFTSHIFT+KEY_MINUS")
	r.settle(r.key("enter"))

	if r.client.sourceCalls != 0 || r.client.lastSet.From != "KEY_4" || r.client.lastSet.OldFrom != "KEY_4" {
		t.Errorf("calls=%d set=%+v", r.client.sourceCalls, r.client.lastSet)
	}
}

func TestConfirmRelearnAndDiscard(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.settle(r.key("a"))
	r.wantView("Step 3/3")

	r.settle(r.key("t"))
	r.wantView("Step 3/3")

	r.key("esc")
	r.wantView("Devices")

	if r.client.setCalls != 0 {
		t.Error("discarded mapping was saved")
	}
}

func TestDeleteFlow(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()

	r.key("d")
	r.key("n")

	if r.client.delCalls != 0 {
		t.Error("delete called after n")
	}

	r.key("d")
	r.settle(r.key("y"))

	if r.client.delCalls != 1 || r.client.lastDel != "KEY_4" {
		t.Errorf("delete calls=%d last=%q", r.client.delCalls, r.client.lastDel)
	}
}

func TestEscCancelsLearnAndDropsStaleResult(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceBlock = true
	r.client.sourceDone = make(chan struct{})
	r.loaded()

	cmd := r.key("a")
	result := runAsync(cmd)

	r.key("esc")

	select {
	case <-r.client.sourceDone:
	case <-time.After(2 * time.Second):
		t.Fatal("learn context not cancelled")
	}

	r.wantView("Devices")

	// The late result belongs to a dead session and must not change the view.
	r.update(<-result)
	r.wantView("Devices")
}

func TestForbiddenShowsAdminHint(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceErr = &ipc.Error{Code: ipc.CodeForbidden, Message: "no"}
	r.loaded()
	r.settle(r.key("a"))
	r.wantView("not admin_uid")
}

func TestTimeoutMessage(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceErr = &ipc.Error{Code: ipc.CodeTimeout, Message: "x"}
	r.loaded()
	r.settle(r.key("a"))
	r.wantView("no key pressed within 10 s")

	r.key("x")
	r.wantView("Devices")
}

func TestOtherErrorShown(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.setErr = errors.New("disk exploded")
	r.loaded()
	r.settle(r.key("a"))
	r.settle(r.key("enter"))
	r.wantView("disk exploded")
}

func TestCtrlCDuringLearnCancelsAndQuits(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceBlock = true
	r.client.sourceDone = make(chan struct{})
	r.loaded()

	cmd := r.key("a")

	runAsync(cmd)

	quit := r.key("ctrl+c")
	if quit == nil {
		t.Fatal("no quit cmd")
	}

	select {
	case <-r.client.sourceDone:
	case <-time.After(2 * time.Second):
		t.Fatal("learn context not cancelled")
	}

}

func TestCountdownTicks(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("a")
	r.wantView("10 s left")

	for _, want := range []string{"9 s left", "8 s left"} {
		if cmd := r.update(r.fire(time.Second)); cmd == nil {
			t.Fatal("countdown not rescheduled")
		}

		r.wantView(want)
	}
}

func TestStaleAndStoppedTicksAreDropped(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("a")

	first := r.fire(time.Second)

	// Leaving the learn view invalidates the session.
	r.key("esc")
	r.wantView("Devices")

	if cmd := r.update(first); cmd != nil {
		t.Error("stale tick rescheduled")
	}

	// A new session ignores ticks of the previous one.
	r.key("a")
	current := r.fire(time.Second)

	if cmd := r.update(first); cmd != nil {
		t.Error("tick of an old session rescheduled")
	}

	r.wantView("10 s left")

	if cmd := r.update(current); cmd == nil {
		t.Error("current tick dropped")
	}

	r.wantView("9 s left")
}

func TestBusyIsRetried(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceBusy = 2
	r.loaded()

	msgs := exec1(r.key("a"))

	for range 2 {
		cmd := r.update(msgs[0])
		if cmd == nil {
			t.Fatal("no retry scheduled")
		}

		r.wantView("Step 1/3")

		msgs = exec1(r.update(r.fire(200 * time.Millisecond)))
	}

	r.settle(r.update(msgs[0]))
	r.wantView("Step 3/3")
}

func TestBusyGivesUpAfterRetries(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceBusy = 100
	r.loaded()

	msgs := exec1(r.key("a"))

	for range 5 {
		r.update(msgs[0])
		msgs = exec1(r.update(r.fire(200 * time.Millisecond)))
	}

	r.update(msgs[0])
	r.wantView("busy")
	r.key("x")
	r.wantView("Devices")
}
