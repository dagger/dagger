package dagql

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// changeTestEntry is everything a reader can keep of one entry of a
// blob-backed cache: its own facts, its holdings' keys, its classes' entries,
// and its part answers, by part address key.
type changeTestEntry struct {
	Stored              bool
	StoredExpiresAtUnix int64
	Recipes             []digest.Digest
	Holders             []HolderKey
	Class               []uint64
	Parts               map[string]PartAvailability
}

// changeTestAnswers is every answer a reader can keep of a blob-backed
// cache, at one clock: each holding's description, and each entry's.
type changeTestAnswers struct {
	holdings map[HolderKey]RemoteHoldingInfo
	entries  map[uint64]changeTestEntry
}

// changeTestAddresses returns every part address the cache knows: its
// entries' stored parts, the parts their records map, and its holdings'
// complete and offered parts.
func changeTestAddresses(c *Cache) map[string]PersistedPartAddress {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	addresses := map[string]PersistedPartAddress{}
	add := func(address PersistedPartAddress) {
		if key, err := partAddressKey(address); err == nil {
			addresses[key] = address
		}
	}
	for _, res := range c.resultsByID {
		for _, part := range res.storedParts {
			add(part.address())
		}
		if res.persistedEnvelope != nil {
			call, _ := res.entryFrameLocked().decode()
			outputs, err := mapTransferredOutputs(PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: call})
			if err == nil {
				for _, output := range outputs {
					add(output.Address)
				}
			}
		}
		for _, ref := range res.holderRefsLocked() {
			info := holdingInfo(ref.key, ref.h)
			for _, address := range append(info.Parts, info.OfferedParts...) {
				add(address)
			}
		}
	}
	return addresses
}

// changeTestRead reads every answer through the cache's own reads, at now.
func changeTestRead(t *testing.T, c *Cache, now int64, addresses map[string]PersistedPartAddress) changeTestAnswers {
	t.Helper()
	c.egraphMu.RLock()
	keys := slices.Collect(maps.Keys(c.holderEntriesForTest()))
	numbers := make([]uint64, 0, len(c.resultsByID))
	for id := range c.resultsByID {
		numbers = append(numbers, uint64(id))
	}
	c.egraphMu.RUnlock()
	answers := changeTestAnswers{holdings: map[HolderKey]RemoteHoldingInfo{}, entries: map[uint64]changeTestEntry{}}
	for _, key := range keys {
		held, ok := c.RemoteHoldingInfo(key)
		require.True(t, ok, "holding %v", key)
		answers.holdings[key] = held.Holding
	}
	for _, number := range numbers {
		info, ok := c.EntryInfo(number)
		require.True(t, ok, "entry %d", number)
		class, ok := c.EntryClassEntries(number)
		require.True(t, ok, "entry %d", number)
		entry := changeTestEntry{
			Stored:              info.Stored,
			StoredExpiresAtUnix: info.StoredExpiresAtUnix,
			Recipes:             info.Recipes,
			Class:               class.Entries,
			Parts:               map[string]PartAvailability{},
		}
		for _, h := range info.Holdings {
			entry.Holders = append(entry.Holders, h.Key)
		}
		for key, address := range addresses {
			av, err := c.AvailablePart(number, address, now)
			require.NoError(t, err)
			entry.Parts[key] = av
		}
		answers.entries[number] = entry
	}
	return answers
}

// changeWatch checks, write by write, that each write's change names every
// holding and entry whose answers differ after it, and counts those it names
// with none differing.
type changeWatch struct {
	t     *testing.T
	cache *Cache
	now   int64
	last  changeTestAnswers
	// overHoldings and overEntries count the holdings and entries a change
	// named whose answers didn't differ, and readOnly the holdings named
	// whose descriptions differed only in what a reader may not need: the
	// sessions holding them and their dependents.
	overHoldings, overEntries, readOnly int
	named                               int
}

func newChangeWatch(t *testing.T, c *Cache, now int64) *changeWatch {
	w := &changeWatch{t: t, cache: c, now: now}
	w.reread()
	return w
}

// read reads every answer, over every part address known before or after.
func (w *changeWatch) read(extra map[string]PersistedPartAddress) changeTestAnswers {
	addresses := changeTestAddresses(w.cache)
	maps.Copy(addresses, extra)
	return changeTestRead(w.t, w.cache, w.now, addresses)
}

// reread takes the answers afresh, after the test changed the cache by
// hand, outside any write.
func (w *changeWatch) reread() {
	w.last = w.read(nil)
}

