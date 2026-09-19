package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestSharingDonorRestart is the native donor-lifetime row (design §5 as
// amended by B1, batch 6's owed row 1). A exports only metadata, so nothing
// offers the imported Directory R any bytes and it has no Lazy operation: the
// one way R can get its snapshot is early sharing from B's own capture L. The
// share completes; then every owner of L ends (its session, its saved edge),
// the engine's real garbage collection runs, and L is gone. R still reads,
// through its exact imported handle, before and after a clean restart, with no
// content read, no chain install and no evaluation at any point.
//
// Both import orders run: the donor exists before the import, and the donor is
// captured after it.
func (RemoteCacheTransferSuite) TestSharingDonorRestart(ctx context.Context, t *testctx.T) {
	for _, order := range []string{"DonorBeforeImport", "DonorAfterImport"} {
		t.Run(order, func(ctx context.Context, t *testctx.T) {
			outer := connect(ctx, t)
			a := newFixtureEngine(ctx, t, outer, "donor-a", true)
			b := newFixtureEngine(ctx, t, outer, "donor-b", true)
			notes := "donor lifetime " + identity.NewID()
			a.hostFile("src/notes.txt", notes)
			b.hostFile("src/notes.txt", notes)

			aDirectory, err := a.client.Host().Directory("src").Sync(ctx)
			require.NoError(t, err)
			aID, err := aDirectory.ID(ctx)
			require.NoError(t, err)
			// Metadata only: no selected output, so no chain is offered.
			require.NoError(t, a.fixture("export", "donor.json", []string{string(aID)}, nil))
			a.copyFixtureTo(b, "donor.json")

			// capture loads B's own copy and finds its row, the donor L, among
			// every row: once R exists the captured ID resolves to R, which is
			// the older member of the same output class, so L is named by what
			// it is instead: the class's one local row that owns a snapshot.
			capture := func() (handle string, row dagql.TransferFixtureRow) {
				directory, err := b.client.Host().Directory("src").Sync(ctx)
				require.NoError(t, err)
				id, err := directory.ID(ctx)
				require.NoError(t, err)
				var resolved, all fixtureControlsReport
				require.NoError(t, b.fixture("report", "", []string{string(id)}, &resolved))
				require.Len(t, resolved.Rows, 1)
				require.NoError(t, b.fixture("report", "", nil, &all))
				var donors []dagql.TransferFixtureRow
				for _, candidate := range all.Rows {
					shares := slices.ContainsFunc(candidate.OutputClasses, func(class uint64) bool {
						return slices.Contains(resolved.Rows[0].OutputClasses, class)
					})
					if shares && !candidate.Imported && len(candidate.SnapshotLinks) == 1 {
						donors = append(donors, candidate)
					}
				}
				require.Len(t, donors, 1, "B's capture is one local row that owns its snapshot; the captured ID resolved to row %d (imported=%t)", resolved.Rows[0].ResultID, resolved.Rows[0].Imported)
				return string(id), donors[0]
			}

			// The pass is asynchronous. A pause before its external Finish is
			// the real signal that a share committed; nothing polls a report.
			var armed dagql.FixtureBarrierArmed
			require.NoError(t, b.fixture("barrierArm", b.control("finish.json", dagql.FixtureBarrierRequest{Key: "finish", Point: dagql.FixtureBeforeFinish, Action: dagql.FixturePause}), nil, &armed))

			var lHandle string
			var lRow dagql.TransferFixtureRow
			var imported []transferFixtureMapping
			if order == "DonorBeforeImport" {
				lHandle, lRow = capture()
				require.NoError(t, b.fixture("import", "donor.json", nil, &imported))
			} else {
				require.NoError(t, b.fixture("import", "donor.json", nil, &imported))
				lHandle, lRow = capture()
			}
			require.NotEmpty(t, imported)
			require.Equal(t, "Directory", imported[0].Type.NamedType)
			rHandle, rID := imported[0].Handle, imported[0].ResultID
			donorRef := lRow.SnapshotLinks[0].RefKey
			t.Logf("donor L=%d persisted=%t ref=%s; receiver R=%d", lRow.ResultID, lRow.Persisted, donorRef, rID)

			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			var reached dagql.FixtureBarrierReached
			err = transferFixture(waitCtx, b.client, "barrierWait", b.control("finish-wait.json", map[string]any{"key": "finish", "generation": armed.Generation}), []string{}, &reached)
			cancel()
			require.NoError(t, err, "no sharing pass committed a receipt")
			require.Equal(t, rID, reached.Event.ResultID, "the committed receipt is R's")
			require.NotZero(t, reached.Event.PassID)
			require.NoError(t, b.fixture("barrierRelease", "finish-wait.json", nil, nil))

			// An ordinary read through the exact imported reference. It joins
			// the installed output's task, so it returns after the settlement.
			entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(rHandle)).Entries(ctx)
			require.NoError(t, err)
			require.Contains(t, entries, "notes.txt")

			var shared fixtureControlsReport
			require.NoError(t, b.fixture("report", "", []string{rHandle}, &shared))
			require.Len(t, shared.Rows, 1)
			require.True(t, shared.Rows[0].Imported)
			require.Len(t, shared.Rows[0].SnapshotLinks, 1, "R owns its own snapshot link")
			require.Equal(t, donorRef, shared.Rows[0].SnapshotLinks[0].RefKey, "it is L's exact snapshot, owned independently")
			// R owns its role lease by name, and the pass left no pin behind.
			require.NotNil(t, shared.Storage)
			require.Zero(t, shared.Storage.TransientPins, "the pass released its transient pins")
			ownsLease := func(report fixtureControlsReport, id uint64) bool {
				return slices.ContainsFunc(report.Storage.OwnerLeases, func(lease string) bool {
					return strings.HasPrefix(lease, fmt.Sprintf("dagql/result/%d/", id))
				})
			}
			require.True(t, ownsLease(shared, rID), "R has its own owner lease: %v", shared.Storage.OwnerLeases)
			require.True(t, ownsLease(shared, lRow.ResultID), "L still has its own")
			forbidden := []string{"provider-read", "installed-chain", "lazy-enter", "selected-chain"}
			noForbidden := func(report fixtureControlsReport, when string) {
				for _, kind := range forbidden {
					require.Empty(t, partEventsOf(report.transferFixtureReport, rID, kind), "%s: %s for R", when, kind)
				}
			}
			var all fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &all))
			require.Len(t, partEventsOf(all.transferFixtureReport, rID, "installed-ready"), 1, "R took its snapshot exactly once")
			require.Len(t, partEventsOf(all.transferFixtureReport, rID, "settled"), 1)
			require.Empty(t, partEventsOf(all.transferFixtureReport, rID, "selected-ready"), "the pass installed it; no demand selected a source")
			noForbidden(all, "after the share")

			// Release the donor: end the session that loaded L, remove its saved
			// retention edge if it has one, and run the engine's real GC.
			var before fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &before))
			require.False(t, lRow.Persisted, "a Host capture has no saved retention edge; its session is its one owner")
			b.reconnect()
			var gc struct {
				Generation                      uint64
				SnapshotsBefore, SnapshotsAfter uint64
				LeasesBefore, LeasesAfter       uint64
			}
			require.NoError(t, b.fixture("gc", "", nil, &gc))
			require.Equal(t, uint64(1), gc.Generation)
			t.Logf("gc after donor release: snapshots %d -> %d, leases %d -> %d", gc.SnapshotsBefore, gc.SnapshotsAfter, gc.LeasesBefore, gc.LeasesAfter)
			var after fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &after))
			for _, row := range after.Rows {
				require.NotEqual(t, lRow.ResultID, row.ResultID, "L must actually be unregistered once its last owner ended")
			}
			require.False(t, ownsLease(after, lRow.ResultID), "L's owner lease went with it")
			require.True(t, ownsLease(after, rID))
			if order == "DonorBeforeImport" {
				// Here the handle named L itself, so it no longer loads at all.
				require.ErrorContains(t, b.fixture("report", "", []string{lHandle}, nil), "missing shared result")
			}
			require.Equal(t, dagql.TransferFixtureControls{}, before.Controls, "no fixture hold kept L or R alive")

			// A fresh reader that never loaded L or the origin reads R.
			entries, err = dagger.Ref[*dagger.Directory](b.client, dagger.ID(rHandle)).Entries(ctx)
			require.NoError(t, err, "a receiver-owned snapshot does not depend on its donor")
			require.Contains(t, entries, "notes.txt")
			contents, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(rHandle)).File("notes.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, notes, contents)
			require.NoError(t, b.fixture("report", "", nil, &all))
			noForbidden(all, "after donor collection")
			require.Len(t, partEventsOf(all.transferFixtureReport, rID, "installed-ready"), 1)

			// Clean restart: no reset, R still imported with its own link, and
			// the same exact read with nothing to fall back on.
			b.restart()
			var restored fixtureControlsReport
			require.NoError(t, b.fixture("report", "", []string{rHandle}, &restored))
			require.Equal(t, dagql.CachePersistenceResetNone, restored.Persistence.PersistenceResetReason)
			require.Empty(t, restored.Persistence.LocalCacheResetReason)
			require.Len(t, restored.Rows, 1)
			require.True(t, restored.Rows[0].Imported)
			require.Len(t, restored.Rows[0].SnapshotLinks, 1)
			require.Equal(t, donorRef, restored.Rows[0].SnapshotLinks[0].RefKey, "R's owner link survived the restart")
			require.NoError(t, b.fixture("gc", "", nil, &gc), "a collection after the restart must not take R's snapshot")
			contents, err = dagger.Ref[*dagger.Directory](b.client, dagger.ID(rHandle)).File("notes.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, notes, contents)
			require.NoError(t, b.fixture("report", "", nil, &all))
			noForbidden(all, "after the restart")
			require.Empty(t, partEventsOf(all.transferFixtureReport, rID, "installed-ready"), "nothing is installed again after the restart")
		})
	}
}
