package dagql

import (
	"fmt"
	"slices"
	"testing"
	"unique"

	"github.com/stretchr/testify/require"
)

// heldSetsSnapshot is a holding's sets as one read saw them: the slices
// themselves, and copies of what they held then.
type heldSetsSnapshot struct {
	deps, unknownDeps         []uint64
	sessions                  []unique.Handle[string]
	parts                     []heldPart
	wantDeps, wantUnknownDeps []uint64
	wantSessions              []unique.Handle[string]
	wantParts                 []heldPart
	// facts are HoldingFacts' slices, which callers keep.
	facts, wantFacts HoldingFacts
}

func snapshotHeldSets(t *testing.T, cloud *Cache, key HolderKey) heldSetsSnapshot {
	t.Helper()
	facts, ok := cloud.HoldingFacts(key)
	require.True(t, ok)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	_, h := cloud.holdingLocked(key)
	require.NotNil(t, h)
	return heldSetsSnapshot{
		deps: h.deps, unknownDeps: h.unknownDeps, sessions: h.sessions, parts: h.parts,
		wantDeps: slices.Clone(h.deps), wantUnknownDeps: slices.Clone(h.unknownDeps),
		wantSessions: slices.Clone(h.sessions), wantParts: slices.Clone(h.parts),
		facts: facts,
		wantFacts: HoldingFacts{
			Deps:        slices.Clone(facts.Deps),
			UnknownDeps: slices.Clone(facts.UnknownDeps),
		},
	}
}

func (s heldSetsSnapshot) requireUnchanged(t *testing.T, after string) {
	t.Helper()
	require.Equal(t, s.wantDeps, s.deps, "deps read before %s", after)
	require.Equal(t, s.wantUnknownDeps, s.unknownDeps, "unknown deps read before %s", after)
	require.Equal(t, s.wantSessions, s.sessions, "sessions read before %s", after)
	require.Equal(t, s.wantParts, s.parts, "parts read before %s", after)
	require.Equal(t, s.wantFacts.Deps, s.facts.Deps, "HoldingFacts' deps read before %s", after)
	require.Equal(t, s.wantFacts.UnknownDeps, s.facts.UnknownDeps, "HoldingFacts' unknown deps read before %s", after)
}

// A holding's sets are replaced, never changed in place: a slice read under
// the lock, HoldingFacts' among them, stays as it was read through every
// write that adds to, removes from or changes each set: dependencies known
// and unknown, sessions and parts.
func TestHeldSetsAreReplacedNotChanged(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	ctx := t.Context()
	cache := CacheID("cache-a")
	parent := HolderKey{Cache: cache, Number: 10}
	a.call(callRow{key: HolderKey{Cache: cache, Number: 11}, session: "s1", holding: holdingOf("dep-11")})
	root := holdingOf("root", 11, 12, 13, 14)
	root.Parts = []PersistedPartAddress{{Part: "meta"}}
	a.call(callRow{key: parent, session: "s1", holding: root})
	_, err := cloud.AddRemoteHold(ctx, parent, "s2")
	require.NoError(t, err)
	_, err = cloud.AddRemoteHold(ctx, parent, "s3")
	require.NoError(t, err)
	_, ok, err := cloud.ApplyHeldValueState(ctx, parent, HeldValueState{OfferedParts: []PersistedPartAddress{{Part: "fs"}, {Part: "snapshot"}}})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, a.collect())

	var snapshots []heldSetsSnapshot
	step := func(name string, write func()) {
		t.Helper()
		snapshots = append(snapshots, snapshotHeldSets(t, cloud, parent))
		write()
		for _, s := range snapshots {
			s.requireUnchanged(t, name)
		}
	}
	info := func() RemoteHoldingInfo {
		held, ok := cloud.RemoteHoldingInfo(parent)
		require.True(t, ok)
		return held.Holding
	}
	before := info()
	require.Equal(t, []uint64{11}, before.Deps)
	require.Equal(t, []uint64{12, 13, 14}, before.UnknownDeps)
	require.Equal(t, []string{"s1", "s2", "s3"}, before.Sessions)

	step("a part completes", func() {
		_, _, err := cloud.UpdateRemoteHolding(ctx, parent, RemoteHoldingUpdate{Parts: []PersistedPartAddress{{Part: "fs"}}})
		require.NoError(t, err)
	})
	require.Equal(t, []PersistedPartAddress{{Part: "fs"}, {Part: "meta"}}, info().Parts)
	step("an unknown dependency's holding arrives", func() {
		a.call(callRow{key: HolderKey{Cache: cache, Number: 12}, session: "s1", holding: holdingOf("dep-12")})
	})
	require.Equal(t, []uint64{11, 12}, info().Deps)
	require.Equal(t, []uint64{13, 14}, info().UnknownDeps)
	step("a session ends", func() {
		a.endSession(cache, "s2")
	})
	require.Equal(t, []string{"s1", "s3"}, info().Sessions)
	step("a dependency's holding goes", func() {
		_, found, err := cloud.CollectRemoteHolding(ctx, HolderKey{Cache: cache, Number: 11})
		require.NoError(t, err)
		require.True(t, found)
	})
	require.Equal(t, []uint64{12}, info().Deps)
	require.Equal(t, []uint64{11, 13, 14}, info().UnknownDeps)
	step("a new value drops dependencies and parts", func() {
		_, _, err := cloud.UpdateRemoteHolding(ctx, parent, RemoteHoldingUpdate{Replacements: 1, Deps: []uint64{12, 14}, Parts: []PersistedPartAddress{{Part: "fs"}}})
		require.NoError(t, err)
	})
	require.Equal(t, []uint64{12}, info().Deps)
	require.Equal(t, []uint64{14}, info().UnknownDeps)
	require.Equal(t, []PersistedPartAddress{{Part: "fs"}}, info().Parts)
	step("a dependency is added", func() {
		_, _, err := cloud.UpdateRemoteHolding(ctx, parent, RemoteHoldingUpdate{Replacements: 1, Deps: []uint64{15}})
		require.NoError(t, err)
	})
	require.Equal(t, []uint64{14, 15}, info().UnknownDeps)
	step("a session is added", func() {
		_, err := cloud.AddRemoteHold(ctx, parent, "s0")
		require.NoError(t, err)
	})
	require.Equal(t, []string{"s0", "s1", "s3"}, info().Sessions)
}

