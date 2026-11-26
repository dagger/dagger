package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A bundle whose root the recipient holds sends it as a held row with the
// calls the Cloud keeps on it, and an engine that restores it learns the call
// for the entry it already holds: a call the Cloud kept after the engine
// merged the entry, which the engine's copy never carried.
func TestAHeldRootTeachesItsKeptCall(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	now := time.Now().Unix()

	// The engine restores the entry before the Cloud keeps the call.
	first, err := cloud.StoredBundle(ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	require.Empty(t, first.Bundle.Calls)
	engineCtx, engine, srv := transferTestCache(t)
	reply, err := engine.RestoreValues(engineCtx, first.Bundle, nil, "first")
	require.NoError(t, err)
	require.NoError(t, reply.Release(engineCtx))

	test.attach(t, ctx, test.span(t, true, 0))
	again, err := cloud.StoredBundle(ctx, []uint64{test.number}, now, &HeldBy{Cache: "engine", Also: func(n uint64) bool { return n == test.number }})
	require.NoError(t, err)
	require.Len(t, again.Bundle.Values, 1)
	require.NotEmpty(t, again.Bundle.Values[0].Held, "the root goes as a held row")
	require.Len(t, again.Bundle.Roots, 1)
	require.Equal(t, again.Bundle.Values[0].Ordinal, again.Bundle.Roots[0].Ordinal)
	require.Len(t, again.Bundle.Calls, 1, "with the call kept on it")
	require.Equal(t, again.Bundle.Values[0].Ordinal, again.Bundle.Calls[0].Value)

	reply, err = engine.RestoreValues(engineCtx, again.Bundle, []TransferOrdinal{again.Bundle.Roots[0].Ordinal}, "again")
	require.NoError(t, err)
	require.Empty(t, reply.Stale)
	require.NoError(t, reply.Release(engineCtx))

	ran := false
	_, err = engine.GetOrInitCall(engineCtx, "test-session", srv,
		&CallRequest{ResultCall: test.frame.clone(), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran again"}, test.frame)
		})
	require.NoError(t, err)
	require.False(t, ran, "the call the held root carried hits")
}

// A held root's retention reads as any root's: left unretained it takes no
// retention edge, so an entry the engine holds only below another root stays
// held only below it; otherwise it takes one. An edge it has already stays
// either way.
func TestAHeldRootIsRetainedOnlyIfNotLeftUnretained(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	now := time.Now().Unix()
	engineCtx, engine, _ := transferTestCache(t)
	first, err := cloud.StoredBundle(ctx, []uint64{r}, now, nil)
	require.NoError(t, err)
	reply, err := engine.RestoreValues(engineCtx, first.Bundle, nil, "first")
	require.NoError(t, err)
	require.NoError(t, reply.Release(engineCtx))
	root, dep := heldRootTestEntry(t, engine, first.Bundle, reply, r), heldRootTestEntry(t, engine, first.Bundle, reply, p)
	require.True(t, heldRootTestRetained(engine, root))
	require.False(t, heldRootTestRetained(engine, dep), "the dependency is held below the root")

	held := &HeldBy{Cache: "engine", Also: func(n uint64) bool { return n == r || n == p }}
	restore := func(roots []uint64, leave bool) {
		t.Helper()
		b, err := cloud.StoredBundle(ctx, roots, now, held)
		require.NoError(t, err)
		for _, v := range b.Bundle.Values {
			require.NotEmpty(t, v.Held)
		}
		var unretained []TransferOrdinal
		if leave {
			for _, root := range b.Bundle.Roots {
				unretained = append(unretained, root.Ordinal)
			}
		}
		reply, err := engine.RestoreValues(engineCtx, b.Bundle, unretained, "held")
		require.NoError(t, err)
		require.Empty(t, reply.Stale)
		require.NoError(t, reply.Release(engineCtx))
	}

	restore([]uint64{p}, true)
	require.False(t, heldRootTestRetained(engine, dep), "left unretained, the held root takes no edge")
	require.True(t, heldRootTestLive(engine, dep), "and stays held below the root")
	restore([]uint64{r}, true)
	require.True(t, heldRootTestRetained(engine, root), "an edge it has stays")
	restore([]uint64{p}, false)
	require.True(t, heldRootTestRetained(engine, dep), "not left unretained, it takes one")
}

// heldRootTestEntry is the engine's entry a restore landed the Cloud's entry
// number on.
func heldRootTestEntry(t *testing.T, engine *Cache, bundle ValueBundle, reply MergeReply, number uint64) *sharedResult {
	t.Helper()
	for _, sent := range bundle.Values {
		if sent.SenderNumber != number {
			continue
		}
		for _, v := range reply.Values {
			if v.Ordinal != sent.Ordinal {
				continue
			}
			engine.egraphMu.RLock()
			res := engine.resultsByID[sharedResultID(v.Number)]
			engine.egraphMu.RUnlock()
			require.NotNil(t, res)
			return res
		}
	}
	t.Fatalf("entry %d didn't land", number)
	return nil
}

// heldRootTestLive reports whether the engine still has res.
func heldRootTestLive(engine *Cache, res *sharedResult) bool {
	engine.egraphMu.RLock()
	defer engine.egraphMu.RUnlock()
	return engine.resultsByID[res.id] == res
}

// heldRootTestRetained reports whether the engine has a retention edge on res.
func heldRootTestRetained(engine *Cache, res *sharedResult) bool {
	engine.egraphMu.RLock()
	defer engine.egraphMu.RUnlock()
	_, ok := engine.persistedEdgesByResult[res.id]
	return ok
}
