package dagql

import (
	"context"
	"fmt"
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
	ReleaseRemoteSession(context.Context, CacheID, string) RemoteChange
	ReleaseRemoteCache(context.Context, CacheID) RemoteChange
	CollectRemoteHoldings(context.Context, []HolderKey) ([]HolderKey, RemoteChange, error)
	RemoteHoldingInfo(HolderKey) (RemoteHeldEntry, bool)
	HolderClosure(HolderKey) []HolderKey
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
// ends, retention set and dropped, a replaced value's dropped dependency,
// a whole engine cache released, collections, refused operations, and the
// reads between them.
func remoteWritesScript(t *testing.T, ctx context.Context, ops remoteWriteOps) []string {
	t.Helper()
	var out []string
	note := func(name string, v ...any) {
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

	read := func(name string) {
		for _, key := range []HolderKey{{a, 1}, {a, 2}, {a, 3}, {a, 4}, {b, 1}, {b, 5}, {b, 6}} {
			held, ok := ops.RemoteHoldingInfo(key)
			note(fmt.Sprintf("%s info %v", name, key), held, ok)
			note(fmt.Sprintf("%s closure %v", name, key), ops.HolderClosure(key))
			if ok {
				class, ok := ops.EntryClassEntries(held.Number)
				note(fmt.Sprintf("%s class %v", name, key), class, ok)
			}
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
	collect(change.Candidates...)
	change = ops.ReleaseRemoteCache(ctx, b)
	note("release cache-b", change)
	collect(change.Candidates...)
	read("released")
	return out
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
