package dagql

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// remoteWriteOps are the operations a RemoteWrites applies in its hold, as
// the Cache applies them in holds of their own.
type remoteWriteOps interface {
	AttachRemoteHolding(context.Context, HolderKey, RemoteHolding) (RemoteChange, error)
	UpdateRemoteHolding(context.Context, HolderKey, RemoteHoldingUpdate) (RemoteChange, bool, error)
	AddRemoteHold(context.Context, HolderKey, string) (RemoteChange, error)
	ObserveRemoteRetention(context.Context, HolderKey, RetentionObservation) (RemoteChange, error)
	ApplyMergedReply(context.Context, CacheID, ValueBundle, MergeReply) (RemoteChange, error)
	ReleaseRemoteSession(context.Context, CacheID, string) RemoteChange
	ReleaseRemoteCache(context.Context, CacheID) RemoteChange
	CollectRemoteHoldings(context.Context, []HolderKey) ([]HolderKey, RemoteChange, error)
	RemoteHoldingInfo(HolderKey) (RemoteHeldEntry, bool)
	HolderClosure(HolderKey) []HolderKey
	HolderGraph(CacheID, []uint64) map[uint64][]uint64
	EntryClassEntries(uint64) (EntryClass, bool)
}

var (
	_ remoteWriteOps = (*Cache)(nil)
	_ remoteWriteOps = (*RemoteWrites)(nil)
)

