package dagql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
)

// A session-scoped call, such as Container.from on a tag, has a new recipe in
// every session, and returns the result of a call every session shares, such
// as Container.from on the digest the tag resolves to. On a long-lived engine
// that shared result gathers an identity for every session that ever reached
// it, so a session's calls must cost the same however many sessions came
// before.
func TestCacheSessionScopedCallCostDoesNotGrowWithEarlierSessions(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)

	contentDigest := digest.FromString("session-scoped-content")
	pinned := cacheTestIntCall("from-pinned")
	var sharedID sharedResultID
	sessions := 0
	// runSessions runs n sessions that each call their own recipe twice, a
	// publication and a hit, then end, and returns how long they took.
	runSessions := func(n int) time.Duration {
		start := time.Now()
		for range n {
			sessions++
			sessionID := fmt.Sprintf("session-%d", sessions)
			ctx := ContextWithCache(engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
				ClientID:  sessionID,
				SessionID: sessionID,
			}), c)
			req := cacheTestIntCall("from-" + sessionID)
			for range 2 {
				res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{
					ResultCall:    req,
					IsPersistable: true,
				}, func(ctx context.Context) (AnyResult, error) {
					return c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{
						ResultCall:    pinned,
						IsPersistable: true,
					}, func(ctx context.Context) (AnyResult, error) {
						return cacheTestIntResult(pinned, 1).(Result[Int]).WithContentDigest(ctx, contentDigest)
					})
				})
				assert.NilError(t, err)
				if sharedID == 0 {
					sharedID = res.cacheSharedResult().id
				}
				assert.Equal(t, sharedID, res.cacheSharedResult().id, "every session must share the one result")
			}
			assert.NilError(t, c.ReleaseSession(ctx, sessionID))
		}
		return time.Since(start)
	}
	// Each sample is the quickest of three batches, to keep a busy machine's
	// noise out of the comparison.
	sample := func() time.Duration {
		best := runSessions(100)
		for range 2 {
			best = min(best, runSessions(100))
		}
		return best
	}

	early := sample()
	runSessions(10_000)
	history := sessions
	late := sample()

	c.egraphMu.RLock()
	terms := len(c.resultTerms[sharedID])
	c.egraphMu.RUnlock()
	assert.Assert(t, terms > 10_000, "the shared result should hold every session's identity, has %d", terms)
	t.Logf("100 sessions took %s on a new cache and %s after %d earlier sessions", early, late, history)
	assert.Assert(t, late < 3*early,
		"100 sessions took %s after %d earlier sessions, against %s on a new cache", late, history, early)
}
