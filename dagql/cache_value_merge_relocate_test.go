package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeTestMovingStream returns two bundles of one source cache: a child's,
// and a root's that holds the child too. Merged in that order, the root's
// child lands on the entry the first merge made, so the root's record has to
// be renumbered: its references move.
func mergeTestMovingStream(t *testing.T) (child, root ValueBundle) {
	t.Helper()
	ctx, a, srv := transferTestCache(t)
	c := persistedListTestResult(t, ctx, a, srv, "moved-child", String("child"))
	r := persistedListTestResult(t, ctx, a, srv, "moved-root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{c}})
	child = exportTestBundle(t, ctx, a, c)
	root = exportTestBundle(t, ctx, a, r)
	require.Len(t, root.Values, 2)
	return child, root
}

// mergeTestMovingPayloadStream is mergeTestMovingStream with a root whose
// object payload names its child, so that the reference that moves is in
// the root's payload.
func mergeTestMovingPayloadStream(t *testing.T) (child, root ValueBundle) {
	t.Helper()
	ctx, a, srv := transferTestCache(t)
	c := persistedListTestResult(t, ctx, a, srv, "moved-payload-child", &transferTestValue{Text: "child"})
	r := persistedListTestResult(t, ctx, a, srv, "moved-payload-root", &transferTestValue{Text: "moved-payload-root", Child: rowNumber(c)})
	transferTestDependency(a, ctx, r, c)
	child = exportTestBundle(t, ctx, a, c)
	root = exportTestBundle(t, ctx, a, r)
	require.Len(t, root.Values, 2)
	return child, root
}

// mergeTestMovingStreams are the streams whose second bundle moves a
// record's references: in a list's items, and in an object's payload.
func mergeTestMovingStreams(t *testing.T) map[string][2]ValueBundle {
	t.Helper()
	listChild, listRoot := mergeTestMovingStream(t)
	payloadChild, payloadRoot := mergeTestMovingPayloadStream(t)
	return map[string][2]ValueBundle{"list": {listChild, listRoot}, "payload": {payloadChild, payloadRoot}}
}

// mergeTestState is a cache's entries as merge leaves them: each entry's
// number, record and dependencies, in number order.
func mergeTestState(t *testing.T, c *Cache) []string {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var out []string
	for id, res := range c.resultsByID {
		var deps []uint64
		for dep := range res.deps {
			deps = append(deps, uint64(dep))
		}
		slices.Sort(deps)
		envelope, err := json.Marshal(res.persistedEnvelope)
		require.NoError(t, err)
		call, err := json.Marshal(res.loadResultCall())
		require.NoError(t, err)
		out = append(out, fmt.Sprintf("%d deps=%v call=%s envelope=%s", id, deps, call, envelope))
	}
	slices.Sort(out)
	return out
}

// mergeTestStream merges the bundles in turn into a new cache, set up by
// setup, and returns its state.
func mergeTestStream(t *testing.T, setup func(*Cache), bundles ...ValueBundle) []string {
	t.Helper()
	ctx, c, _ := transferTestCache(t)
	if setup != nil {
		setup(c)
	}
	for _, bundle := range bundles {
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
	}
	return mergeTestState(t, c)
}

// A merge renumbers its records before its commit takes the graph lock, so
// the commit, which holds the lock every check's calls wait on, doesn't
// decode and encode them again.
func TestMergeValuesRelocatesBeforeTheGraphLock(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	var under []uint64
	mergeTestStream(t, func(c *Cache) {
		c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	}, child, root)
	require.Empty(t, under, "no record is relocated under the graph lock")

	// Without the relocation before the lock, the root's record is
	// relocated under it: the stream does move a record.
	under = nil
	mergeTestStream(t, func(c *Cache) {
		c.testNoPrerelocation = true
		c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	}, child, root)
	require.Len(t, under, 1)
}

// Relocating before the lock leaves the cache as relocating under it does:
// the same entries, records and dependencies.
func TestMergeValuesRelocationBeforeTheLockLeavesTheSameCache(t *testing.T) {
	t.Parallel()
	for name, stream := range mergeTestMovingStreams(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, stream[0], stream[1])
			require.Equal(t, want, mergeTestStream(t, nil, stream[0], stream[1]))
		})
	}
}

