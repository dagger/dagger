package containerd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// fakeSnapshotter answers the calls the engine times, failing Commit.
type fakeSnapshotter struct {
	snapshots.Snapshotter
}

func (fakeSnapshotter) Usage(context.Context, string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}

func (fakeSnapshotter) Mounts(context.Context, string) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) Prepare(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) View(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) Commit(context.Context, string, string, ...snapshots.Opt) error {
	return errors.New("commit failed")
}

// Each snapshot call made under a profiled op is an op under it, named for the
// call, with its snapshot key as its ident and a failed call failed. An
// unprofiled call records nothing.
func TestSnapshotCallsAreOps(t *testing.T) {
	wcprof.EnsureRecorder()
	sn := &nsSnapshotter{ns: "test", Snapshotter: fakeSnapshotter{}}
	calls := func(ctx context.Context, prefix string) {
		_, _ = sn.Usage(ctx, prefix+"-usage")
		_, _ = sn.Mounts(ctx, prefix+"-mounts")
		_, _ = sn.Prepare(ctx, prefix+"-prepare", "")
		_, _ = sn.View(ctx, prefix+"-view", "")
		_ = sn.Commit(ctx, prefix+"-commit", prefix+"-active")
	}

	calls(context.Background(), "unprofiled")
	ctx, parent := wcprof.BeginOp(wcprof.ContextWithProfiling(context.Background()), wcprof.OpKindInternal, "snaptest.parent", wcprof.OpOpts{})
	calls(ctx, "profiled")
	parent.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	got := map[string]wcprof.DumpEvent{}
	for _, ev := range events {
		if ev.Type != "op" {
			continue
		}
		require.NotContains(t, header.Strings[ev.IdentID], "unprofiled", "an unprofiled call recorded an op")
		if ev.ParentID == parent.ID() {
			got[header.Strings[ev.ClassID]] = ev
		}
	}
	for _, call := range []string{"usage", "mounts", "prepare", "view", "commit"} {
		ev, ok := got["snapshot."+call]
		require.True(t, ok, "no snapshot.%s op under the parent", call)
		require.Equal(t, "profiled-"+call, header.Strings[ev.IdentID])
		require.Equal(t, wcprof.OpKindInternal.String(), ev.OpKind)
		want := wcprof.OutcomeOK.String()
		if call == "commit" {
			want = wcprof.OutcomeError.String()
		}
		require.Equal(t, want, ev.Outcome, call)
	}
}
