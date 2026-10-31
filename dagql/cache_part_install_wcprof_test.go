package dagql

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/wcprof"
)

// profiledEvent is a dumped event with its strings resolved.
type profiledEvent struct {
	wcprof.DumpEvent
	class, ident string
}

// profiledRoot starts a recorded op for a test to run under.
func profiledRoot(ctx context.Context, class string) (context.Context, *wcprof.Op) {
	wcprof.EnsureRecorder()
	return wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindSessionPhase, class, wcprof.OpOpts{})
}

// profiledUnder returns the dumped events recorded under the op root: its
// descendant ops, and the waits and links from them, and maps each op's ID
// to its event.
func profiledUnder(t *testing.T, root uint64) ([]profiledEvent, map[uint64]profiledEvent) {
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
	ops := map[uint64]profiledEvent{}
	for _, ev := range events {
		id := ev.ParentID
		if ev.Type == "op" {
			id = ev.OpID
		}
		if ev.OpID == root || !under(id) {
			continue
		}
		pe := profiledEvent{DumpEvent: ev, class: header.Strings[ev.ClassID], ident: header.Strings[ev.IdentID]}
		out = append(out, pe)
		if ev.Type == "op" {
			ops[ev.OpID] = pe
		}
	}
	return out, ops
}

func profiledOps(events []profiledEvent, class string) []profiledEvent {
	var out []profiledEvent
	for _, ev := range events {
		if ev.Type == "op" && ev.class == class {
			out = append(out, ev)
		}
	}
	return out
}

func profiledLinks(events []profiledEvent, kind string, from uint64) []profiledEvent {
	var out []profiledEvent
	for _, ev := range events {
		if ev.Type == "link" && ev.LinkKind == kind && ev.ParentID == from {
			out = append(out, ev)
		}
	}
	return out
}

// A part installed from an offer is one remote_cache op under the task that
// installed it, naming the receiving result and the part, with the bytes
// downloaded. It links the part's size, the whole chain's, for pricing a
// pull; its children are the layers' fetches and applies, then the commit,
// whose own steps nest under it. The task's lazy op names the task.
func TestOfferInstallProfiled(t *testing.T) {
	chain := newChainFixture(t, "install-prof")
	f := newExhaustionFixture(t, chain)
	f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
	layers := chain.chain.Layers
	held, err := f.cache.snapshotManager.ImportChain(f.ctx, &snapshots.ExportChain{Layers: layers[:1], Provider: chain.chain.Provider})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, held.Release(context.Background())) })
	var total int64
	for _, layer := range layers {
		total += layer.Descriptor.Size
	}

	ctx, root := profiledRoot(f.ctx, "test.demand")
	f.ctx = ctx
	require.NoError(t, f.run())
	require.True(t, f.installed())
	root.End(wcprof.OutcomeOK)

	events, ops := profiledUnder(t, root.ID())
	installs := profiledOps(events, "remotecache.install")
	require.Len(t, installs, 1)
	install := installs[0]
	key, err := partAddressKey(f.address)
	require.NoError(t, err)
	receiver := uint64(f.receiver.cacheSharedResult().id)
	require.Equal(t, "remote_cache", install.OpKind)
	require.Equal(t, "external", install.WorkType)
	require.Equal(t, "ok", install.Outcome)
	require.Equal(t, key, install.ident)
	require.Equal(t, receiver, install.ResultID)
	require.Equal(t, layers[1].Descriptor.Size, install.Bytes, "only the upper layer is downloaded")

	parts := profiledLinks(events, "part", install.OpID)
	require.Len(t, parts, 1)
	require.Equal(t, key, parts[0].ident)
	require.Equal(t, receiver, parts[0].ResultID)
	require.Equal(t, total, parts[0].Bytes, "the part's size is its whole chain's")

	var children []string
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == install.OpID && !snapshotterCall(ev) {
			children = append(children, ev.class)
			require.GreaterOrEqual(t, ev.StartNS, install.StartNS, ev.class)
			require.LessOrEqual(t, ev.EndNS, install.EndNS, ev.class)
		}
	}
	require.Equal(t, []string{"snapshot.import.fetch[download]", "snapshot.import.apply", "remotecache.install.commit"}, sortedByStart(events, install.OpID))
	require.Len(t, children, 3)

	// The commit's own steps nest under it: preparing the part, with the
	// receiver's probe and the source snapshot's pin, then publishing it.
	commits := profiledOps(events, "remotecache.install.commit")
	require.Len(t, commits, 1)
	commit := commits[0]
	require.Equal(t, install.OpID, commit.ParentID)
	require.Equal(t, []string{"remotecache.install.prepare", "remotecache.install.publish"}, sortedByStart(events, commit.OpID))
	prepares := profiledOps(events, "remotecache.install.prepare")
	require.Len(t, prepares, 1)
	require.Equal(t, []string{"remotecache.install.probe", "remotecache.install.pin"}, sortedByStart(events, prepares[0].OpID))
	for _, ev := range events {
		if ev.Type == "op" && strings.HasPrefix(ev.class, "remotecache.install.") && ev.class != "remotecache.install.commit" {
			require.Equal(t, "internal", ev.OpKind, ev.class)
			require.GreaterOrEqual(t, ev.StartNS, commit.StartNS, ev.class)
			require.LessOrEqual(t, ev.EndNS, commit.EndNS, ev.class)
		}
	}

	lazy, ok := ops[install.ParentID]
	require.True(t, ok, "the install's parent is recorded")
	require.Equal(t, "lazy", lazy.OpKind)
	require.True(t, strings.HasPrefix(lazy.ident, "lazy:"), "the task's lazy op names its task: %q", lazy.ident)

	// After the body, the task's bookkeeping: each step with work is an
	// internal op of the task's, in order.
	var steps []string
	last := install.EndNS
	for _, class := range sortedByStart(events, lazy.OpID) {
		if !strings.HasPrefix(class, "lazyBody.") {
			continue
		}
		steps = append(steps, class)
		step := profiledOps(events, class)
		require.Len(t, step, 1, class)
		require.Equal(t, lazy.OpID, step[0].ParentID, class)
		require.Equal(t, "internal", step[0].OpKind, class)
		require.Equal(t, "ok", step[0].Outcome, class)
		require.LessOrEqual(t, last, step[0].StartNS, class)
		last = step[0].EndNS
	}
	require.Subset(t, steps, []string{"lazyBody.ownerSync", "lazyBody.settle", "lazyBody.releaseOperationLease", "lazyBody.teachContentIdentity"})
}

