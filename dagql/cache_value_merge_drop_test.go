package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeTestDropStreams are bundle streams that exercise what a prepared merge
// lets go of: a record renumbered before its commit (a root whose child
// landed first), and a root whose record carries an offer, which an engine
// keeps and the Cloud drops.
func mergeTestDropStreams(t *testing.T) map[string][]ValueBundle {
	t.Helper()
	child, root := mergeTestMovingStream(t)
	ctx, source, srv := transferTestCache(t)
	owner := persistedListTestResult(t, ctx, source, srv, "drop-offer-owner", String("owner"))
	offered := persistedListTestResult(t, ctx, source, srv, "drop-offered-root", &transferTestValue{Text: "offered"})
	mergeTestOfferOn(t, ctx, source, offered, "/offered", owner)
	withOffer := exportTestBundle(t, ctx, source, offered)
	var offers int
	for _, row := range withOffer.Values {
		offers += len(row.Record.Envelope.PendingOffers)
	}
	require.Equal(t, 1, offers, "the offered root's record carries its offer")
	return map[string][]ValueBundle{
		"moved":   {child, root},
		"offered": {withOffer},
	}
}

// mergeTestDropSkip reports a stream a kind of cache doesn't merge: the
// Cloud drops offers and refuses the offered stream, whose offer's owner is
// reachable only through the offer (TestMergeValuesCloudChecksClosureAfterDroppingParts).
func mergeTestDropSkip(cacheName, streamName string) bool {
	return cacheName == "cloud" && streamName == "offered"
}

// mergeTestDropCaches are the two kinds of cache that merge: an engine, from
// the Cloud, and the Cloud, from an engine.
var mergeTestDropCaches = map[string]func(t *testing.T) (context.Context, *Cache, CacheID){
	"engine": func(t *testing.T) (context.Context, *Cache, CacheID) {
		ctx, c, _ := transferTestCache(t)
		return ctx, c, cloudCacheID
	},
	"cloud": func(t *testing.T) (context.Context, *Cache, CacheID) {
		ctx, c := storedPartTestCache(t, WithBlobStore())
		return ctx, c, "sender"
	},
}

// A prepared merge waiting for its commit holds no copy of its bundle's
// payloads or call frames: of each row, only its own fields and its offers,
// which the commit's closure reads, and of each record's entry in the
// private cache it derived identities in, no envelope. The commit reads each
// record as prepared, and commits.
func TestPreparedMergeHoldsNoCopyOfItsRecords(t *testing.T) {
	t.Parallel()
	for cacheName, newCache := range mergeTestDropCaches {
		for streamName, bundles := range mergeTestDropStreams(t) {
			if mergeTestDropSkip(cacheName, streamName) {
				continue
			}
			t.Run(cacheName+"/"+streamName, func(t *testing.T) {
				t.Parallel()
				ctx, c, from := newCache(t)
				for _, bundle := range bundles {
					p, err := c.PrepareValueMerge(ctx, from, bundle)
					require.NoError(t, err)
					require.NotEmpty(t, p.prep.bundle.Values)
					for _, row := range p.prep.bundle.Values {
						require.Equal(t, PersistedRecord{Envelope: PersistedResultEnvelope{PendingOffers: row.Record.Envelope.PendingOffers}}, row.Record,
							"row %d holds no copy of its record", row.Ordinal)
						if !c.blobBacked {
							require.Equal(t, len(mergeTestBundleRow(bundle, row.Ordinal).Record.Envelope.PendingOffers), len(row.Record.Envelope.PendingOffers),
								"an engine's row keeps its offers")
						}
					}
					for id, plan := range p.prep.plans {
						if p.prep.held[id] {
							continue
						}
						require.Nil(t, plan.row.persistedEnvelope, "row %d's private entry holds no envelope", id)
						require.NotNil(t, plan.row.loadResultCall(), "row %d's private entry keeps its frame", id)
					}
					reply, err := c.CommitValueMerge(ctx, p)
					require.NoError(t, err)
					require.True(t, reply.Committed)
				}
			})
		}
	}
}

func mergeTestBundleRow(bundle ValueBundle, ordinal TransferOrdinal) TransferredValue {
	for _, row := range bundle.Values {
		if row.Ordinal == ordinal {
			return row
		}
	}
	return TransferredValue{}
}

