package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// relocateTestSets are the sets a Cloud sends a fresh engine for three
// results of one engine: "kept", "shared", and "root", a list of the two.
// Each set's records carry their parts, as a restore's set files' do, so a
// merge leaves them unprepared.
type relocateTestSets struct {
	kept, shared, root ValueBundle
}

func relocateTestCloudSets(t *testing.T) relocateTestSets {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	kept := persistedListTestResult(t, actx, a, asrv, "set-kept", String("kept"))
	shared := persistedListTestResult(t, actx, a, asrv, "set-shared", String("shared"))
	root := persistedListTestResult(t, actx, a, asrv, "set-root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{kept, shared}})
	exported := exportTestBundle(t, actx, a, root)
	require.Len(t, exported.Values, 3)

	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exported)
	require.NoError(t, err)
	numbers := map[uint64]uint64{}
	for _, value := range reply.Values {
		for _, sent := range exported.Values {
			if sent.Ordinal == value.Ordinal {
				numbers[sent.SenderNumber] = value.Number
			}
		}
	}
	set := func(res AnyResult) ValueBundle {
		n := numbers[uint64(res.cacheSharedResult().id)]
		require.NotZero(t, n)
		got, err := cloud.StoredBundle(ctx, []uint64{n}, time.Now().Unix(), nil)
		require.NoError(t, err)
		bundle, err := cloneValueBundle(got.Bundle)
		require.NoError(t, err)
		return bundle
	}
	sets := relocateTestSets{kept: set(kept), shared: set(shared), root: set(root)}
	require.Len(t, sets.root.Values, 3)

	// A merge of the root's set leaves every record unprepared.
	pctx, c, _ := transferTestCache(t)
	p, err := c.PrepareValueMerge(pctx, cloudCacheID, sets.root)
	require.NoError(t, err)
	require.Len(t, p.prep.unprepared, 3, "every record of a set carries its parts")
	p.Release()
	return sets
}

// relocateTestLanding is what a restore of sets left: the cache's entries,
// named by their recipes, and what the last restore's reply said of each of
// its records and roots, with how many records its commit relocated under
// the graph lock.
type relocateTestLanding struct {
	state []string
	reply []string
	rows  MergeRowCounts
	under int
}

func relocateTestLand(t *testing.T, c *Cache, reply MergeReply, under int) relocateTestLanding {
	t.Helper()
	c.egraphMu.RLock()
	names := mergeTestEntryNames(c)
	c.egraphMu.RUnlock()
	named := func(numbers []uint64) []string {
		out := make([]string, 0, len(numbers))
		for _, n := range numbers {
			out = append(out, names[n])
		}
		slices.Sort(out)
		return out
	}
	var landed []string
	for _, value := range reply.Values {
		landed = append(landed, fmt.Sprintf("value %d=%s replacements=%d expires=%d deps=%v parts=%v offered=%v",
			value.Ordinal, names[value.Number], value.Replacements, value.ExpiresAtUnix, named(value.Deps), value.Parts, value.OfferedParts))
	}
	for _, root := range reply.Roots {
		landed = append(landed, fmt.Sprintf("root %d=%s expired=%t retained=%t retention-expires=%t",
			root.Ordinal, names[root.Number], root.Expired, root.Retained, root.RetentionExpiresAtUnix != 0))
	}
	slices.Sort(landed)
	return relocateTestLanding{state: relocateTestState(t, c), reply: landed, rows: reply.Rows, under: under}
}

// relocateTestState is a cache's entries as a restore of sets leaves them,
// each named by its recipes in place of its number, as itself and wherever
// a number names an entry: its dependencies; its record's call and offers,
// relocated; and what preparing its payload at its first use takes. The
// payload, which names rows by the Cloud's numbers until then, is as the
// Cloud sent it.
func relocateTestState(t *testing.T, c *Cache) []string {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	names := mergeTestEntryNames(c)
	named := func(id uint64) string { return names[id] }
	var out []string
	for id, res := range c.resultsByID {
		var deps []string
		for _, dep := range res.deps {
			deps = append(deps, named(uint64(dep)))
		}
		slices.Sort(deps)
		var refs []string
		collect := func(ref *PersistedRef) error {
			if ref.RecipeID == nil && ref.ResultID != 0 {
				refs = append(refs, named(ref.ResultID))
			}
			return nil
		}
		record := "none"
		res.payloadMu.RLock()
		envelope, use := res.persistedEnvelope, res.firstUse
		res.payloadMu.RUnlock()
		if call := res.loadResultCall(); call != nil {
			require.NoError(t, visitResultCallReferences(call.clone(), collect))
		}
		if envelope != nil {
			offers, err := clonePartOffers(envelope.PendingOffers)
			require.NoError(t, err)
			for i := range offers {
				require.NoError(t, visitPersistedPartOffer(&offers[i], nil, collect))
			}
			env := *envelope
			env.ResultID, env.PendingOffers = 0, nil
			raw, err := json.Marshal(env)
			require.NoError(t, err)
			record = string(raw)
		}
		first := "none"
		if use != nil {
			numbers := map[uint64]string{}
			for sender, n := range use.numbers {
				numbers[sender] = named(uint64(n))
			}
			var useDeps []string
			for _, n := range use.deps {
				useDeps = append(useDeps, named(n))
			}
			slices.Sort(useDeps)
			first = fmt.Sprintf("numbers=%v deps=%v complete=%v", numbers, useDeps, use.complete.list())
		}
		out = append(out, fmt.Sprintf("%s deps=%v refs=%v record=%s first-use=%s", named(uint64(id)), deps, refs, record, first))
	}
	slices.Sort(out)
	return out
}