// sortedByStart returns the classes of parent's child ops in start order.
// snapshotterCall reports whether ev is the profile of a containerd
// snapshotter call (prepare, mounts, commit...), which these tests leave out:
// they check the install's own phases.
func snapshotterCall(ev profiledEvent) bool {
	return ev.OpKind == "internal" && strings.HasPrefix(ev.class, "snapshot.")
}

func sortedByStart(events []profiledEvent, parent uint64) []string {
	var children []profiledEvent
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == parent && !snapshotterCall(ev) {
			children = append(children, ev)
		}
	}
	for i := 1; i < len(children); i++ {
		for j := i; j > 0 && children[j].StartNS < children[j-1].StartNS; j-- {
			children[j], children[j-1] = children[j-1], children[j]
		}
	}
	classes := make([]string, len(children))
	for i, ev := range children {
		classes[i] = ev.class
	}
	return classes
}

// An install that fails records its outcome: bad content is an error with
// the content fact, after which the demand, with no source left, computes
// the part locally and says so. A canceled install is canceled.
func TestOfferInstallFailedProfiled(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		chain := newChainFixture(t, "install-prof-corrupt")
		f := newExhaustionFixture(t, chain)
		f.transport.fault("https://fixed.invalid/install-prof-corrupt/1", contentTestFault{corrupt: true, stallAfter: -1})
		f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
		ctx, root := profiledRoot(f.ctx, "test.demand")
		f.ctx = ctx
		require.ErrorIs(t, f.run(), errRenewalFallbackRan)
		root.End(wcprof.OutcomeOK)

		events, _ := profiledUnder(t, root.ID())
		installs := profiledOps(events, "remotecache.install")
		require.Len(t, installs, 1)
		require.Equal(t, "error", installs[0].Outcome)
		require.Positive(t, installs[0].Bytes, "the bytes it cost")
		facts := profiledLinks(events, "fact", installs[0].OpID)
		require.Len(t, facts, 1)
		require.Equal(t, "install.content", facts[0].ident)
		receiver := uint64(f.receiver.cacheSharedResult().id)
		var noSource int
		for _, ev := range events {
			if ev.Type == "link" && ev.LinkKind == "fact" && ev.ident == "part.no_source" {
				require.Equal(t, receiver, ev.ResultID)
				noSource++
			}
		}
		require.Equal(t, 1, noSource, "the fallback to the operation is recorded once")
	})
	t.Run("canceled", func(t *testing.T) {
		chain := newChainFixture(t, "install-prof-canceled")
		f := newExhaustionFixture(t, chain)
		f.transport.opened = make(chan struct{}, 4)
		f.transport.fault("https://fixed.invalid/install-prof-canceled/0", contentTestFault{stallAfter: 0})
		f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
		ctx, root := profiledRoot(f.ctx, "test.demand")
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		f.ctx = ctx
		demanded := make(chan error, 1)
		go func() { demanded <- f.run() }()
		within(t, f.transport.opened)
		cancel()
		require.ErrorIs(t, within(t, demanded), context.Canceled)
		var installs []profiledEvent
		require.Eventually(t, func() bool {
			events, _ := profiledUnder(t, root.ID())
			installs = profiledOps(events, "remotecache.install")
			return len(installs) > 0
		}, 10*time.Second, 10*time.Millisecond)
		root.End(wcprof.OutcomeOK)
		require.Len(t, installs, 1)
		require.Equal(t, "canceled", installs[0].Outcome)
	})
}

// A part demand's acquire task records the steps of each pass as internal
// ops of its own: probing the receiver, selecting a source, and any decision
// or join, so that a profile can say where a long acquisition spends the
// time between them.
func TestDemandPartProfilesItsSteps(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	_, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	ctx, root := profiledRoot(ctx, "test.demand")
	require.NoError(t, c.demandPart(ctx, receiver, PersistedPartAddress{Part: "fs"}))
	root.End(wcprof.OutcomeOK)

	events, _ := profiledUnder(t, root.ID())
	var acquire *profiledEvent
	for i, ev := range events {
		if ev.Type == "op" && ev.ident == "acquire:fs" {
			acquire = &events[i]
		}
	}
	require.NotNil(t, acquire, "the demand's acquire task is profiled")
	steps := map[string]int{}
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == acquire.OpID && strings.HasPrefix(ev.class, "part.demand.") {
			steps[ev.class]++
			require.Equal(t, "internal", ev.OpKind, ev.class)
			require.GreaterOrEqual(t, ev.StartNS, acquire.StartNS, ev.class)
			require.LessOrEqual(t, ev.EndNS, acquire.EndNS, ev.class)
		}
	}
	require.Positive(t, steps["part.demand.probe"], "each pass probes the receiver")
	require.Positive(t, steps["part.demand.select"], "and selects a source")
}