// A merge whose prepared merge let go of its record copies commits the same
// as one that kept them: the same reply, and the same entries, record for
// record, on an engine and on the Cloud.
func TestPreparedMergeCommitsTheSameWithoutItsRecordCopies(t *testing.T) {
	t.Parallel()
	for cacheName, newCache := range mergeTestDropCaches {
		for streamName, bundles := range mergeTestDropStreams(t) {
			if mergeTestDropSkip(cacheName, streamName) {
				continue
			}
			t.Run(cacheName+"/"+streamName, func(t *testing.T) {
				t.Parallel()
				merge := func(keep bool) ([]MergeReply, []string) {
					ctx, c, from := newCache(t)
					c.testKeepRecordCopies = keep
					var replies []MergeReply
					for _, bundle := range bundles {
						p, err := c.PrepareValueMerge(ctx, from, bundle)
						require.NoError(t, err)
						reply, err := c.CommitValueMerge(ctx, p)
						require.NoError(t, err)
						require.True(t, reply.Committed)
						replies = append(replies, MergeReply{Values: reply.Values, Roots: reply.Roots, Stale: reply.Stale, Rows: reply.Rows})
					}
					return replies, mergeTestWholeState(t, c)
				}
				keptReplies, keptState := merge(true)
				droppedReplies, droppedState := merge(false)
				require.NotEmpty(t, keptState)
				require.Equal(t, keptReplies, droppedReplies)
				require.Equal(t, keptState, droppedState)
			})
		}
	}
}

// A caller whose preparation keeps no light record's input, the Cloud's or
// an engine's with none, may let its input go once PrepareValueMerge
// returns: the commit is the same, reply and entries record for record, as
// when the caller keeps it, even with every byte of the input's payloads
// overwritten. On an engine the moved stream's child is kept, a light
// record, so the engine merges the offered stream alone.
func TestPreparedMergeCommitsTheSameWithItsInputLetGo(t *testing.T) {
	t.Parallel()
	for cacheName, newCache := range mergeTestDropCaches {
		for streamName, bundles := range mergeTestDropStreams(t) {
			if mergeTestDropSkip(cacheName, streamName) || cacheName == "engine" && streamName == "moved" {
				continue
			}
			t.Run(cacheName+"/"+streamName, func(t *testing.T) {
				t.Parallel()
				merge := func(letGo bool) ([]MergeReply, []string) {
					ctx, c, from := newCache(t)
					var replies []MergeReply
					for _, bundle := range bundles {
						input, err := cloneValueBundle(bundle)
						require.NoError(t, err)
						p, err := c.PrepareValueMerge(ctx, from, input)
						require.NoError(t, err)
						require.Nil(t, p.input.Values, "no light record keeps the input")
						if letGo {
							for i := range input.Values {
								mergeTestOverwritePayloads(&input.Values[i].Record.Envelope)
								input.Values[i].Record = PersistedRecord{}
							}
						}
						reply, err := c.CommitValueMerge(ctx, p)
						require.NoError(t, err)
						require.True(t, reply.Committed)
						replies = append(replies, MergeReply{Values: reply.Values, Roots: reply.Roots, Stale: reply.Stale, Rows: reply.Rows})
					}
					return replies, mergeTestWholeState(t, c)
				}
				keptReplies, keptState := merge(false)
				goneReplies, goneState := merge(true)
				require.NotEmpty(t, keptState)
				require.Equal(t, keptReplies, goneReplies)
				require.Equal(t, keptState, goneState)
			})
		}
	}
}

// mergeTestOverwritePayloads overwrites every byte of an envelope's payloads,
// its items' included.
func mergeTestOverwritePayloads(env *PersistedResultEnvelope) {
	for _, payload := range [][]byte{env.ObjectJSON, env.ScalarJSON} {
		for j := range payload {
			payload[j] = 'x'
		}
	}
	for i := range env.Items {
		mergeTestOverwritePayloads(&env.Items[i])
	}
}

// mergeTestWholeState is a cache's entries as merge leaves them, however
// each holds its record: each entry's number, dependencies, call frame and
// whole envelope, decoded when held compressed (the Cloud's stored bodies),
// in number order.
func mergeTestWholeState(t *testing.T, c *Cache) []string {
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
		frame, err := res.entryFrameLocked().decode()
		require.NoError(t, err)
		call, err := json.Marshal(frame)
		require.NoError(t, err)
		env, err := res.loadPayloadState().wholeEnvelope(uint64(id))
		require.NoError(t, err)
		envelope, err := json.Marshal(env)
		require.NoError(t, err)
		out = append(out, fmt.Sprintf("%d deps=%v call=%s envelope=%s", id, deps, call, envelope))
	}
	slices.Sort(out)
	return out
}
