package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// profiledEvent is a dumped event with its strings resolved.
type profiledEvent struct {
	wcprof.DumpEvent
	class, ident string
}

// profiledUnder returns the dumped events recorded under the op root: its
// descendant ops, and the waits and links from them.
func profiledUnder(t *testing.T, root uint64) []profiledEvent {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	parents := map[uint64]uint64{}
	for _, ev := range events {
		if ev.Type == "op" {
			parents[ev.OpID] = ev.ParentID
		}
	}
	under := func(id uint64) bool {
		for ; id != 0; id = parents[id] {
			if id == root {
				return true
			}
		}
		return false
	}
	var out []profiledEvent
	for _, ev := range events {
		id := ev.ParentID
		if ev.Type == "op" {
			id = ev.OpID
		}
		if ev.OpID == root || !under(id) {
			continue
		}
		out = append(out, profiledEvent{DumpEvent: ev, class: header.Strings[ev.ClassID], ident: header.Strings[ev.IdentID]})
	}
	return out
}

// A profiled export records where the adapter's time went: holding the
// entries, then the capture, whose children are the graph lock's wait, the
// walk under it, the records' copy, and the chains' opening, one op per
// exported part with its layers' diffs under it. The capture links each
// output's part to its result with the part's size, its chain's.
func TestRemoteCacheAdapterExportProfiled(t *testing.T) {
	e := newAdapterTestEngine(t)
	ready := e.file(t, "ready")
	require.NoError(t, e.cache.Evaluate(e.ctx, ready))
	number := entryNumber(t, ready)
	req := protocol.Export{Roots: []uint64{number}, Outputs: []protocol.ExportOutput{{Number: number, Address: snapshotPart}}}

	wcprof.EnsureRecorder()
	ctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(e.ctx), wcprof.OpKindRemoteCache, "test.export", wcprof.OpOpts{})
	var size int64
	var consumeOp uint64
	require.NoError(t, e.adapter.Export(ctx, req, func(ctx context.Context, exp remotecache.Export) error {
		require.Len(t, exp.Bundle.Outputs, 1)
		for _, layer := range exp.Bundle.Outputs[0].Chain.Layers {
			size += layer.Descriptor.Size
		}
		consumeOp = wcprof.CurrentOpID(ctx)
		return nil
	}))
	root.End(wcprof.OutcomeOK)
	require.Positive(t, size)

	events := profiledUnder(t, root.ID())
	byClass := map[string][]profiledEvent{}
	ids := map[uint64]profiledEvent{}
	for _, ev := range events {
		if ev.Type == "op" {
			byClass[ev.class] = append(byClass[ev.class], ev)
			ids[ev.OpID] = ev
		}
	}
	for _, class := range []string{"remotecache.export.hold", "remotecache.export.capture"} {
		require.Len(t, byClass[class], 1, class)
		require.Equal(t, root.ID(), byClass[class][0].ParentID, class)
		require.Equal(t, "remote_cache", byClass[class][0].OpKind, class)
	}
	capture := byClass["remotecache.export.capture"][0]
	require.Equal(t, "ok", capture.Outcome)
	require.LessOrEqual(t, byClass["remotecache.export.hold"][0].EndNS, capture.StartNS)
	require.Equal(t, root.ID(), consumeOp, "the bundle's consumer runs under the export, not the capture")

	for _, class := range []string{"remotecache.export.walk", "remotecache.export.copy", "remotecache.export.chains"} {
		require.Len(t, byClass[class], 1, class)
		require.Equal(t, capture.OpID, byClass[class][0].ParentID, class)
	}
	var lockWaits int
	for _, ev := range events {
		if ev.Type == "wait" && ev.ParentID == capture.OpID && ev.ident == "dagql.Cache.egraphMu" {
			lockWaits++
		}
	}
	require.Equal(t, 1, lockWaits, "the walk's wait for the graph lock")

	key, err := json.Marshal(snapshotPart)
	require.NoError(t, err)
	chains := byClass["remotecache.export.chain"]
	require.Len(t, chains, 1)
	chain := chains[0]
	require.Equal(t, byClass["remotecache.export.chains"][0].OpID, chain.ParentID)
	require.Equal(t, string(key), chain.ident)
	require.Equal(t, number, chain.ResultID)
	require.Equal(t, size, chain.Bytes, "a fresh snapshot's layers are all diffed now")
	var diffs int
	for _, ev := range byClass["snapshot.export.diff"] {
		require.Equal(t, chain.OpID, ev.ParentID)
		diffs++
	}
	require.Positive(t, diffs)

	var parts []profiledEvent
	for _, ev := range events {
		if ev.Type == "link" && ev.LinkKind == "part" {
			parts = append(parts, ev)
		}
	}
	require.Len(t, parts, 1)
	require.Equal(t, capture.OpID, parts[0].ParentID)
	require.Equal(t, string(key), parts[0].ident)
	require.Equal(t, number, parts[0].ResultID)
	require.Equal(t, size, parts[0].Bytes)
}

