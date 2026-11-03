package fix

import (
	"context"
	"errors"
	"sync"
)

// ErrNotConfigured is returned by every package helper until the matching
// Set* hook has registered an implementation.
var ErrNotConfigured = errors.New("fix: not configured")

var (
	mu        sync.RWMutex
	resolver  Resolver
	session   Session
	explainer Explainer
	retrier   Retrier
	triager   Triager
)

// SetResolver registers the Resolver used by InstalledTools and ResolveChoice.
func SetResolver(r Resolver) {
	mu.Lock()
	defer mu.Unlock()
	resolver = r
}

// SetSession registers the Session used by OpenSession.
func SetSession(s Session) {
	mu.Lock()
	defer mu.Unlock()
	session = s
}

// SetExplainer registers the Explainer used by Explain.
func SetExplainer(e Explainer) {
	mu.Lock()
	defer mu.Unlock()
	explainer = e
}

// SetRetrier registers the Retrier used by Retry.
func SetRetrier(r Retrier) {
	mu.Lock()
	defer mu.Unlock()
	retrier = r
}

// SetTriager registers the Triager used by Diagnose.
func SetTriager(t Triager) {
	mu.Lock()
	defer mu.Unlock()
	triager = t
}

// InstalledTools delegates to the registered Resolver.
func InstalledTools(ctx context.Context) ([]Tool, error) {
	mu.RLock()
	r := resolver
	mu.RUnlock()
	if r == nil {
		return nil, ErrNotConfigured
	}
	return r.InstalledTools(ctx)
}

// ResolveChoice delegates to the registered Resolver.
func ResolveChoice(ctx context.Context, req Request) (Choice, error) {
	mu.RLock()
	r := resolver
	mu.RUnlock()
	if r == nil {
		return Choice{}, ErrNotConfigured
	}
	return r.ResolveChoice(ctx, req)
}

// OpenSession delegates to the registered Session.
func OpenSession(ctx context.Context, req Request) error {
	mu.RLock()
	s := session
	mu.RUnlock()
	if s == nil {
		return ErrNotConfigured
	}
	return s.OpenSession(ctx, req)
}

// Explain delegates to the registered Explainer.
func Explain(ctx context.Context, req Request) (Explanation, error) {
	mu.RLock()
	e := explainer
	mu.RUnlock()
	if e == nil {
		return Explanation{}, ErrNotConfigured
	}
	return e.Explain(ctx, req)
}

// Retry delegates to the registered Retrier.
func Retry(ctx context.Context, req Request) error {
	mu.RLock()
	r := retrier
	mu.RUnlock()
	if r == nil {
		return ErrNotConfigured
	}
	return r.Retry(ctx, req)
}

// Diagnose delegates to the registered Triager.
func Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	mu.RLock()
	t := triager
	mu.RUnlock()
	if t == nil {
		return Diagnosis{}, ErrNotConfigured
	}
	return t.Diagnose(ctx, req)
}