// A record relocated before the lock with a number the commit doesn't decide
// is relocated again under the lock, so the cache ends as if nothing had been
// relocated before it.
func TestMergeValuesRelocatesAgainWhenANumberChanged(t *testing.T) {
	t.Parallel()
	for name, stream := range mergeTestMovingStreams(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, stream[0], stream[1])
			var under []uint64
			got := mergeTestStream(t, func(c *Cache) {
				c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
					for _, numbers := range used {
						for provisional := range numbers {
							numbers[provisional] += 1000
						}
					}
				}
				c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
			}, stream[0], stream[1])
			require.Equal(t, want, got)
			require.Len(t, under, 1, "the record whose numbers no longer hold is relocated under the lock")
		})
	}
}

// An engine relocates a record whose references move to its final numbers
// as it prepares it, so the record's payload is decoded once in the merge,
// and not again to relocate it before the commit, nor under the lock.
func TestMergeValuesDecodesAMovedPayloadOnce(t *testing.T) {
	// Not parallel: the codec's step hook is the package's.
	child, root := mergeTestMovingPayloadStream(t)
	ctx, c, _ := transferTestCache(t)
	_, err := c.MergeValues(ctx, cloudCacheID, child)
	require.NoError(t, err)
	var mu sync.Mutex
	visits := 0
	steps := func(step string, v PersistedPayloadVisit) {
		if step == "visit" && bytes.Contains(v.Payload, []byte("moved-payload-root")) {
			mu.Lock()
			defer mu.Unlock()
			visits++
		}
	}
	transferTestCodecSteps.Store(&steps)
	defer transferTestCodecSteps.Store(nil)
	var moved, under []uint64
	c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
		for ordinal := range used {
			moved = append(moved, ordinal)
		}
	}
	c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	reply, err := c.MergeValues(ctx, cloudCacheID, root)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Len(t, moved, 1, "the root's reference moves")
	require.Empty(t, under, "nothing is relocated under the lock")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, visits, "the root's payload is visited once")
}

// A merge renumbers its records as it is prepared, outside its caller's
// lock, which on the Cloud is the remote cache service's work lock, and the
// commit uses them: here the root's record is relocated before the commit
// begins, and nothing is relocated under the graph lock.
func TestPreparedMergeRelocatesBeforeItsCommit(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	ctx, c, _ := transferTestCache(t)
	_, err := c.MergeValues(ctx, cloudCacheID, child)
	require.NoError(t, err)
	committing := false
	var relocatedWhileCommitting []bool
	var under []uint64
	c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
		if len(used) > 0 {
			relocatedWhileCommitting = append(relocatedWhileCommitting, committing)
		}
	}
	c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	p, err := c.PrepareValueMerge(ctx, cloudCacheID, root)
	require.NoError(t, err)
	committing = true
	reply, err := c.CommitValueMerge(ctx, p)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Equal(t, []bool{false}, relocatedWhileCommitting, "relocated once, in the prepare")
	require.Empty(t, under, "no record is relocated under the graph lock")
}

// mergeTestEntryNames names each of a cache's entries by its recipes, which
// two caches share where their merges reserved numbers in another order.
// Requires egraphMu.
func mergeTestEntryNames(c *Cache) map[uint64]string {
	names := make(map[uint64]string, len(c.resultsByID))
	for id, res := range c.resultsByID {
		keys := make([]string, 0, len(res.recipeKeys))
		for _, key := range res.recipeKeys {
			keys = append(keys, key.String())
		}
		slices.Sort(keys)
		names[uint64(id)] = strings.Join(keys, ",")
	}
	return names
}

// mergeTestStateByRecipe is a cache's entries as merge leaves them, as
// mergeTestState gives them, with each entry named by its recipes in place
// of its number: as itself, in its record's references and in its
// dependencies.
func mergeTestStateByRecipe(t *testing.T, c *Cache) []string {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	names := mergeTestEntryNames(c)
	sorted := slices.Sorted(func(yield func(string) bool) {
		for _, name := range names {
			if !yield(name) {
				return
			}
		}
	})
	place := map[string]uint64{}
	for i, name := range sorted {
		place[name] = uint64(i + 1)
	}
	var out []string
	for id, res := range c.resultsByID {
		var deps []string
		for dep := range res.deps {
			deps = append(deps, names[uint64(dep)])
		}
		slices.Sort(deps)
		record := "none"
		if res.persistedEnvelope != nil {
			rec, err := VisitEncodedReferences(PersistedRecord{ResultID: uint64(id), Envelope: *res.persistedEnvelope, Call: res.loadResultCall()}, func(ref *PersistedRef) error {
				if ref.RecipeID == nil && ref.ResultID != 0 {
					ref.ResultID = place[names[ref.ResultID]]
				}
				return nil
			})
			require.NoError(t, err)
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			record = string(raw)
		}
		out = append(out, fmt.Sprintf("%s deps=%v record=%s", names[uint64(id)], deps, record))
	}
	slices.Sort(out)
	return out
}

