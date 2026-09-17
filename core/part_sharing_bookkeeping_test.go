package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// A sharing pass installs an imported Directory's snapshot from a local
// equivalent and its owner synchronization then fails, so the output is
// installed and its bookkeeping is owed. Which read pays that debt depends on
// which row serves it. A session's load of the receiver's handle is an ordinary
// lookup, and equivalent results are interchangeable: it is served by the
// complete local row, never touches the receiver, and retries nothing. A demand
// of the receiver's exact row joins its installation and retries only the
// bookkeeping: no selection, no read, no evaluation and no second install.
func TestSharingOwedBookkeepingIsPaidByAnExactDemand(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	aRef, _ := aStore.Build(t, nil, "notes.txt", "shared notes")
	aCtx, a, aSrv := transferCache(t, aStore, "", "a")
	mk := func(ref bkcache.ImmutableRef) *Directory {
		d := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: Platform{OS: "linux", Architecture: "amd64"}}
		d.SetPath("/")
		d.SetSnapshot(ref)
		return d
	}
	root := attachTransferObject(t, aCtx, a, aSrv, "a", "sharedDir", mk(aRef))
	var bundle dagql.ValueBundle
	require.NoError(t, a.WithExportedValues(aCtx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}}, config.RefConfig{}, func(_ context.Context, v *dagql.ExportedValues) error {
		bundle = v.Bundle
		return nil
	}))

	bCtx, b, bSrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
	require.NoError(t, b.EnableSnapshotSharing())
	b.EnableTransferFixtureParts()
	bRef, _ := bStore.Build(t, nil, "notes.txt", "shared notes")
	// The same recipe as the imported row makes the two equivalent.
	donor := attachTransferObject(t, bCtx, b, bSrv, "b", "sharedDir", mk(bRef))

	attach, err := b.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{Key: "attach", Point: dagql.FixtureAfterOwnerAttach, Action: dagql.FixtureFailOwnerAttachAfter})
	require.NoError(t, err)
	synced, err := b.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{Key: "synced", Point: dagql.FixtureOwnerSyncDone, Action: dagql.FixturePause})
	require.NoError(t, err)

	imported, err := b.ImportValues(bCtx, bundle)
	require.NoError(t, err)
	rID := imported[len(imported)-1].ResultID
	wait, cancel := context.WithTimeout(bCtx, 10*time.Second)
	defer cancel()
	_, err = b.WaitTransferFixtureBarrier(wait, attach.Key, attach.Generation)
	require.NoError(t, err, "the pass never reached the owner attach")
	_, err = b.WaitTransferFixtureBarrier(wait, synced.Key, synced.Generation)
	require.NoError(t, err)
	require.NoError(t, b.ReleaseTransferFixtureBarrier(synced.Key, synced.Generation))

	kinds := func() (out []string) {
		report, err := b.TransferFixtureSnapshot(bCtx, "b", nil)
		require.NoError(t, err)
		for _, e := range report.Parts {
			if e.ResultID == rID {
				out = append(out, e.Kind)
			}
		}
		return out
	}
	require.Equal(t, []string{"installed-ready", "owner-sync"}, kinds(), "installed, with the failed synchronization owed")
	donorRow, err := b.PersistedResultID(donor)
	require.NoError(t, err)

	load := func(session string) uint64 {
		t.Helper()
		loaded, err := b.LoadResultByResultID(bCtx, session, bSrv, rID)
		require.NoError(t, err)
		r := loaded.(dagql.ObjectResult[*Directory])
		require.NoError(t, b.Evaluate(bCtx, r))
		require.Equal(t, []string{"notes.txt"}, demandedDirectoryEntries(t, bCtx, r, ""))
		row, err := b.PersistedResultID(r)
		require.NoError(t, err)
		return row
	}
	require.Equal(t, donorRow, load("b"), "the session's read of the receiver's handle is served by its local equivalent")
	require.Equal(t, []string{"installed-ready", "owner-sync"}, kinds(), "which leaves the receiver and its debt untouched")

	require.Equal(t, rID, load(""), "an exact load selects the receiver itself")
	after := kinds()
	require.Contains(t, after, "settled", "its demand paid the owed bookkeeping")
	for _, kind := range after {
		require.Contains(t, []string{"installed-ready", "owner-sync", "settled"}, kind, "and did nothing else")
	}
	installs := 0
	for _, kind := range after {
		if kind == "installed-ready" {
			installs++
		}
	}
	require.Equal(t, 1, installs, "no second installation")
}