// A profiled merge is one op.
func TestRemoteCacheAdapterMergeProfiled(t *testing.T) {
	source := newGCTestCache(t)
	sctx, res := addGCTestPersistableResult(t, source, "source", "merged", dagql.NewInt(7))
	var bundle dagql.ValueBundle
	require.NoError(t, source.WithExportedValues(sctx, dagql.ValueSelection{Roots: []dagql.AnyResult{res}}, config.RefConfig{}, func(_ context.Context, exp *dagql.ExportedValues) error {
		bundle = exp.Bundle
		return nil
	}))
	adapter := newTestRemoteCacheAdapter(t, newGCTestCache(t))

	wcprof.EnsureRecorder()
	ctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(t.Context()), wcprof.OpKindRemoteCache, "test.merge", wcprof.OpOpts{})
	_, err := adapter.Merge(ctx, protocol.Merge{Bundle: bundle})
	require.NoError(t, err)
	root.End(wcprof.OutcomeOK)

	var merges []profiledEvent
	for _, ev := range profiledUnder(t, root.ID()) {
		if ev.Type == "op" && ev.class == "remotecache.merge" {
			merges = append(merges, ev)
		}
	}
	require.Len(t, merges, 1)
	require.Equal(t, "ok", merges[0].Outcome)
	require.Equal(t, "remote_cache", merges[0].OpKind)
	require.Equal(t, root.ID(), merges[0].ParentID)
}

// A profiled offer is one op per offered part, naming the part and the row
// its address resolved to, with an offer.<outcome> fact; a gone entry has
// none.
func TestRemoteCacheAdapterOfferProfiled(t *testing.T) {
	e := newAdapterTestEngine(t)
	complete := e.file(t, "complete")
	require.NoError(t, e.cache.Evaluate(e.ctx, complete))
	pending, _ := e.publishFile(t, "pending", complete)
	other := testutil.NewStore(t)
	ref, _ := other.Build(t, nil, "offered.txt", "offered bytes")
	chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	spec := adapterTestPlatform.Spec()
	offer := protocol.CloudPart{
		Offer:       dagql.PersistedPartOffer{Address: snapshotPart, Value: dagql.SnapshotValue{Kind: "file", Path: "/offered.txt", Platform: &spec}, Chain: dagql.OfferedChain{Layers: chain.Layers, RenewalKey: "7/snapshot"}},
		CloudNumber: 7,
	}

	wcprof.EnsureRecorder()
	ctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(e.ctx), wcprof.OpKindRemoteCache, "test.offer", wcprof.OpOpts{})
	_, err = e.adapter.OfferParts(ctx, protocol.Offer{Items: []protocol.OfferItem{
		{Number: entryNumber(t, pending), Offers: []protocol.CloudPart{offer}},
		{Number: entryNumber(t, complete), Offers: []protocol.CloudPart{offer}},
		{Number: goneNumber, Offers: []protocol.CloudPart{offer}},
	}})
	require.NoError(t, err)
	root.End(wcprof.OutcomeOK)

	key, err := json.Marshal(snapshotPart)
	require.NoError(t, err)
	events := profiledUnder(t, root.ID())
	facts := map[uint64]string{}
	for _, ev := range events {
		if ev.Type == "link" && ev.LinkKind == "fact" {
			facts[ev.ParentID] = ev.ident
		}
	}
	got := map[uint64]string{}
	for _, ev := range events {
		if ev.Type != "op" || ev.class != "remotecache.offer" {
			continue
		}
		require.Equal(t, string(key), ev.ident)
		got[ev.ResultID] = facts[ev.OpID]
	}
	require.Equal(t, map[uint64]string{
		entryNumber(t, pending):  "offer." + string(protocol.OfferAccepted),
		entryNumber(t, complete): "offer." + string(protocol.OfferAlreadyComplete),
	}, got)
}