// step runs one write and checks what its change names.
func (w *changeWatch) step(name string, write func() RemoteChange) {
	w.t.Helper()
	before := w.last
	beforeAddresses := changeTestAddresses(w.cache)
	change := write()
	after := w.read(beforeAddresses)
	w.cache.egraphMu.RLock()
	pending := w.cache.changesPendingLocked()
	w.cache.egraphMu.RUnlock()
	require.False(w.t, pending, "%s: every mark is handed back by the write that made it", name)

	require.True(w.t, slices.IsSortedFunc(change.Holdings, compareChangedHoldings), "%s: holdings sorted by number, then cache", name)
	require.True(w.t, slices.IsSorted(change.Entries), "%s: entries sorted", name)
	holdings := map[HolderKey]bool{}
	for _, key := range change.Holdings {
		holdings[key] = true
	}
	entries := map[uint64]bool{}
	for _, number := range change.Entries {
		entries[number] = true
	}
	w.named += len(change.Holdings) + len(change.Entries)

	for _, key := range changeTestUnion(before.holdings, after.holdings) {
		b, inBefore := before.holdings[key]
		a, inAfter := after.holdings[key]
		differs := inBefore != inAfter || !reflect.DeepEqual(b, a)
		switch {
		case differs && !holdings[key]:
			w.t.Errorf("%s: holding %v changed but wasn't named:\nbefore %+v (%v)\nafter  %+v (%v)", name, key, b, inBefore, a, inAfter)
		case !differs && holdings[key]:
			w.overHoldings++
		case differs && inBefore && inAfter:
			b.Sessions, a.Sessions, b.Dependents, a.Dependents = nil, nil, 0, 0
			if reflect.DeepEqual(b, a) {
				w.readOnly++
			}
		}
	}
	for _, number := range changeTestUnion(before.entries, after.entries) {
		b, inBefore := before.entries[number]
		a, inAfter := after.entries[number]
		differs := inBefore != inAfter || !changeTestEntryEqual(b, a, beforeAddresses)
		switch {
		case differs && !entries[number]:
			w.t.Errorf("%s: entry %d changed but wasn't named:\nbefore %+v (%v)\nafter  %+v (%v)", name, number, b, inBefore, a, inAfter)
		case !differs && entries[number]:
			w.overEntries++
		}
	}
	w.last = after
}

// changeTestEntryEqual compares two readings of an entry, its part answers
// over the addresses known before the write: a part address the write added
// has no answer before it.
func changeTestEntryEqual(b, a changeTestEntry, addresses map[string]PersistedPartAddress) bool {
	bp, ap := b.Parts, a.Parts
	b.Parts, a.Parts = nil, nil
	if !reflect.DeepEqual(b, a) {
		return false
	}
	for key := range ap {
		if _, known := addresses[key]; !known && ap[key].State == PartUnavailable {
			// A part no record or holding named before: unavailable before
			// too.
			continue
		}
		if !reflect.DeepEqual(bp[key], ap[key]) {
			return false
		}
	}
	return true
}

func changeTestUnion[K comparable, V any](a, b map[K]V) []K {
	keys := map[K]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	return slices.Collect(maps.Keys(keys))
}

// checkTime checks that no part answer changes before the moment it says it
// holds until, by time alone: at that moment less a second, each answer is
// the same, and an answer with no bound is the same a year on.
func (w *changeWatch) checkTime(name string) {
	w.t.Helper()
	addresses := changeTestAddresses(w.cache)
	for number, entry := range w.last.entries {
		for key, av := range entry.Parts {
			if _, ok := addresses[key]; !ok {
				continue
			}
			at := w.now + 365*24*3600
			if av.ValidUntilUnix != 0 {
				require.Greater(w.t, av.ValidUntilUnix, w.now, "%s: entry %d part %s holds until a moment to come", name, number, key)
				at = av.ValidUntilUnix - 1
			}
			later, err := w.cache.AvailablePart(number, addresses[key], at)
			require.NoError(w.t, err)
			require.Equal(w.t, av, later, "%s: entry %d part %s at %d, before its answer's time", name, number, key, at)
		}
	}
}

// changeTestRecipe returns the recipe of the entry numbered number.
func changeTestRecipe(c *Cache, number uint64) (digest.Digest, string) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	return res.recipeKeys[0], res.description
}