// HoldingFacts answers, for every holding, what RemoteHoldingInfo and its
// entry's facts answer for the same fields.
func TestHoldingFactsAnswersAsRemoteHoldingInfo(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	ctx := t.Context()
	var keys []HolderKey
	for _, cache := range []CacheID{"cache-a", "cache-b"} {
		for n := uint64(1); n <= 6; n++ {
			h := holdingOf(fmt.Sprintf("entry-%d", n%4), n-1, n+10)
			h.TypeName = []string{"Directory", "File", "Container", ""}[n%4]
			h.ExpiresAtUnix = int64(n % 3 * 1000)
			h.Executed = n%2 == 0
			h.Image = n == 5
			h.Replacements = n % 2
			h.ContentDigest = testDigest(fmt.Sprintf("content-%d", n%3))
			h.Parts = []PersistedPartAddress{{Part: "fs"}, {Part: PartKey(fmt.Sprintf("mount:/m%d", n))}}
			key := HolderKey{Cache: cache, Number: n}
			var retained *RetentionObservation
			if n%2 == 1 {
				retained = retainedAt(1, int64(n), int64(n*500))
			}
			a.call(callRow{key: key, session: "s", holding: h, retained: retained})
			_, _, err := cloud.ApplyHeldValueState(ctx, key, HeldValueState{Replacements: n % 2, OfferedParts: []PersistedPartAddress{{Part: "snapshot"}}})
			require.NoError(t, err)
			keys = append(keys, key)
		}
	}
	require.Empty(t, a.collect())
	keys = append(keys, HolderKey{Cache: "cache-none", Number: 1})
	for _, key := range keys {
		held, ok := cloud.RemoteHoldingInfo(key)
		facts, factsOK := cloud.HoldingFacts(key)
		require.Equal(t, ok, factsOK, "%v", key)
		if !ok {
			continue
		}
		h := held.Holding
		require.Equal(t, HoldingFacts{
			Number:                 held.Number,
			Stored:                 held.Stored,
			StoredExpiresAtUnix:    held.StoredExpiresAtUnix,
			TypeName:               h.TypeName,
			ExpiresAtUnix:          h.ExpiresAtUnix,
			Retained:               h.Retained,
			RetentionExpiresAtUnix: h.RetentionExpiresAtUnix,
			Replacements:           h.Replacements,
			Executed:               h.Executed,
			Image:                  h.Image,
			Deps:                   h.Deps,
			UnknownDeps:            h.UnknownDeps,
			Parts:                  h.Parts,
		}, facts, "%v", key)
		require.NotEmpty(t, h.OfferedParts, "%v has offered parts, which HoldingFacts leaves out", key)
	}
}

// BenchmarkHoldingsOnOneEntry attaches holdings of many engine caches to one
// entry, each with a session, and then releases and collects them all: the
// cost of an entry held by every engine of several pull requests at once.
func BenchmarkHoldingsOnOneEntry(b *testing.B) {
	for _, holders := range []int{30, 300} {
		b.Run(fmt.Sprintf("holders=%d", holders), func(b *testing.B) {
			ctx := b.Context()
			cloud, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer cloud.CloseDiscardingPersistence()
			caches := make([]CacheID, holders)
			for i := range caches {
				caches[i] = CacheID(fmt.Sprintf("cache-%03d", i))
			}
			h := RemoteHolding{Recipe: testDigest("shared"), Request: testDigest("shared"), Field: "shared", TypeName: "Directory",
				Parts: []PersistedPartAddress{{Part: "fs"}}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, cache := range caches {
					key := HolderKey{Cache: cache, Number: 1}
					if _, err := cloud.AttachRemoteHolding(ctx, key, h); err != nil {
						b.Fatal(err)
					}
					if _, err := cloud.AddRemoteHold(ctx, key, "session"); err != nil {
						b.Fatal(err)
					}
				}
				var candidates []HolderKey
				for _, cache := range caches {
					candidates = append(candidates, cloud.ReleaseRemoteCache(ctx, cache).Candidates...)
				}
				if _, _, err := cloud.CollectRemoteHoldings(ctx, candidates); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*holders), "ns/holding")
		})
	}
}
