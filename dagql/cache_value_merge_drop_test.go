package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"
)

// mergeTestDropStreams are bundle streams that exercise what a prepared merge
// lets go of: a record renumbered before its commit (a root whose child
// landed first), a root sent with an offer as the Cloud sends a part (an
// output with its owner), which an engine keeps and the Cloud drops, and an
// engine's root sent above a held row, as the Cloud receives one.
func mergeTestDropStreams(t *testing.T) map[string][]ValueBundle {
	t.Helper()
	child, root := mergeTestMovingStream(t)
	heldChild, heldRoot := mergeTestHeldStream(t)
	ctx, source, srv := transferTestCache(t)
	owner := persistedListTestResult(t, ctx, source, srv, "drop-offer-owner", String("owner"))
	offered := persistedListTestResult(t, ctx, source, srv, "drop-offered-root", &transferTestValue{Text: "offered"})
	mergeTestOfferOn(t, ctx, source, offered, "/offered", owner)
	withOffer := cloudTestBundle(t, ctx, source, offered)
	require.Len(t, withOffer.Values, 2, "the offered root and its offer's owner")
	require.Len(t, withOffer.Outputs, 1, "the offered root goes with its offer")
	return map[string][]ValueBundle{
		"moved":   {child, root},
		"offered": {withOffer},
		"held":    {heldChild, heldRoot},
	}
}

// mergeTestHeldStream is an engine's export of a child, and then of a root
// above it that sends the child as a held row, as an engine sends a row the
// Cloud already holds.
func mergeTestHeldStream(t *testing.T) (child, root ValueBundle) {
	t.Helper()
	ctx, engine, srv := transferTestCache(t)
	c := persistedListTestResult(t, ctx, engine, srv, "drop-held-child", String("child"))
	r := persistedListTestResult(t, ctx, engine, srv, "drop-held-root", String("root"))
	transferTestDependency(engine, ctx, r, c)
	child = exportTestBundle(t, ctx, engine, c)
	root = engineHeldTestBundle(t, engine, exportTestBundle(t, ctx, engine, r), c)
	require.Len(t, root.Values, 2, "the root and its held child")
	return child, root
}

// mergeTestSentOffers counts the offers the bundle sends for the row at
// ordinal: its record's and its outputs with chains, which an engine embeds
// in the record as offers (embedMergedParts).
func mergeTestSentOffers(bundle ValueBundle, ordinal TransferOrdinal) int {
	n := len(mergeTestBundleRow(bundle, ordinal).Record.Envelope.PendingOffers)
	for _, output := range bundle.Outputs {
		if output.Ordinal == ordinal && output.Chain != nil {
			n++
		}
	}
	return n
}

// mergeTestDropSkip reports a stream a kind of cache doesn't merge: the
// Cloud drops offers and refuses the offered stream, whose offer's owner is
// reachable only through the offer (TestMergeValuesCloudChecksClosureAfterDroppingParts),
// and only the Cloud merges an engine's held rows.
func mergeTestDropSkip(cacheName, streamName string) bool {
	return cacheName == "cloud" && streamName == "offered" || cacheName == "engine" && streamName == "held"
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
							require.Equal(t, mergeTestSentOffers(bundle, row.Ordinal), len(row.Record.Envelope.PendingOffers),
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

// A prepared merge waiting for its commit holds nothing that only its
// preparation read: no early decision of its rows, no record for a held row,
// and nothing of the private cache it derived identities in, which a
// collection then frees. The commit reads each row's plan and record as
// prepared, and commits.
func TestPreparedMergeHoldsNoPrivateCache(t *testing.T) {
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
					var private []weak.Pointer[sharedResult]
					c.testPrivateMergeCache = func(p *Cache) {
						for _, res := range p.resultsByID {
							private = append(private, weak.Make(res))
						}
					}
					p, err := c.PrepareValueMerge(ctx, from, bundle)
					require.NoError(t, err)
					require.NotEmpty(t, private)
					require.Nil(t, p.prep.early, "the early decision of the rows is the preparation's")
					for id := range p.prep.held {
						_, ok := p.prep.records[id]
						require.False(t, ok, "held row %d keeps no record", id)
					}
					runtime.GC()
					for _, entry := range private {
						require.Nil(t, entry.Value(), "the prepared merge holds no entry of its private cache")
					}
					reply, err := c.CommitValueMerge(ctx, p)
					require.NoError(t, err)
					require.True(t, reply.Committed)
					require.Empty(t, reply.Stale)
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
		for _, dep := range res.deps {
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
