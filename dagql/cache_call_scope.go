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

// SessionBound names the first recorded call that a later session would
// make again, as Type.field when its receiver type is known, or returns ""
// when there was none.
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
		if req.ReceiverTypeName != "" {
			s.bound = req.ReceiverTypeName + "." + req.Field
		}
	}
}

func (s *CallScopes) recordExpiry(expiresAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expiresAt = mergeSharedResultExpiryUnix(s.expiresAt, expiresAt)
}

// SessionBound reports whether the call is keyed per call, session or
// client, so no later session makes it again.
func (frame *ResultCall) SessionBound() bool {
	return frame != nil && sessionBoundInputs(frame.ImplicitInputs)
}

// onlyItsSessionMatches reports whether no other session, on this engine or
// another, can ever match the call: it is keyed to its session, one of its
// clients or itself (SessionBound), which names that key in its recipe and
// in its term, and it has no extra digest, such as a content digest, by
// which another session's call can find its result. An engine asks the
// remote cache service about no miss of such a call (noteRemoteMiss), and
// the Cloud keeps no such call on the entry it returned (decodeKeptCall).
//
// Only the call's own key counts. A call that isn't keyed to its client,
// made on a receiver that is, can still be matched by its term on another
// engine whose own receiver has the same content
// (TestCallRecordHitsOverAnEquivalentReceiver): it isn't one of these.
func (frame *ResultCall) onlyItsSessionMatches() bool {
	if !frame.SessionBound() {
		return false
	}
	for _, extra := range frame.ExtraDigests {
		if extra.Digest != "" {
			return false
		}
	}
	return true
}

// sessionScopedInputs names the implicit inputs, besides the per-call,
// per-session and per-client ones, that key a call to its session whenever
// they have a value (see SessionScopedInput).
var sessionScopedInputs sync.Map

// SessionScopedInput declares that the implicit input named name keys a call
// to its session, or to one of its clients or calls, whenever its value isn't
// empty: a later session gives the call another key. A call with such an
// input is one CallScopes reports as session-bound.
func SessionScopedInput(name string) {
	sessionScopedInputs.Store(name, struct{}{})
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
		if _, scoped := sessionScopedInputs.Load(input.Name); scoped {
			return input.Value == nil || input.Value.Kind != ResultCallLiteralKindString || input.Value.StringValue != ""
		}
		return false
	})
}

func callScopesFrom(ctx context.Context) *CallScopes {
	scopes, _ := ctx.Value(callScopesKey{}).(*CallScopes)
	return scopes
}

// recordCallResultExpiry records when a recorded call's result expires.
func (c *Cache) recordCallResultExpiry(ctx context.Context, scopes *CallScopes, req *CallRequest, res AnyResult) {
	if scopes == nil || req == nil || req.DoNotCache || res == nil {
		return
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return
	}
	c.readLockGraphGathered(ctx)
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