// Two whole sets that overtake each other land as they do one after the
// other. An engine relocates the root's record to its final numbers as it
// prepares it, by a decision made while the shared row had no entry; another
// restore then creates that entry, so the root's reference to it moves
// again. The root is relocated again before its commit takes the lock when
// the other restore committed during its preparation, and under the lock
// when it committed after. Either way the same entries hold the same
// records, and the reply says the same of each record and root: the entry
// of its recipe, that entry's dependencies, replacements, expiry and parts,
// and each root's retention. Entry numbers can differ with the order the
// merges reserved them in, so entries are compared by their recipes, and a
// retention's expiry, which is read off the clock, by whether it is set.
func TestRestoreValuesOvertakenAsPreparedLandsTheSame(t *testing.T) {
	t.Parallel()
	sctx, source, srv := transferTestCache(t)
	kept := persistedListTestResult(t, sctx, source, srv, "overtaken-kept", String("kept"))
	shared := persistedListTestResult(t, sctx, source, srv, "overtaken-shared", String("shared"))
	root := persistedListTestResult(t, sctx, source, srv, "overtaken-root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{kept, shared}})
	keptBundle := exportTestBundle(t, sctx, source, kept)
	sharedBundle := exportTestBundle(t, sctx, source, shared)
	rootBundle := exportTestBundle(t, sctx, source, root)
	require.Len(t, rootBundle.Values, 3)

	type landing struct {
		state []string
		reply []string
		rows  MergeRowCounts
		under int
	}
	land := func(t *testing.T, order string) landing {
		ctx, c, _ := transferTestCache(t)
		var under atomic.Int64
		c.testRelocatedUnderLock = func(uint64) { under.Add(1) }
		restore := func(bundle ValueBundle) MergeReply {
			reply, err := c.RestoreValues(ctx, bundle, nil)
			require.NoError(t, err)
			require.True(t, reply.Committed)
			t.Cleanup(func() { require.NoError(t, reply.Release(context.Background())) })
			return reply
		}
		restore(keptBundle)
		var reply MergeReply
		switch order {
		case "one after the other":
			restore(sharedBundle)
			reply = restore(rootBundle)
		case "overtaken as it prepares":
			var overtaken atomic.Bool
			c.testRecordPayloadPreparing = func(uint64) error {
				if overtaken.CompareAndSwap(false, true) {
					restore(sharedBundle)
				}
				return nil
			}
			reply = restore(rootBundle)
			c.testRecordPayloadPreparing = nil
			require.True(t, overtaken.Load())
		case "overtaken before its commit":
			p, err := c.PrepareValueMerge(ctx, cloudCacheID, rootBundle)
			require.NoError(t, err)
			p.restore, p.hold = true, true
			restore(sharedBundle)
			reply, err = c.CommitValueMerge(ctx, p)
			require.NoError(t, err)
			require.True(t, reply.Committed)
			t.Cleanup(func() { require.NoError(t, reply.Release(context.Background())) })
		}
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
		return landing{state: mergeTestStateByRecipe(t, c), reply: landed, rows: reply.Rows, under: int(under.Load())}
	}
	want := land(t, "one after the other")
	require.Len(t, want.reply, 4, "three records and the root")
	require.Zero(t, want.under)
	for order, under := range map[string]int{"overtaken as it prepares": 0, "overtaken before its commit": 1} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			got := land(t, order)
			require.Equal(t, want.state, got.state, "the same entries hold the same records")
			require.Equal(t, want.reply, got.reply, "the reply says the same of each record and root")
			require.Equal(t, want.rows, got.rows)
			require.Equal(t, under, got.under, "records relocated again under the lock")
		})
	}
}
