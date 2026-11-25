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

// handOff makes outer, a call keyed per client, under ctx. Its resolver makes
// inner, a call keyed by its argument alone, with arg as that argument, and
// returns inner's result, holding resource as a session resource unless it is
// empty. declared says whether outer is keyed per client only to borrow
// (FieldSpec.PerClientOnlyToBorrow).
func (s *callScopesTest) handOff(ctx context.Context, outer string, declared bool, arg, resource string) AnyResult {
	s.t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: outer, Type: NewResultCallType(String("").Type())}
	client, err := resultCallArgFromInput(ctx, PerClientInput.Name, NewString("client-1"), false)
	require.NoError(s.t, err)
	frame.ImplicitInputs = []*ResultCallArg{client}
	req := &CallRequest{ResultCall: frame, IsPersistable: true, PerClientOnlyToBorrow: declared}
	res, err := s.c.GetOrInitCall(ctx, s.session, s.srv, req, func(ctx context.Context) (AnyResult, error) {
		innerFrame := &ResultCall{Kind: ResultCallKindField, Field: outer + "-inner", Type: NewResultCallType(String("").Type())}
		input, err := resultCallArgFromInput(ctx, "input", NewString(arg), false)
		if err != nil {
			return nil, err
		}
		innerFrame.Args = []*ResultCallArg{input}
		return s.c.GetOrInitCall(ctx, s.session, s.srv, &CallRequest{ResultCall: innerFrame, IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			res, err := NewResultForCall(String(arg), innerFrame)
			if err != nil || resource == "" {
				return res, err
			}
			return res.WithSessionResourceHandle(ctx, SessionResourceHandle(resource))
		})
	})
	require.NoError(s.t, err)
	return res
}

// A call keyed per client only to turn what its client lends it into
// explicit inputs of the call it hands over to, which names each as a session
// resource, doesn't keep the resolver that made it to its session: another
// session making the call gets the same result, or one that names its own
// resources, and the cache serves either only to a session holding them.
func TestCallScopesDontBindACallKeyedPerClientOnlyToBorrow(t *testing.T) {
	s := newCallScopesTest(t)
	ctx, scopes := WithCallScopes(s.ctx)
	s.handOff(ctx, "borrowing", true, "the client's credential", "lent-credential")
	require.Empty(t, scopes.SessionBound(), "a call keyed per client only to borrow doesn't bind its caller")
}

// A call keyed per client that hands over to a call keyed by its arguments
// alone, with arguments it chose from what its client holds, still keeps the
// resolver that made it to its session, unless it declares that what it took
// is only what it borrowed: another session's client may hold another value,
// and the result it hands over names nothing that tells them apart.
func TestCallScopesBindACallKeyedPerClientThatHandsOverWhatItRead(t *testing.T) {
	s := newCallScopesTest(t)
	ctx, scopes := WithCallScopes(s.ctx)
	s.handOff(ctx, "reading", false, "a value from the client's files", "")
	require.Equal(t, "reading", scopes.SessionBound(), "a call that hands over what it read from its client binds its caller")
}

// A resolver that reads its session's state other than through a cached
// call says so, and its call scopes count the read. Outside call scopes the
// note does nothing.
func TestCallScopesRecordASessionRead(t *testing.T) {
	s := newCallScopesTest(t)
	ctx, scopes := WithCallScopes(s.ctx)
	RecordSessionRead(ctx, "the session's lock")
	require.Equal(t, "the session's lock", scopes.SessionBound())
	RecordSessionRead(s.ctx, "outside call scopes")
}
