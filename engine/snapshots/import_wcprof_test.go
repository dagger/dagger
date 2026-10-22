package snapshots_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/stretchr/testify/require"
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

func profiledOp(ctx context.Context, class string) (context.Context, *wcprof.Op) {
	wcprof.EnsureRecorder()
	return wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindRemoteCache, class, wcprof.OpOpts{})
}

// A profiled chain import records each fetched layer's fetch and apply, with
// the blob's digest and size, and its wait for the layer's import lock. A
// layer whose snapshot is reused is neither fetched nor applied.
func TestImportChainProfiled(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "profiled prefix")
	ab, _ := producer.Build(t, a, "b.txt", "profiled suffix")
	chain := exportChain(t, ab)
	require.Len(t, chain.Layers, 2)

	ctx, root := profiledOp(context.Background(), "test.import")
	for range 2 {
		ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, chain.Provider))
		require.NoError(t, err)
		require.NoError(t, ref.Release(context.Background()))
	}
	root.End(wcprof.OutcomeOK)

	type phase struct{ class, ident string }
	var ops []phase
	byPhase := map[phase]profiledEvent{}
	var lockWaits int
	for _, ev := range profiledUnder(t, root.ID()) {
		switch ev.Type {
		case "op":
			p := phase{ev.class, ev.ident}
			ops = append(ops, p)
			byPhase[p] = ev
			require.Equal(t, root.ID(), ev.ParentID, "%s is a child of the import's op", ev.class)
			require.Equal(t, "ok", ev.Outcome, ev.class)
		case "wait":
			require.Equal(t, "lock", ev.Reason)
			require.Equal(t, "snapshots.importLayer", ev.ident)
			require.LessOrEqual(t, ev.StartNS, ev.EndNS)
			lockWaits++
		}
	}
	var want []phase
	for _, layer := range chain.Layers {
		dgst := layer.Descriptor.Digest.String()
		want = append(want, phase{"snapshot.import.fetch[download]", dgst}, phase{"snapshot.import.apply", dgst})
	}
	require.ElementsMatch(t, want, ops, "the second import reuses both layers")
	require.Equal(t, 4, lockWaits, "one lock wait per layer per import")
	for _, layer := range chain.Layers {
		dgst := layer.Descriptor.Digest.String()
		fetch, apply := byPhase[phase{"snapshot.import.fetch[download]", dgst}], byPhase[phase{"snapshot.import.apply", dgst}]
		require.Equal(t, layer.Descriptor.Size, fetch.Bytes)
		require.Equal(t, layer.Descriptor.Size, apply.Bytes)
		require.Equal(t, "external", fetch.WorkType)
		require.Equal(t, "engine", apply.WorkType)
		require.LessOrEqual(t, fetch.EndNS, apply.StartNS, "a layer is fetched before it is applied")
	}
}
