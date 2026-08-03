package tui

import (
	"time"
)

// ToastKind describes how a toast should be dismissed.
type ToastKind int

const (
	// ToastOK: data-change toasts auto-dismiss after 2s.
	ToastOK ToastKind = iota
	// ToastErr: error toasts auto-dismiss after 5s.
	ToastErr
	// ToastAsync: async events dismiss on any keypress (or after a long period).
	ToastAsync
)

// MaxToastStack is the maximum number of toasts rendered at once.
const MaxToastStack = 3

// Toast is a single notification with a message, kind, and dismissal deadline.
type Toast struct {
	Message   string
	Kind      ToastKind
	CreatedAt time.Time
}

// dismissAfter returns how long a toast of this kind lives before auto-dismiss.
func (t Toast) dismissAfter() time.Duration {
	switch t.Kind {
	case ToastOK:
		return 2 * time.Second
	case ToastErr:
		return 5 * time.Second
	default:
		return 0 // async toasts never auto-dismiss; they go on keypress
	}
}

// Notifier is the toast queue. It is a simple ordered ring that keeps at most
// MaxToastStack visible toasts, dropping the oldest when full.
type Notifier struct {
	toasts []Toast
	now    func() time.Time
}

// NewNotifier builds an empty notifier. A clock is injected for testability.
func NewNotifier(now func() time.Time) *Notifier {
	if now == nil {
		now = time.Now
	}
	return &Notifier{now: now}
}

// Push adds a toast, dropping the oldest when the stack exceeds MaxToastStack.
// It returns true if a toast was dropped.
func (n *Notifier) Push(message string, kind ToastKind) bool {
	dropped := len(n.toasts) >= MaxToastStack
	n.toasts = append(n.toasts, Toast{Message: message, Kind: kind, CreatedAt: n.now()})
	if dropped {
		n.toasts = n.toasts[1:]
	}
	return dropped
}

// Dismiss removes the most recent toast (used on keypress for async toasts).
func (n *Notifier) Dismiss() {
	if len(n.toasts) > 0 {
		n.toasts = n.toasts[:len(n.toasts)-1]
	}
}

// DismissExpired removes toasts whose auto-dismiss deadline has passed.
func (n *Notifier) DismissExpired() {
	now := n.now()
	kept := n.toasts[:0]
	for _, t := range n.toasts {
		if t.dismissAfter() > 0 && now.Sub(t.CreatedAt) >= t.dismissAfter() {
			continue
		}
		kept = append(kept, t)
	}
	n.toasts = kept
}

// All returns the current visible toasts, oldest first.
func (n *Notifier) All() []Toast {
	return n.toasts
}

// Len returns the current number of visible toasts.
func (n *Notifier) Len() int {
	return len(n.toasts)
}
