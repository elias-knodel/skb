package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elias-knodel/skb/internal/tui"
	"github.com/elias-knodel/skb/internal/update"
)

func availableResult() update.Result {
	return update.Result{
		Current:    "v1.1.0",
		Latest:     "v1.2.0",
		URL:        "https://github.com/elias-knodel/skb/releases/tag/v1.2.0",
		Comparable: true,
		Available:  true,
	}
}

func updateRig(t *testing.T, res update.Result, err error, calls *atomic.Int32) *rig {
	t.Helper()

	return newRigWith(t, adminStatus(), func(c *tui.Config) {
		c.CheckUpdate = func(context.Context) (update.Result, error) {
			calls.Add(1)

			return res, err
		}
	})
}

func TestUpdateNoticeShown(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	r := updateRig(t, availableResult(), nil, &calls)
	r.loaded()
	r.wantView("update available: v1.2.0", "releases/tag/v1.2.0")

	r.key("s")
	r.wantView("update available: v1.2.0")
}

func TestUpdateNoticeHiddenWhenCurrent(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	res := availableResult()
	res.Available = false

	r := updateRig(t, res, nil, &calls)
	r.loaded()
	r.wantNoView("update available")
}

func TestUpdateCheckErrorSilent(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	r := updateRig(t, update.Result{}, errors.New("offline"), &calls)
	r.loaded()
	r.wantNoView("update available", "Error", "offline")
}

func TestUpdateCheckNotBlockingInit(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	r := updateRig(t, availableResult(), nil, &calls)

	cmd := r.m.Init()
	if calls.Load() != 0 {
		t.Fatalf("calls after Init = %d", calls.Load())
	}

	r.settle(cmd)

	if calls.Load() != 1 {
		t.Fatalf("calls after settle = %d", calls.Load())
	}

	r.settle(r.update(r.fire(time.Hour)))
	r.settle(r.update(r.fire(time.Hour)))

	if calls.Load() != 1 {
		t.Errorf("calls after polls = %d", calls.Load())
	}
}

func TestUpdateNoticeSmallHeight(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	r := updateRig(t, availableResult(), nil, &calls)
	r.loaded()
	r.update(tea.WindowSizeMsg{Width: 100, Height: 3})

	v := r.view()
	if n := len(strings.Split(v, "\n")); n > 3 {
		t.Errorf("view has %d lines:\n%s", n, v)
	}

	r.wantView(" skb ")
}