// remoteWritesScript runs a run of telemetry's operations on ops, and
// returns what each answered, in order: holdings attached on their own
// entries and on one another's recipes, joined by content digest and by
// term, a dependency named before its holding, holds and their sessions'
// ends, retention set and dropped, merged replies landing and replacing,
// a replaced value's dropped dependency,
// a whole engine cache released, collections, refused operations, and the
// reads between them.
func remoteWritesScript(t *testing.T, ctx context.Context, ops remoteWriteOps) []string {
	t.Helper()
	var out []string
	note := func(name string, v ...any) {
		for i, x := range v {
			if change, ok := x.(RemoteChange); ok {
				v[i] = unorderedChange(change)
			}
		}
		out = append(out, fmt.Sprintf("%s: %+v", name, v))
	}
	attach := func(key HolderKey, desc RemoteHolding) {
		change, err := ops.AttachRemoteHolding(ctx, key, desc)
		note(fmt.Sprintf("attach %v", key), change, err)
	}
	a, b := CacheID("cache-a"), CacheID("cache-b")
	attach(HolderKey{a, 1}, holdingOf("writes-1"))
	attach(HolderKey{a, 2}, holdingOf("writes-2", 1, 3))
	attach(HolderKey{a, 3}, holdingOf("writes-3"))
	// Two holdings of one recipe.
	attach(HolderKey{b, 1}, holdingOf("writes-1"))
	// A content digest and a term that join classes.
	content := holdingOf("writes-4", 1)
	content.ContentDigest = testDigest("writes-content")
	attach(HolderKey{a, 4}, content)
	joined := holdingOf("writes-5")
	joined.ContentDigest = testDigest("writes-content")
	attach(HolderKey{b, 5}, joined)
	congruent := holdingOf("writes-6")
	congruent.Term = &RemoteTerm{Self: testDigest("writes-2-self")}
	attach(HolderKey{b, 6}, congruent)
	// Refused: no recipe, and a sender's cache.
	attach(HolderKey{a, 7}, RemoteHolding{})
	attach(HolderKey{Cache: "cloud", Number: 1}, holdingOf("writes-7"))

	update := func(key HolderKey, u RemoteHoldingUpdate) {
		change, found, err := ops.UpdateRemoteHolding(ctx, key, u)
		note(fmt.Sprintf("update %v", key), change, found, err)
	}
	update(HolderKey{a, 3}, RemoteHoldingUpdate{ContentDigest: testDigest("writes-content"), Parts: []PersistedPartAddress{{Part: "snapshot"}}})
	update(HolderKey{a, 9}, RemoteHoldingUpdate{Deps: []uint64{1}})
	// A replaced value drops a dependency.
	update(HolderKey{a, 2}, RemoteHoldingUpdate{Replacements: 1, Deps: []uint64{1}})

	hold := func(key HolderKey, session string) {
		change, err := ops.AddRemoteHold(ctx, key, session)
		note(fmt.Sprintf("hold %v %s", key, session), change, err)
	}
	hold(HolderKey{a, 1}, "s1")
	hold(HolderKey{a, 2}, "s1")
	hold(HolderKey{a, 2}, "s1")
	hold(HolderKey{b, 5}, "s2")
	hold(HolderKey{a, 9}, "s1")
	hold(HolderKey{a, 1}, "")

	retain := func(key HolderKey, obs RetentionObservation) {
		change, err := ops.ObserveRemoteRetention(ctx, key, obs)
		note(fmt.Sprintf("retain %v", key), change, err)
	}
	retain(HolderKey{a, 4}, RetentionObservation{Retained: true, Generation: 1, EngineTimeUnixNano: 10})
	retain(HolderKey{b, 6}, RetentionObservation{Retained: true, Generation: 1, EngineTimeUnixNano: 10})
	retain(HolderKey{b, 6}, RetentionObservation{Generation: 1, EngineTimeUnixNano: 20})
	retain(HolderKey{a, 9}, RetentionObservation{Retained: true})

	// Merged replies: the Cloud's entries of cache-a's holdings 1 and 2
	// land on new holdings of cache-c, 2 over 1, with a part, 2 retained;
	// then again at a higher count, which drops 2's dependency and its
	// retention; and two refused, onto a sender's cache and with an
	// ordinal that wasn't sent.
	c := CacheID("cache-c")
	merge := func(name string, cache CacheID, sent ValueBundle, reply MergeReply) {
		change, err := ops.ApplyMergedReply(ctx, cache, sent, reply)
		note(name, change, err)
	}
	e1, _ := ops.RemoteHoldingInfo(HolderKey{a, 1})
	e2, _ := ops.RemoteHoldingInfo(HolderKey{a, 2})
	sent := ValueBundle{Values: []TransferredValue{{Ordinal: 1, SenderNumber: e1.Number}, {Ordinal: 2, SenderNumber: e2.Number}}}
	merge("merge into cache-c", c, sent, MergeReply{Generation: 1, EngineTimeUnixNano: 30,
		Values: []MergedValue{{Ordinal: 1, Number: 1}, {Ordinal: 2, Number: 2, Deps: []uint64{1}, Parts: []PersistedPartAddress{{Part: "snapshot"}}}},
		Roots:  []MergedRoot{{Ordinal: 2, Number: 2, Retained: true}}})
	merge("merge into cache-c again", c, sent, MergeReply{Generation: 1, EngineTimeUnixNano: 40,
		Values: []MergedValue{{Ordinal: 2, Number: 2, Replacements: 1}},
		Roots:  []MergedRoot{{Ordinal: 2, Number: 2}}})
	merge("merge into a sender's cache", "cloud", sent, MergeReply{Values: []MergedValue{{Ordinal: 1, Number: 1}}})
	merge("merge of an ordinal not sent", c, sent, MergeReply{Values: []MergedValue{{Ordinal: 9, Number: 3}}})

	read := func(name string) {
		for _, key := range []HolderKey{{a, 1}, {a, 2}, {a, 3}, {a, 4}, {b, 1}, {b, 5}, {b, 6}, {c, 1}, {c, 2}} {
			held, ok := ops.RemoteHoldingInfo(key)
			note(fmt.Sprintf("%s info %v", name, key), held, ok)
			note(fmt.Sprintf("%s closure %v", name, key), ops.HolderClosure(key))
			if ok {
				class, ok := ops.EntryClassEntries(held.Number)
				note(fmt.Sprintf("%s class %v", name, key), class, ok)
			}
		}
		for _, cache := range []CacheID{a, b, c} {
			note(fmt.Sprintf("%s graph %v", name, cache), ops.HolderGraph(cache, []uint64{1, 2, 3, 4, 5, 6}))
		}
	}
	read("held")

	collect := func(candidates ...HolderKey) {
		collected, change, err := ops.CollectRemoteHoldings(ctx, candidates)
		note(fmt.Sprintf("collect %v", candidates), collected, change, err)
	}
	// Owned by a hold, retention or a dependent: nothing goes.
	collect(HolderKey{a, 1}, HolderKey{a, 4}, HolderKey{b, 5})
	// Owned by nothing: the dropped dependency and the dropped retention.
	collect(HolderKey{a, 3}, HolderKey{b, 6})
	change := ops.ReleaseRemoteSession(ctx, a, "s1")
	note("end s1", change)
	collect(unorderedChange(change).Candidates...)
	change = ops.ReleaseRemoteCache(ctx, b)
	note("release cache-b", change)
	collect(unorderedChange(change).Candidates...)
	read("released")
	return out
}

