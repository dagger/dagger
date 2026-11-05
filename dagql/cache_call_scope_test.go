package dagql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// callScopesTest makes calls the way a resolver's own calls reach the cache.
type callScopesTest struct {
	t       *testing.T
	ctx     context.Context
	c       *Cache
	srv     *Server
	session string
}

func newCallScopesTest(t *testing.T) *callScopesTest {
	ctx, c, srv := persistedListTestCache(t, filepath.Join(t.TempDir(), "cache.db"))
	return &callScopesTest{t: t, ctx: currentEntryTestSession(ctx, c, "session-1"), c: c, srv: srv, session: "session-1"}
}

// call makes field's call under ctx. A session-bound call is keyed per
// session; a cached call with ttl keeps its result that many seconds.
func (s *callScopesTest) call(ctx context.Context, field string, sessionBound, doNotCache bool, ttl int64, fn func(context.Context) (AnyResult, error)) AnyResult {
	s.t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(String("").Type())}
	if sessionBound {
		arg, err := resultCallArgFromInput(ctx, PerSessionInput.Name, NewString(s.session), false)
		require.NoError(s.t, err)
		frame.ImplicitInputs = []*ResultCallArg{arg}
	}
	if fn == nil {
		fn = func(context.Context) (AnyResult, error) { return NewResultForCall(String(field), frame) }
	}
	res, err := s.c.GetOrInitCall(ctx, s.session, s.srv, &CallRequest{ResultCall: frame, TTL: ttl, DoNotCache: doNotCache, IsPersistable: true}, fn)
	require.NoError(s.t, err)
	return res
}

func (s *callScopesTest) expiresAt(res AnyResult) int64 {
	s.c.egraphMu.RLock()
	defer s.c.egraphMu.RUnlock()
	return res.cacheSharedResult().expiresAtUnix
}

func TestCallScopesRecordWhatALaterSessionWouldCallAgain(t *testing.T) {
	s := newCallScopesTest(t)

	ctx, scopes := WithCallScopes(s.ctx)
	s.call(ctx, "kept", false, false, 0, nil)
	require.Empty(t, scopes.SessionBound(), "a call cached across sessions doesn't bind its caller")
	require.Zero(t, scopes.ExpiresAtUnix())

	ctx, scopes = WithCallScopes(s.ctx)
	s.call(ctx, "per-session", true, false, 0, nil)
	require.Equal(t, "per-session", scopes.SessionBound())

	// An uncached call runs again each time, so the calls it makes count
	// as the caller's own.
	ctx, scopes = WithCallScopes(s.ctx)
	s.call(ctx, "routing", false, true, 0, func(ctx context.Context) (AnyResult, error) {
		return s.call(ctx, "routed-per-session", true, false, 0, nil), nil
	})
	require.Equal(t, "routed-per-session", scopes.SessionBound())

	// A cached call's result stands for the calls its resolver makes.
	ctx, scopes = WithCallScopes(s.ctx)
	s.call(ctx, "wrapper", false, false, 0, func(ctx context.Context) (AnyResult, error) {
		return s.call(ctx, "wrapped-per-session", true, false, 0, nil), nil
	})
	require.Empty(t, scopes.SessionBound())
}

// A call kept for a time binds its caller to the expiry of its result, which
// a cache hit doesn't extend, and ExpireBy gives the caller's result that
// expiry.
func TestCallScopesLimitTheCallersExpiry(t *testing.T) {
	s := newCallScopesTest(t)
	first := s.call(s.ctx, "timed", false, false, 3600, nil)
	expiry := s.expiresAt(first)
	require.NotZero(t, expiry)

	time.Sleep(1100 * time.Millisecond)
	var hit AnyResult
	outer := s.call(s.ctx, "standing-for-timed", false, false, 0, func(ctx context.Context) (AnyResult, error) {
		scoped, scopes := WithCallScopes(ctx)
		hit = s.call(scoped, "timed", false, false, 3600, nil)
		require.Equal(t, expiry, scopes.ExpiresAtUnix(), "the hit keeps its result's expiry")
		require.Empty(t, scopes.SessionBound(), "a time limit doesn't bind the caller to its session")
		ExpireBy(ctx, scopes.ExpiresAtUnix())
		return NewResultForCall(String("outer"), CurrentCall(ctx))
	})
	require.True(t, hit.HitCache())
	require.Equal(t, expiry, s.expiresAt(outer), "the caller's result expires with the result it stands for")

	// Without ExpireBy, a result without a TTL doesn't expire.
	plain := s.call(s.ctx, "standing-for-nothing", false, false, 0, nil)
	require.Zero(t, s.expiresAt(plain))
}

// An implicit input declared session-scoped binds its call to the session
// whenever it has a value, as a tag lookup's scope does; empty, as for a
// digest, it doesn't.
func TestCallScopesRecordDeclaredSessionScopedInputs(t *testing.T) {
	s := newCallScopesTest(t)
	const name = "test-session-scope"
	SessionScopedInput(name)
	scoped := func(field, value string) string {
		ctx, scopes := WithCallScopes(s.ctx)
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(String("").Type())}
		arg, err := resultCallArgFromInput(ctx, name, NewString(value), false)
		require.NoError(t, err)
		frame.ImplicitInputs = []*ResultCallArg{arg}
		_, err = s.c.GetOrInitCall(ctx, s.session, s.srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(String(field), frame)
		})
		require.NoError(t, err)
		return scopes.SessionBound()
	}
	require.Equal(t, "session-scoped", scoped("session-scoped", "session-1"))
	require.Empty(t, scoped("unscoped", ""))
}
