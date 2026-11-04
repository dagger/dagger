package dagql

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// callScopesKey carries the CallScopes recording the calls a resolver makes.
type callScopesKey struct{}

// CallScopes records how long the results of the calls made directly under a
// context stay reusable. A resolver whose result stands for those calls
// keeps it no longer than they are: not past its session when one is keyed
// per call, session or client, so a later session makes it again, and not
// past the earliest expiry of their results.
//
// Only direct calls count. A cached call's result stands for the calls its
// own resolver makes. An uncached call runs again each time, so the calls it
// makes are recorded as direct ones, as DeclinePersistence within it applies
// to its caller.
type CallScopes struct {
	mu        sync.Mutex
	bound     string
	expiresAt int64
}

// WithCallScopes returns a context that records the scopes of the calls made
// directly under it.
func WithCallScopes(ctx context.Context) (context.Context, *CallScopes) {
	scopes := &CallScopes{}
	return context.WithValue(ctx, callScopesKey{}, scopes), scopes
}

// SessionBound returns the field of the first recorded call that a later
// session would make again, or "" when there was none.
func (s *CallScopes) SessionBound() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bound
}

// ExpiresAtUnix returns the earliest expiry of the recorded calls' results,
// or 0 when none expires.
func (s *CallScopes) ExpiresAtUnix() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiresAt
}

func (s *CallScopes) record(req *CallRequest) {
	if req == nil || req.ResultCall == nil || req.DoNotCache || !sessionBoundInputs(req.ImplicitInputs) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound == "" {
		s.bound = req.Field
	}
}

func (s *CallScopes) recordExpiry(expiresAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expiresAt = mergeSharedResultExpiryUnix(s.expiresAt, expiresAt)
}

// SessionBound reports whether the call is keyed per call, session or
// client, so no later session makes it again.
func (call *ResultCall) SessionBound() bool {
	return call != nil && sessionBoundInputs(call.ImplicitInputs)
}

func sessionBoundInputs(inputs []*ResultCallArg) bool {
	return slices.ContainsFunc(inputs, func(input *ResultCallArg) bool {
		if input == nil {
			return false
		}
		switch input.Name {
		case PerCallInput.Name, PerSessionInput.Name, PerClientInput.Name:
			return true
		}
		return false
	})
}

func callScopesFrom(ctx context.Context) *CallScopes {
	scopes, _ := ctx.Value(callScopesKey{}).(*CallScopes)
	return scopes
}

// recordCallResultExpiry records when a recorded call's result expires.
func (c *Cache) recordCallResultExpiry(scopes *CallScopes, req *CallRequest, res AnyResult) {
	if scopes == nil || req == nil || req.DoNotCache || res == nil {
		return
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return
	}
	c.egraphMu.RLock()
	expiresAt := shared.expiresAtUnix
	c.egraphMu.RUnlock()
	scopes.recordExpiry(expiresAt)
}

// withoutCallScopes ends recording for a cached call's resolver: its result
// stands for the calls it makes.
func withoutCallScopes(ctx context.Context) context.Context {
	if ctx.Value(callScopesKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, callScopesKey{}, (*CallScopes)(nil))
}

// expiryLimitKey carries the expiryLimit of the call whose resolver runs
// under a context.
type expiryLimitKey struct{}

// expiryLimit is the latest expiry a call's resolver allows its result, as a
// Unix time, or 0 for none.
type expiryLimit struct{ atUnix atomic.Int64 }

func (l *expiryLimit) load() int64 {
	if l == nil {
		return 0
	}
	return l.atUnix.Load()
}

// ExpireBy keeps the result of the call whose resolver runs under ctx from
// being reused after expiresAtUnix, as a TTL would, when the result stands
// for others that expire then. Zero sets no limit. Like DeclinePersistence,
// it must be called with the resolver's own context, and does nothing
// outside a resolver the cache runs.
func ExpireBy(ctx context.Context, expiresAtUnix int64) {
	l, _ := ctx.Value(expiryLimitKey{}).(*expiryLimit)
	if l == nil || expiresAtUnix == 0 {
		return
	}
	for {
		cur := l.atUnix.Load()
		next := mergeSharedResultExpiryUnix(cur, expiresAtUnix)
		if next == cur || l.atUnix.CompareAndSwap(cur, next) {
			return
		}
	}
}
