package tui_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/tui"
)

// toConfirm walks the add flow up to the unarmed confirm view.
func toConfirm(r *rig) {
	r.t.Helper()
	r.settle(r.key("a"))
	r.wantView("Step 3/3")
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}

	_, ok := cmd().(tea.QuitMsg)

	return ok
}

func TestManualFetchesDoNotAddPollChains(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()

	want := func(step string) {
		t.Helper()

		if n := r.countTicks(time.Hour); n != 1 {
			t.Fatalf("%s: poll timers = %d, want 1", step, n)
		}
	}

	want("loaded")

	r.settle(r.key("r"))
	want("refresh")

	toConfirm(r)
	r.arm()
	r.settle(r.key("enter"))
	want("save")

	r.key("d")
	r.settle(r.key("y"))
	want("delete")

	r.settle(r.update(tui.ExecDone(nil, "")))
	want("exec done")

	r.settle(r.update(r.fire(time.Hour)))
	want("poll")

	r.client.statusErr = errors.New("down")
	r.settle(r.update(r.fire(time.Hour)))
	want("poll error")

	r.settle(r.key("r"))
	want("refresh error")
}

func TestConfirmIgnoresKeysDuringGuard(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)

	for _, k := range []string{"q", "enter", "t", "esc", "ctrl+c"} {
		if cmd := r.key(k); cmd != nil {
			t.Errorf("%s: cmd during guard", k)
		}

		r.wantView("Step 3/3", "waiting for keys to settle")
	}

	if r.client.setCalls != 0 {
		t.Error("saved during guard")
	}

	r.arm()
	r.settle(r.key("enter"))

	if r.client.setCalls != 1 {
		t.Errorf("setCalls = %d", r.client.setCalls)
	}
}

func TestStaleArmIgnored(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)

	stale := r.fire(tui.InputGuard)
	r.update(stale)
	r.settle(r.key("t"))
	r.wantView("Step 3/3")
	r.update(stale)

	if cmd := r.key("enter"); cmd != nil {
		t.Error("stale arm ended the guard")
	}

	if r.client.setCalls != 0 {
		t.Error("saved")
	}
}

func TestCtrlCIgnoredDuringLearnTarget(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("e")
	r.wantView("Step 2/3")

	if cmd := r.key("ctrl+c"); cmd != nil {
		t.Error("ctrl+c acted during target step")
	}

	r.wantView("Step 2/3")
	r.key("esc")
	r.wantView("Devices")
}

func TestConfirmQDiscards(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)
	r.arm()

	if cmd := r.key("q"); cmd != nil {
		t.Error("q returned a cmd")
	}

	r.wantView("Devices")

	if r.client.setCalls != 0 {
		t.Error("discarded mapping was saved")
	}
}

func TestUnreachableHidesMutations(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.statusErr = errors.New("connection refused")
	r.loaded()
	r.wantView("daemon unreachable")
	r.wantNoView("add", "edit", "delete")

	for _, k := range []string{"a", "e", "d"} {
		if cmd := r.key(k); cmd != nil {
			t.Errorf("%s: cmd while unreachable", k)
		}
	}

	if r.client.sourceCalls != 0 {
		t.Error("LearnSource called")
	}
}

func TestStaleStatusBlocksLearn(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()

	r.client.statusErr = errors.New("gone")
	r.settle(r.update(r.fire(time.Hour)))

	if cmd := r.key("a"); cmd != nil {
		t.Error("learn started against a dead daemon")
	}

	r.wantView("daemon unreachable")

	if r.client.sourceCalls != 0 {
		t.Error("LearnSource called")
	}
}

func TestErrorViewCtrlCQuits(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceErr = &ipc.Error{Code: ipc.CodeTimeout, Message: "x"}
	r.loaded()
	r.settle(r.key("a"))
	r.wantView("no key pressed")

	if !isQuit(r.key("ctrl+c")) {
		t.Error("ctrl+c did not quit")
	}
}

func TestErrorViewOtherKeyReturns(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.client.sourceErr = &ipc.Error{Code: ipc.CodeTimeout, Message: "x"}
	r.loaded()
	r.settle(r.key("a"))
	r.key("x")
	r.wantView("Devices")
}

func TestDoubleEnterSavesOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)
	r.arm()

	first := r.key("enter")

	if second := r.key("enter"); second != nil {
		t.Error("second enter sent again")
	}

	r.wantView("Saving")
	r.settle(first)

	if r.client.setCalls != 1 {
		t.Errorf("setCalls = %d", r.client.setCalls)
	}
}

func TestDoubleYDeletesOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("d")

	first := r.key("y")

	if second := r.key("y"); second != nil {
		t.Error("second y sent again")
	}

	r.wantView("Deleting")
	r.settle(first)

	if r.client.delCalls != 1 {
		t.Errorf("delCalls = %d", r.client.delCalls)
	}
}

func TestDeleteQCancels(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("d")
	r.wantView("Delete mapping?", "KEY_4")

	if cmd := r.key("q"); cmd != nil {
		t.Error("q returned a cmd")
	}

	r.wantView("Devices")

	if r.client.delCalls != 0 {
		t.Error("deleted")
	}
}

func TestAddSelectsNewMappingAndFlashes(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)
	r.arm()
	r.settle(r.key("enter"))

	r.wantLine("›", "KEY_1")
	r.wantView("Mapping saved")

	older := r.fire(tui.FlashDuration)

	r.client.sourceKey = "KEY_2"
	toConfirm(r)
	r.arm()
	r.settle(r.key("enter"))

	r.update(older)
	r.wantView("Mapping saved")

	r.update(r.fire(tui.FlashDuration))
	r.wantNoView("Mapping saved")
}

func TestSpinTicksBoundToSession(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("a")

	if n := r.countTicks(tui.SpinInterval); n != 1 {
		t.Fatalf("spinner timers = %d", n)
	}

	old := r.fire(tui.SpinInterval)

	if cmd := r.update(old); cmd == nil {
		t.Fatal("spinner not rescheduled")
	}

	r.key("esc")

	if cmd := r.update(old); cmd != nil {
		t.Error("stale spinner tick rescheduled")
	}

	r.key("a")

	if cmd := r.update(old); cmd != nil {
		t.Error("spinner tick of an old session rescheduled")
	}
}

func TestInstallFailureShowsStderr(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("s")
	r.key("i")

	if r.cmds.last == nil || r.cmds.last.Stderr == nil {
		t.Fatal("stderr not captured")
	}

	lines := make([]string, 15)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%02d", i)
	}

	r.update(tui.ExecDone(errors.New("exit status 126"), strings.Join(lines, "\n")))
	r.wantView("exit status 126", "line05", "line14")
	r.wantNoView("line04")
}

func TestSmallTerminalKeepsHeader(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.update(tea.WindowSizeMsg{Width: 80, Height: 12})

	v := r.view()
	if n := strings.Count(v, "\n") + 1; n > 12 {
		t.Errorf("view has %d lines:\n%s", n, v)
	}

	r.wantView("skb", "Mappings")
}

func TestStderrIsSanitizedAndBounded(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("s")
	r.update(tui.ExecDone(errors.New("exit status 1"), "\x1b[31mred\x1b[0m\x07 bell\x00\r\n\x1b]0;title\x07ok\ttab"))
	r.wantView("exit status 1", "red bell", "tab")
	r.wantNoView("\x1b", "\x07", "\x00", "\r", "title")

	big := strings.Repeat("x", 100000) + "END"
	got := tui.TailWriterString(8192, big)

	if len(got) != 8192 || !strings.HasSuffix(got, "END") {
		t.Errorf("tail writer len=%d", len(got))
	}
}

func TestFlashAndHintFitHeight(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	toConfirm(r)
	r.arm()
	r.settle(r.key("enter"))
	r.wantView("Mapping saved")

	r.client.statusErr = errors.New("connection refused")
	r.settle(r.key("r"))
	r.key("a")
	r.wantView("daemon unreachable - press s")

	for _, h := range []int{12, 10, 8} {
		r.update(tea.WindowSizeMsg{Width: 40, Height: h})

		v := r.view()
		if n := strings.Count(v, "\n") + 1; n > h {
			t.Errorf("height %d: view has %d lines:\n%s", h, n, v)
		}

		if !strings.Contains(strings.SplitN(v, "\n", 2)[0], "skb") {
			t.Errorf("height %d: header missing:\n%s", h, v)
		}
	}
}

func TestTinyHeightKeepsHeader(t *testing.T) {
	t.Parallel()

	for _, h := range []int{1, 2, 3, 4, 5} {
		r := newRig(t, adminStatus())
		r.loaded()
		r.update(tea.WindowSizeMsg{Width: 80, Height: h})

		v := r.view()
		if n := strings.Count(v, "\n") + 1; n > h {
			t.Errorf("height %d: view has %d lines:\n%s", h, n, v)
		}

		if !strings.Contains(strings.SplitN(v, "\n", 2)[0], "skb") {
			t.Errorf("height %d: header missing:\n%s", h, v)
		}
	}
}