// unorderedChange is change with its Candidates and Recipes sorted: a
// change lists them in no order of its own, such as a session's holdings,
// which it releases in its map's order.
func unorderedChange(change RemoteChange) RemoteChange {
	change.recipes = nil
	change.Candidates = slices.Clone(change.Candidates)
	slices.SortFunc(change.Candidates, compareHolderKeys)
	change.Recipes = slices.Clone(change.Recipes)
	slices.Sort(change.Recipes)
	return change
}

// A run of operations in one hold answers, and leaves the cache, as the
// same operations do through the Cache one by one: each returns the same
// change, its marks of what it changed included, and every read after
// answers alike.
func TestRemoteWritesApplyAsTheCacheDoes(t *testing.T) {
	t.Parallel()
	ctx, each := storedPartTestCache(t, WithBlobStore())
	_, held := storedPartTestCache(t, WithBlobStore())

	want := remoteWritesScript(t, ctx, each)
	w := held.HoldRemoteWrites()
	got := remoteWritesScript(t, ctx, w)
	require.NoError(t, w.Release(ctx))
	require.Equal(t, want, got)

	// After the hold, the Cache reads the same of both.
	require.Equal(t, remoteWritesReads(each), remoteWritesReads(held))
}

// remoteWritesReads is what the script's holdings read through the Cache.
func remoteWritesReads(c *Cache) []string {
	var out []string
	for _, key := range []HolderKey{{"cache-a", 1}, {"cache-a", 2}, {"cache-a", 4}, {"cache-a", 9}} {
		held, ok := c.RemoteHoldingInfo(key)
		out = append(out, fmt.Sprintf("%v: %+v %v %v", key, held, ok, c.HolderClosure(key)))
	}
	return out
}

// A hold keeps the cache's readers out until its Release.
func TestRemoteWritesHoldTheCache(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	key := HolderKey{"cache-a", 1}
	_, err := c.AttachRemoteHolding(ctx, key, holdingOf("hold-1"))
	require.NoError(t, err)

	w := c.HoldRemoteWrites()
	read := make(chan bool)
	go func() {
		_, ok := c.RemoteHoldingInfo(key)
		read <- ok
	}()
	_, err = w.AddRemoteHold(ctx, key, "s1")
	require.NoError(t, err)
	select {
	case <-read:
		t.Fatal("a reader read the cache during a hold")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, w.Release(ctx))
	require.True(t, <-read)
	held, ok := c.RemoteHoldingInfo(key)
	require.True(t, ok)
	require.Equal(t, []string{"s1"}, held.Holding.Sessions)
}

