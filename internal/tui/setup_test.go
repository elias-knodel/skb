package tui_test

import (
	"slices"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/ipc"
	"github.com/elias-knodel/skb/internal/tui"
)

func TestUnreachableWithoutUnitOpensSetup(t *testing.T) {
	t.Parallel()

	r := newRig(t, ipc.Status{})
	r.client.statusErr = &ipc.Error{Code: ipc.CodeInternal, Message: "x"}
	r.m = tui.New(tui.Config{
		Client:   r.client,
		Prober:   fakeProber{state: tui.SetupState{UnitInstalled: false}},
		Command:  r.cmds.factory,
		UID:      1000,
		Interval: time.Hour,
		Tick:     noTick,
	})

	r.loaded()
	r.wantView("Setup", "not installed")
}

func TestSetupInstall(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("s")
	r.wantView("Setup")

	cmd := r.key("i")
	if cmd == nil {
		t.Fatal("no exec cmd")
	}

	if got := r.cmds.calls[0]; !slices.Equal(got, []string{"install", "--admin-uid", "1000"}) {
		t.Errorf("args = %v", got)
	}

	cmd = r.key("x")
	if cmd == nil {
		t.Fatal("no exec cmd")
	}

	if got := r.cmds.calls[1]; !slices.Equal(got, []string{"install", "--admin-uid", "1000", "--disable-input-remapper"}) {
		t.Errorf("args = %v", got)
	}

	cmd = r.key("u")
	if cmd == nil || !slices.Equal(r.cmds.calls[2], []string{"uninstall"}) {
		t.Errorf("uninstall calls = %v", r.cmds.calls)
	}
}

func TestSetupEscAndQuit(t *testing.T) {
	t.Parallel()

	r := newRig(t, adminStatus())
	r.loaded()
	r.key("s")
	r.key("esc")
	r.wantView("Devices")

	r.key("s")

	if cmd := r.key("q"); cmd == nil {
		t.Fatal("no quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("not a quit")
	}
}
