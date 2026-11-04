package dagql

import (
	"context"
	"slices"
	"sync"
)

// callScopesKey carries the CallScopes recording the calls a resolver makes.
type callScopesKey struct{}

// CallScopes records whether any call made directly under a context would
// give a later session a different result: one cached per call, session or
// client, or only for a time. A resolver whose result stands for such calls
// declines to persist it, so the next session makes them again.
//
// Only direct calls count. A cached call's result stands for the calls its
// own resolver makes. An uncached call runs again each time, so the calls it
// makes are recorded as direct ones, as DeclinePersistence within it applies
// to its caller.
type CallScopes struct {
	mu    sync.Mutex
	bound string
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

func (s *CallScopes) record(req *CallRequest) {
	if req == nil || req.ResultCall == nil || req.DoNotCache {
		return
	}
	if req.TTL <= 0 && !sessionBoundInputs(req.ImplicitInputs) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound == "" {
		s.bound = req.Field
	}
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

func recordCallScope(ctx context.Context, req *CallRequest) {
	if scopes, _ := ctx.Value(callScopesKey{}).(*CallScopes); scopes != nil {
		scopes.record(req)
	}
}

// withoutCallScopes ends recording for a cached call's resolver: its result
// stands for the calls it makes.
func withoutCallScopes(ctx context.Context) context.Context {
	if ctx.Value(callScopesKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, callScopesKey{}, (*CallScopes)(nil))
}