// A merged reply applied in pieces (MergedReplyApply), the hold let go
// between them, leaves the cache as the reply applied whole does. At every
// gap between pieces, a reader sees only holdings the whole reply creates,
// those of the records applied so far, each in its final value state but
// for its dependencies on records still to come, which it names as unknown,
// its dependents, and its retention, observed with the last piece: what a
// half-applied reply shows is less than the engine holds, never other.
func TestRemoteWritesApplyAMergedReplyInPieces(t *testing.T) {
	t.Parallel()
	const records = 12
	a, c := CacheID("cache-a"), CacheID("cache-c")
	setup := func(t *testing.T, cache *Cache) (ValueBundle, MergeReply) {
		ctx := t.Context()
		sent := ValueBundle{}
		reply := MergeReply{Generation: 1, EngineTimeUnixNano: 10}
		for i := range uint64(records) {
			_, err := cache.AttachRemoteHolding(ctx, HolderKey{a, i + 1}, holdingOf(fmt.Sprintf("pieces-%d", i+1)))
			require.NoError(t, err)
			held, ok := cache.RemoteHoldingInfo(HolderKey{a, i + 1})
			require.True(t, ok)
			sent.Values = append(sent.Values, TransferredValue{Ordinal: TransferOrdinal(i + 1), SenderNumber: held.Number})
			// Each record over the one before it, and every third over the
			// one after it too, which a later piece applies.
			var deps []uint64
			if i > 0 {
				deps = append(deps, 100+i)
			}
			if i%3 == 0 && i+1 < records {
				deps = append(deps, 100+i+2)
			}
			reply.Values = append(reply.Values, MergedValue{Ordinal: TransferOrdinal(i + 1), Number: 100 + i + 1, Deps: deps,
				Parts: []PersistedPartAddress{{Part: "snapshot"}}})
		}
		reply.Roots = []MergedRoot{{Ordinal: records, Number: 100 + records, Retained: true}}
		return sent, reply
	}
	keys := make([]HolderKey, records)
	for i := range keys {
		keys[i] = HolderKey{c, 100 + uint64(i) + 1}
	}
	read := func(cache *Cache) map[HolderKey]RemoteHeldEntry {
		out := map[HolderKey]RemoteHeldEntry{}
		for _, key := range keys {
			if held, ok := cache.RemoteHoldingInfo(key); ok {
				out[key] = held
			}
		}
		return out
	}
	deps := func(h RemoteHoldingInfo) []uint64 {
		return slices.Sorted(slices.Values(slices.Concat(h.Deps, h.UnknownDeps)))
	}

	ctx, whole := storedPartTestCache(t, WithBlobStore())
	sent, reply := setup(t, whole)
	wholeChange, err := whole.ApplyMergedReply(ctx, c, sent, reply)
	require.NoError(t, err)
	want := read(whole)
	require.Len(t, want, records)

	_, pieced := storedPartTestCache(t, WithBlobStore())
	sent, reply = setup(t, pieced)
	apply, err := PrepareMergedReply(c, sent, reply)
	require.NoError(t, err)
	require.Equal(t, records, apply.Records())
	var candidates []HolderKey
	for applied := 0; ; {
		w := pieced.HoldRemoteWrites()
		change, done := w.ApplyMergedReplyPiece(ctx, apply, 1)
		require.NoError(t, w.Release(ctx))
		candidates = append(candidates, change.Candidates...)
		if !done {
			applied++
		}
		got := read(pieced)
		for _, key := range keys {
			held, ok := got[key]
			if int(key.Number-100) > applied && !done {
				require.False(t, ok, "%v, not applied yet, after %d records", key, applied)
				continue
			}
			require.True(t, ok, "%v after %d records", key, applied)
			final := want[key]
			require.Equal(t, final.Number, held.Number, "%v", key)
			require.Equal(t, final.Holding.Parts, held.Holding.Parts, "%v", key)
			require.Equal(t, final.Holding.Replacements, held.Holding.Replacements, "%v", key)
			require.Equal(t, final.Holding.ExpiresAtUnix, held.Holding.ExpiresAtUnix, "%v", key)
			require.Equal(t, deps(final.Holding), deps(held.Holding), "%v's dependencies, known or to come, after %d records", key, applied)
			for _, dep := range held.Holding.Deps {
				require.LessOrEqual(t, int(dep-100), applied, "%v names %d as held before it is", key, dep)
			}
		}
		if done {
			break
		}
	}
	require.Equal(t, want, read(pieced), "pieces leave the cache as the whole reply does")
	set := func(keys []HolderKey) []HolderKey {
		keys = slices.Clone(keys)
		slices.SortFunc(keys, compareHolderKeys)
		return slices.Compact(keys)
	}
	require.Equal(t, set(wholeChange.Candidates), set(candidates), "the same candidates")
}