func TestOlderPollResultDroppedAndTickRescheduled(t *testing.T) {
	t.Parallel()

	stale := adminStatus()
	stale.Version = "stale-version"

	for _, errCase := range []bool{false, true} {
		r := newRig(t, adminStatus())
		r.loaded()
		// Consume the pending chain tick; only the dropped result may re-arm it.
		r.fire(time.Hour)

		r.update(tui.StatusMsg(adminStatus(), false, 5))

		if n := r.countTicks(time.Hour); n != 0 {
			t.Fatalf("manual result scheduled a tick: %d", n)
		}

		if errCase {
			r.update(tui.StatusErrMsg(errors.New("late failure"), true, 3))
			r.wantNoView("unreachable", "late failure")
		} else {
			r.update(tui.StatusMsg(stale, true, 3))
			r.wantNoView("stale-version")
		}

		if n := r.countTicks(time.Hour); n != 1 {
			t.Errorf("errCase=%v: poll timers = %d, want 1", errCase, n)
		}
	}
}

func TestMutationDropsInFlightPoll(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.settle(r.key("r"))
	toConfirm(r)
	r.arm()

	msgs := exec1(r.key("enter"))
	r.fire(time.Hour)
	r.update(msgs[0])

	stale := adminStatus()
	stale.Version = "stale-version"
	stale.Mappings = nil

	r.update(tui.StatusMsg(stale, true, 1))
	r.wantNoView("stale-version")
	r.wantView("KEY_1")

	if n := r.countTicks(time.Hour); n != 1 {
		t.Errorf("poll timers = %d, want 1", n)
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// toTarget starts the edit flow and returns the learn target view.
func toTarget(r *rig) tea.Cmd {
	r.t.Helper()

	cmd := r.key("e")
	r.wantView("Step 2/3")

	return cmd
}

func TestLabelCapturedDuringTargetLearn(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	cmd := toTarget(r)
	r.update(runes("ß"))
	r.update(runes("x")) // second key ignored
	r.settle(cmd)
	r.wantView("Step 3/3")
	r.wantLine("KEY_4", "→", "KEY_LEFTSHIFT+KEY_MINUS")
	r.wantLine("→", "ß")
	r.wantNoView("Shift + Minus")

	r.arm()
	r.settle(r.key("enter"))

	if r.client.lastSet.Label != "ß" {
		t.Errorf("label sent = %q", r.client.lastSet.Label)
	}

	r.wantLine("›", "KEY_4", "→", "KEY_LEFTSHIFT+KEY_MINUS", "ß")
}

func TestLabelCapturedDuringGuard(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.settle(toTarget(r))

	if cmd := r.update(runes("?")); cmd != nil {
		t.Error("label key returned a cmd")
	}

	r.wantLine("→", "?")
	r.update(runes("x"))
	r.arm()
	r.settle(r.key("enter"))

	if r.client.lastSet.Label != "?" {
		t.Errorf("label sent = %q", r.client.lastSet.Label)
	}
}

func TestLabelIgnoredAfterGuard(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.settle(toTarget(r))
	r.arm()
	r.update(runes("ß"))
	r.wantNoView("ß")
	r.wantLine("→", "Shift + Minus")
}

func TestLabelNotFromSpecialKeys(t *testing.T) {
	t.Parallel()

	for name, msg := range map[string]tea.KeyMsg{
		"ctrl":  {Type: tea.KeyCtrlA},
		"enter": {Type: tea.KeyEnter},
		"space": {Type: tea.KeySpace, Runes: []rune(" ")},
		"alt":   {Type: tea.KeyRunes, Runes: []rune("x"), Alt: true},
		"f5":    {Type: tea.KeyF5},
		"ctrlc": {Type: tea.KeyCtrlC},
	} {
		r := newRig(t, adminStatus())
		r.loaded()
		cmd := toTarget(r)
		r.update(msg)
		r.update(runes("ß")) // first key decides; later ones are ignored
		r.settle(cmd)
		r.arm()
		r.settle(r.key("enter"))

		if r.client.lastSet.Label != "" {
			t.Errorf("%s: label = %q", name, r.client.lastSet.Label)
		}
	}
}

func TestLabelResetOnRelearn(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	cmd := toTarget(r)
	r.update(runes("ß"))
	r.settle(cmd)
	r.arm()
	r.settle(r.key("t"))
	r.wantNoView("ß")
	r.arm()
	r.settle(r.key("enter"))

	if r.client.lastSet.Label != "" {
		t.Errorf("label = %q", r.client.lastSet.Label)
	}
}

func TestDeleteViewShowsLabel(t *testing.T) {
	t.Parallel()

	st := adminStatus()
	st.Mappings[0].Label = "ß"
	r := newRig(t, st)
	r.loaded()
	r.key("d")
	r.wantLine("→", "ß")
}