// changeTestMerge merges a bundle into the Cloud from an engine cache and
// returns the change and the Cloud's number for each sender number.
func changeTestMerge(t *testing.T, ctx context.Context, cloud *Cache, from CacheID, bundle ValueBundle) (RemoteChange, map[uint64]uint64) {
	t.Helper()
	reply, err := cloud.MergeValues(ctx, from, bundle)
	require.NoError(t, err)
	numbers := map[uint64]uint64{}
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal == value.Ordinal {
				numbers[sent.SenderNumber] = value.Number
			}
		}
	}
	return reply.Change, numbers
}

// Every write to a blob-backed cache names, in its change, every holding
// whose description differs after it and every entry whose facts, holdings,
// class or part answers differ, read at one clock; and leaves no mark for a
// later write to hand back. Each kind of write the service makes runs here,
// in an order that gives each something to change: merges that create,
// store and replace records, rows that attach, update and join, holds,
// retention and their release, stored and dropped parts, merged replies,
// collection, and prunes.
func TestChangeReportsNameEveryChangedAnswer(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	now := time.Now().Unix()
	w := newChangeWatch(t, cloud, now)

	// Engine A's root R, which depends on P.
	actx, a, asrv := transferTestCache(t)
	rRes := persistedListTestResult(t, actx, a, asrv, "change-root", &transferTestValue{Text: "root"})
	pRes := persistedListTestResult(t, actx, a, asrv, "change-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, rRes, pRes)
	rootBundle := exportTestBundle(t, actx, a, rRes)
	var r, p uint64
	w.step("merge R and P from cache-a", func() RemoteChange {
		change, numbers := changeTestMerge(t, ctx, cloud, "cache-a", rootBundle)
		r, p = numbers[uint64(rRes.cacheSharedResult().id)], numbers[uint64(pRes.cacheSharedResult().id)]
		return change
	})
	require.NotZero(t, r)
	require.NotZero(t, p)

	// Engine B's M, of another recipe.
	memberBundle, _ := mergeTestSource(t, "change-member", "member")
	var m uint64
	w.step("merge M from cache-b", func() RemoteChange {
		reply, err := cloud.MergeValues(ctx, "cache-b", memberBundle)
		require.NoError(t, err)
		m = reply.Imported()[0].ResultID
		return reply.Change
	})

	// A value whose record maps every part absent.
	bctx, b, bsrv := transferTestCache(t)
	bsrv.InstallObject(NewClass(bsrv, ClassOpts[*inlineCaptureTestValue]{}))
	absentRes := persistedListTestResult(t, bctx, b, bsrv, "change-absent", &inlineCaptureTestValue{})
	absentBundle := exportTestBundle(t, bctx, b, absentRes)
	var absent uint64
	w.step("merge an all-absent value from cache-b", func() RemoteChange {
		change, numbers := changeTestMerge(t, ctx, cloud, "cache-b", absentBundle)
		absent = numbers[uint64(absentRes.cacheSharedResult().id)]
		return change
	})
	require.NotZero(t, absent)

	rRecipe, rField := changeTestRecipe(cloud, r)
	mRecipe, mField := changeTestRecipe(cloud, m)
	absentRecipe, absentField := changeTestRecipe(cloud, absent)
	snapshot := PersistedPartAddress{Part: "snapshot"}
	attach := func(key HolderKey, desc RemoteHolding) func() RemoteChange {
		return func() RemoteChange {
			change, err := cloud.AttachRemoteHolding(ctx, key, desc)
			require.NoError(t, err)
			return change
		}
	}
	rKey, mKey, absentKey := HolderKey{"cache-c", 10}, HolderKey{"cache-c", 11}, HolderKey{"cache-c", 14}
	w.step("attach cache-c's holding of R", attach(rKey, RemoteHolding{Recipe: rRecipe, Request: rRecipe, Field: rField, TypeName: "Directory", ContentDigest: testDigest("change-content"), Parts: []PersistedPartAddress{snapshot}, Executed: true}))
	w.step("attach cache-c's holding of M, of R's content: a class join", attach(mKey, RemoteHolding{Recipe: mRecipe, Request: mRecipe, Field: mField, TypeName: "Directory", ContentDigest: testDigest("change-content")}))
	w.step("attach cache-c's holding of the absent value, of R's content too", attach(absentKey, RemoteHolding{Recipe: absentRecipe, Request: absentRecipe, Field: absentField, ContentDigest: testDigest("change-content")}))
	parentKey, childKey := HolderKey{"cache-c", 12}, HolderKey{"cache-c", 99}
	w.step("attach a holding with no record, naming an unknown dependency", attach(parentKey, holdingOf("change-held-only", 99)))
	w.step("attach the dependency it named", attach(childKey, holdingOf("change-child")))
	update := func(key HolderKey, u RemoteHoldingUpdate) func() RemoteChange {
		return func() RemoteChange {
			change, found, err := cloud.UpdateRemoteHolding(ctx, key, u)
			require.NoError(t, err)
			require.True(t, found)
			return change
		}
	}
	w.step("teach the dependency a content digest", update(childKey, RemoteHoldingUpdate{ContentDigest: testDigest("change-second-content")}))
	w.step("teach its parent the same content: a class join with no holding created", update(parentKey, RemoteHoldingUpdate{ContentDigest: testDigest("change-second-content")}))
	congruent := func(field string) RemoteHolding {
		return RemoteHolding{Recipe: testDigest(field), Request: testDigest(field), Field: field, Term: &RemoteTerm{Self: testDigest("change-congruent-self")}}
	}
	w.step("attach a holding with a term", attach(HolderKey{"cache-c", 20}, congruent("change-congruent-a")))
	w.step("attach a holding of another recipe with the same term: a join by congruence", attach(HolderKey{"cache-c", 21}, congruent("change-congruent-b")))
	w.step("attach cache-e's holding of R: a second engine's", attach(HolderKey{"cache-e", 5}, RemoteHolding{Recipe: rRecipe, Request: rRecipe, Field: rField}))

	setPart := func(number uint64, part PersistedPartOffer) func() RemoteChange {
		return func() RemoteChange {
			stored, change, err := cloud.SetStoredPart(ctx, number, part, 0)
			require.NoError(t, err)
			require.True(t, stored)
			return change
		}
	}
	w.step("store M's snapshot", setPart(m, storedPruneTestPart("snapshot", map[string]int64{"change-m-layer": 10})))
	w.checkTime("after M's part")
	w.step("store R's snapshot", setPart(r, storedPruneTestPart("snapshot", map[string]int64{"change-r-layer": 10})))

	w.step("hold R in session s1", func() RemoteChange {
		change, err := cloud.AddRemoteHold(ctx, rKey, "s1")
		require.NoError(t, err)
		return change
	})
	w.step("hold R in session s1 again: nothing changes", func() RemoteChange {
		change, err := cloud.AddRemoteHold(ctx, rKey, "s1")
		require.NoError(t, err)
		return change
	})
	w.step("hold M in session s2", func() RemoteChange {
		change, err := cloud.AddRemoteHold(ctx, mKey, "s2")
		require.NoError(t, err)
		return change
	})
	w.step("retain R's holding", func() RemoteChange {
		change, err := cloud.ObserveRemoteRetention(ctx, rKey, *retainedAt(1, time.Now().UnixNano(), 0))
		require.NoError(t, err)
		return change
	})
	w.step("complete a part of R's holding", func() RemoteChange {
		change, found, err := cloud.UpdateRemoteHolding(ctx, rKey, RemoteHoldingUpdate{Parts: []PersistedPartAddress{{Part: "mount:/src"}}})
		require.NoError(t, err)
		require.True(t, found)
		return change
	})
	w.step("replace the parent's value state at a higher count", func() RemoteChange {
		change, found, err := cloud.ApplyHeldValueState(ctx, parentKey, HeldValueState{Replacements: 1, Deps: []uint64{99}, ExpiresAtUnix: now + 3600})
		require.NoError(t, err)
		require.True(t, found)
		return change
	})
	w.step("a kept call lowers M's expiry", attach(mKey, RemoteHolding{Recipe: mRecipe, Request: testDigest("change-m-call"), Field: mField, ExpiresAtUnix: now + 600}))
	w.checkTime("after M's expiry")

	// Placement: R's closure merged into engine D.
	w.step("apply cache-d's merged reply for R", func() RemoteChange {
		built, err := cloud.StoredBundle(ctx, []uint64{r}, now, nil)
		require.NoError(t, err)
		reply := MergeReply{Generation: 1, EngineTimeUnixNano: time.Now().UnixNano()}
		for _, value := range built.Bundle.Values {
			var deps []uint64
			for _, dep := range value.DependencyIDs {
				deps = append(deps, 1000+dep)
			}
			reply.Values = append(reply.Values, MergedValue{Ordinal: value.Ordinal, Number: 1000 + uint64(value.Ordinal), Deps: deps, Parts: []PersistedPartAddress{snapshot}})
		}
		for _, root := range built.Bundle.Roots {
			reply.Roots = append(reply.Roots, MergedRoot{Ordinal: root.Ordinal, Number: 1000 + uint64(root.Ordinal), Retained: true})
		}
		change, err := cloud.ApplyMergedReply(ctx, "cache-d", built.Bundle, reply)
		require.NoError(t, err)
		return change
	})

	w.step("drop the blob of M's part", func() RemoteChange {
		dropped, change, err := cloud.DropStoredParts(testDigest("change-m-layer"))
		require.NoError(t, err)
		require.Len(t, dropped, 1)
		return change
	})
	packed := storedPruneTestPart("mount:/cache", map[string]int64{"change-packed-layer": 10})
	packed.Chain.Packs = map[digest.Digest]digest.Digest{testDigest("change-packed-layer"): testDigest("change-pack")}
	w.step("store a part of M's held in a pack", setPart(m, packed))
	w.step("drop the pack", func() RemoteChange {
		dropped, change, err := cloud.DropStoredPack(testDigest("change-pack"))
		require.NoError(t, err)
		require.Len(t, dropped, 1)
		return change
	})
	w.step("end session s1", func() RemoteChange { return cloud.ReleaseRemoteSession(ctx, "cache-c", "s1") })
	w.step("end the process that held s2", func() RemoteChange { return cloud.EndRemoteProcess(ctx, "cache-c", []string{"s2"}) })

	// R's value expires, and engine A sends it again: the Cloud replaces it
	// in place.
	cloud.egraphMu.Lock()
	cloud.resultsByID[sharedResultID(r)].expiresAtUnix = now - 60
	cloud.egraphMu.Unlock()
	w.reread()
	w.step("merge R again over its expired value: a replacement", func() RemoteChange {
		change, _ := changeTestMerge(t, ctx, cloud, "cache-a", rootBundle)
		return change
	})
	w.checkTime("after the replacement")

	w.step("collect the dependency the parent named", func() RemoteChange {
		change, found, err := cloud.CollectRemoteHolding(ctx, childKey)
		require.NoError(t, err)
		require.True(t, found)
		return change
	})
	var candidates []HolderKey
	w.step("release cache-d", func() RemoteChange {
		change := cloud.ReleaseRemoteCache(ctx, "cache-d")
		candidates = change.Candidates
		return change
	})
	w.step("collect cache-d's holdings", func() RemoteChange {
		_, change, err := cloud.CollectRemoteHoldings(ctx, candidates)
		require.NoError(t, err)
		return change
	})
	w.step("prune every stored root", func() RemoteChange {
		report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
		require.NoError(t, err)
		require.Positive(t, report.DroppedValues)
		return report.Change
	})
	w.checkTime("after the prune")
	w.step("merge R and P again onto their entries with no value", func() RemoteChange {
		change, _ := changeTestMerge(t, ctx, cloud, "cache-a", rootBundle)
		return change
	})
	w.step("store R's snapshot again", setPart(r, storedPruneTestPart("snapshot", map[string]int64{"change-r-layer": 10})))
	w.step("prune by the blob store's space", func() RemoteChange {
		report, err := cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 5, TargetSpace: 1}})
		require.NoError(t, err)
		require.Positive(t, report.DroppedValues)
		return report.Change
	})
	for _, cache := range []CacheID{"cache-a", "cache-b", "cache-c", "cache-e"} {
		w.step("release "+string(cache), func() RemoteChange {
			change := cloud.ReleaseRemoteCache(ctx, cache)
			candidates = change.Candidates
			return change
		})
		w.step("collect "+string(cache)+"'s holdings", func() RemoteChange {
			_, change, err := cloud.CollectRemoteHoldings(ctx, candidates)
			require.NoError(t, err)
			return change
		})
	}
	cloud.egraphMu.RLock()
	left := len(cloud.resultsByID)
	cloud.egraphMu.RUnlock()
	require.Zero(t, left, "every entry went with its last holding")
	t.Logf("named %d; named with no answer differing: %d holdings, %d entries; holdings differing only in sessions or dependents: %d", w.named, w.overHoldings, w.overEntries, w.readOnly)
}

// An engine's own cache marks nothing: its writes' changes name no holding
// or entry, and it has no marks to hand back.
func TestChangeReportsOnlyOnABlobBackedCache(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "change-engine-root", "root")
	ctx, c, _ := transferTestCache(t)
	reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Empty(t, reply.Change.Entries)
	require.Empty(t, reply.Change.Holdings)
	require.Nil(t, c.changes)
}
