package tui

import (
	"testing"
	"time"
)

func TestNotifierPushOrder(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	n := NewNotifier(func() time.Time { return now })

	n.Push("one", ToastOK)
	n.Push("two", ToastOK)
	n.Push("three", ToastOK)

	all := n.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 toasts, got %d", len(all))
	}
	if all[0].Message != "one" || all[2].Message != "three" {
		t.Errorf("push order not preserved: %v", all)
	}
}

func TestNotifierMaxStackDropsOldest(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	n := NewNotifier(func() time.Time { return now })

	n.Push("one", ToastOK)
	n.Push("two", ToastOK)
	n.Push("three", ToastOK)
	dropped := n.Push("four", ToastOK)

	if !dropped {
		t.Error("expected a drop when exceeding max-3")
	}
	all := n.All()
	if len(all) != 3 {
		t.Fatalf("expected stack capped at 3, got %d", len(all))
	}
	if all[0].Message != "two" {
		t.Errorf("expected oldest (one) dropped, first is %q", all[0].Message)
	}
	if all[2].Message != "four" {
		t.Errorf("expected newest (four) at the end, got %q", all[2].Message)
	}
}

func TestNotifierAutoDismissTimings(t *testing.T) {
	start := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	n := NewNotifier(func() time.Time { return start.Add(elapsed) })

	n.Push("ok", ToastOK)     // 2s
	n.Push("err", ToastErr)   // 5s
	n.Push("async", ToastAsync) // never auto-dismiss

	// After 2s the OK toast should expire.
	elapsed = 2 * time.Second
	n.DismissExpired()
	if n.Len() != 2 {
		t.Errorf("expected OK toast to expire at 2s, got %d toasts", n.Len())
	}
	// After 5s the error toast should expire, async remains.
	elapsed = 5 * time.Second
	n.DismissExpired()
	all := n.All()
	if len(all) != 1 || all[0].Message != "async" {
		t.Errorf("expected only async toast to remain, got %v", all)
	}

	// Async toast is dismissed on keypress via Dismiss().
	n.Dismiss()
	if n.Len() != 0 {
		t.Errorf("expected async toast to dismiss on keypress, got %d", n.Len())
	}
}