// relocateTestRestore restores bundle on c and holds what it landed until
// the test ends.
func relocateTestRestore(t *testing.T, ctx context.Context, c *Cache, bundle ValueBundle) MergeReply {
	t.Helper()
	reply, err := c.RestoreValues(ctx, bundle, nil, "")
	require.NoError(t, err)
	require.True(t, reply.Committed)
	t.Cleanup(func() { require.NoError(t, reply.Release(context.Background())) })
	return reply
}

// A restore of a set renumbers its records before its commit takes the graph
// lock, as a merge of prepared records does, though a set's records are left
// unprepared: here the root's set lands after the kept record's, so the
// root's reference to it moves, and nothing is relocated under the lock.
func TestRestoredSetRelocatesBeforeTheGraphLock(t *testing.T) {
	t.Parallel()
	sets := relocateTestCloudSets(t)
	ctx, c, _ := transferTestCache(t)
	relocateTestRestore(t, ctx, c, sets.kept)
	var under []uint64
	var moved int
	c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
		for _, numbers := range used {
			for provisional, n := range numbers {
				if uint64(n) != provisional {
					moved++
				}
			}
		}
	}
	relocateTestRestore(t, ctx, c, sets.root)
	require.Empty(t, under, "no record is relocated under the graph lock")
	require.Positive(t, moved, "a reference moved, and was relocated before the lock")
}

// Relocating a set's records before the lock leaves the cache, and the
// reply, as relocating them under it does.
func TestRestoredSetRelocatedBeforeTheLockLandsAsUnderIt(t *testing.T) {
	t.Parallel()
	sets := relocateTestCloudSets(t)
	land := func(t *testing.T, before bool) relocateTestLanding {
		ctx, c, _ := transferTestCache(t)
		c.testNoPrerelocation = !before
		under := 0
		c.testRelocatedUnderLock = func(uint64) { under++ }
		relocateTestRestore(t, ctx, c, sets.kept)
		under = 0
		return relocateTestLand(t, c, relocateTestRestore(t, ctx, c, sets.root), under)
	}
	want := land(t, false)
	require.Positive(t, want.under, "relocated under the lock without the relocation before it")
	got := land(t, true)
	require.Zero(t, got.under)
	got.under = want.under
	require.Equal(t, want, got)
}

// A set's record relocated before the lock is used by its commit only if
// every number it was given still holds. Here another restore lands the
// shared record between the root set's preparation and its commit, so the
// root's reference to it moves again: the root's record is relocated again
// under the lock, and the cache and the reply end as when the sets land one
// after the other. So too when the commit decides other numbers than any
// the relocation used.
func TestRestoredSetOvertakenBeforeItsCommitRelocatesUnderTheLock(t *testing.T) {
	t.Parallel()
	sets := relocateTestCloudSets(t)
	land := func(t *testing.T, order string) relocateTestLanding {
		ctx, c, _ := transferTestCache(t)
		under := 0
		c.testRelocatedUnderLock = func(uint64) { under++ }
		relocateTestRestore(t, ctx, c, sets.kept)
		var reply MergeReply
		switch order {
		case "one after the other":
			relocateTestRestore(t, ctx, c, sets.shared)
			reply = relocateTestRestore(t, ctx, c, sets.root)
		case "overtaken before its commit":
			p, err := c.PrepareValueMerge(ctx, cloudCacheID, sets.root)
			require.NoError(t, err)
			p.restore, p.hold = true, true
			relocateTestRestore(t, ctx, c, sets.shared)
			reply, err = c.CommitValueMerge(ctx, p)
			require.NoError(t, err)
			require.True(t, reply.Committed)
			t.Cleanup(func() { require.NoError(t, reply.Release(context.Background())) })
		case "other numbers":
			relocateTestRestore(t, ctx, c, sets.shared)
			c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
				for _, numbers := range used {
					for provisional := range numbers {
						numbers[provisional] += 1000
					}
				}
			}
			reply = relocateTestRestore(t, ctx, c, sets.root)
		}
		return relocateTestLand(t, c, reply, under)
	}
	want := land(t, "one after the other")
	require.Len(t, want.reply, 4, "three records and the root")
	require.Zero(t, want.under, "nothing relocated under the lock")
	for _, order := range []string{"overtaken before its commit", "other numbers"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			got := land(t, order)
			require.Equal(t, 1, got.under, "the root's record, whose numbers no longer hold, is relocated under the lock")
			got.under = 0
			require.Equal(t, want, got)
		})
	}
}

// A restore's reply decodes its values' part addresses after its commit has
// let go of the graph lock: only the entries' part keys are read under it.
func TestRestoreRepliesWithItsPartsOutsideTheGraphLock(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, c, _ := transferTestCache(t)
	decoded, locked := 0, 0
	c.testMergeReplyPartsDecoding = func() {
		decoded++
		if !c.egraphMu.RWMutex.TryLock() {
			locked++
			return
		}
		c.egraphMu.RWMutex.Unlock()
	}
	reply := relocateTestRestore(t, ctx, c, set.bundle)
	require.Equal(t, 1, decoded)
	require.Zero(t, locked, "the graph lock is free as the reply decodes its parts")
	require.NotEmpty(t, firstUseTestReplyParts(t, set.bundle, reply, set.good), "the reply names the good record's complete part")
}
