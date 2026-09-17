package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// encodedRestartScenario is one B with a local capture L and an imported,
// still encoded Directory R whose only possible source of bytes is early
// sharing from L (A exported metadata only). prepare runs before the import,
// which is what triggers the pass.
type encodedRestartScenario struct {
	b        *fixtureEngine
	notes    string
	rHandle  string
	rID      uint64
	donorRef string
}

func newEncodedRestartScenario(ctx context.Context, t *testctx.T, prepare func(b *fixtureEngine)) *encodedRestartScenario {
	t.Helper()
	outer := connect(ctx, t)
	a := newFixtureEngine(ctx, t, outer, "encoded-a", true)
	b := newFixtureEngine(ctx, t, outer, "encoded-b", true)
	s := &encodedRestartScenario{b: b, notes: "encoded restart " + identity.NewID()}
	a.hostFile("src/notes.txt", s.notes)
	b.hostFile("src/notes.txt", s.notes)
	aDirectory, err := a.client.Host().Directory("src").Sync(ctx)
	require.NoError(t, err)
	aID, err := aDirectory.ID(ctx)
	require.NoError(t, err)
	require.NoError(t, a.fixture("export", "encoded.json", []string{string(aID)}, nil))
	a.copyFixtureTo(b, "encoded.json")

	local, err := b.client.Host().Directory("src").Sync(ctx)
	require.NoError(t, err)
	lID, err := local.ID(ctx)
	require.NoError(t, err)
	var donor fixtureControlsReport
	require.NoError(t, b.fixture("report", "", []string{string(lID)}, &donor))
	require.Len(t, donor.Rows, 1)
	require.Len(t, donor.Rows[0].SnapshotLinks, 1)
	s.donorRef = donor.Rows[0].SnapshotLinks[0].RefKey

	prepare(b)
	var imported []transferFixtureMapping
	require.NoError(t, b.fixture("import", "encoded.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	s.rHandle, s.rID = imported[0].Handle, imported[0].ResultID
	return s
}

func (s *encodedRestartScenario) await(ctx context.Context, t *testctx.T, key string, generation uint64) dagql.FixtureBarrierReached {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var reached dagql.FixtureBarrierReached
	err := transferFixture(waitCtx, s.b.client, "barrierWait", s.b.control(key+"-wait.json", map[string]any{"key": key, "generation": generation}), []string{}, &reached)
	require.NoError(t, err, "barrier %s was never reached", key)
	return reached
}

// TestEncodedRestart is the native half of "encoded decode and partial sync"
// (design §5 as amended by B1) and G3.
func (RemoteCacheTransferSuite) TestEncodedRestart(ctx context.Context, t *testctx.T) {
	// A share's owner synchronization fails after the real attachment. The
	// installed output is kept. The engine is then stopped cleanly with the
	// bookkeeping still owed: the checkpoint must carry R's complete desired
	// role, and the next boot must restore R's owner before the previous
	// process's transfer pins are released, so R reads after the restart and
	// after a real collection, with nothing to fall back on.
	t.Run("FailedAttachThenRestart", func(ctx context.Context, t *testctx.T) {
		var attach, synced dagql.FixtureBarrierArmed
		s := newEncodedRestartScenario(ctx, t, func(b *fixtureEngine) {
			require.NoError(t, b.fixture("barrierArm", b.control("attach.json", dagql.FixtureBarrierRequest{Key: "attach", Point: dagql.FixtureAfterOwnerAttach, Action: dagql.FixtureFailOwnerAttachAfter}), nil, &attach))
			require.NoError(t, b.fixture("barrierArm", b.control("synced.json", dagql.FixtureBarrierRequest{Key: "synced", Point: dagql.FixtureOwnerSyncDone, Action: dagql.FixturePause}), nil, &synced))
		})
		b := s.b
		reached := s.await(ctx, t, "attach", attach.Generation)
		require.Equal(t, s.rID, reached.Event.ResultID, "the faulted attachment is R's")
		require.Contains(t, reached.Event.Detail, s.donorRef)
		done := s.await(ctx, t, "synced", synced.Generation)
		require.Equal(t, s.rID, done.Event.ResultID)
		require.Contains(t, done.Event.Detail, "fixture local storage fault", "the owner synchronization really failed")
		require.NoError(t, b.fixture("barrierRelease", "synced-wait.json", nil, nil))

		var pending fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &pending))
		require.Len(t, partEventsOf(pending.transferFixtureReport, s.rID, "installed-ready"), 1, "the output was installed")
		require.Empty(t, partEventsOf(pending.transferFixtureReport, s.rID, "settled"), "and its bookkeeping is still owed")

		b.restart()
		var restored fixtureControlsReport
		require.NoError(t, b.fixture("report", "", []string{s.rHandle}, &restored))
		require.Equal(t, dagql.CachePersistenceResetNone, restored.Persistence.PersistenceResetReason)
		require.Empty(t, restored.Persistence.LocalCacheResetReason)
		require.Len(t, restored.Rows, 1)
		require.True(t, restored.Rows[0].Imported)
		require.Len(t, restored.Rows[0].SnapshotLinks, 1, "the checkpoint carried R's desired role and boot applied it")
		require.Equal(t, s.donorRef, restored.Rows[0].SnapshotLinks[0].RefKey)
		require.NoError(t, b.fixture("gc", "", nil, nil), "a real collection after boot must not take R's snapshot")
		contents, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(s.rHandle)).File("notes.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.notes, contents)
		var after fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &after))
		for _, kind := range []string{"provider-read", "installed-chain", "installed-ready", "lazy-enter"} {
			require.Empty(t, partEventsOf(after.transferFixtureReport, s.rID, kind), "after the restart: %s", kind)
		}
	})

	// G3. A local restore failure is not a cache hit and is not repaired row
	// by row: one saved owner link that cannot attach resets the whole cache,
	// the engine still boots, and nothing is requested from anywhere.
	t.Run("LocalRestoreReset", func(ctx context.Context, t *testctx.T) {
		var finish dagql.FixtureBarrierArmed
		s := newEncodedRestartScenario(ctx, t, func(b *fixtureEngine) {
			require.NoError(t, b.fixture("barrierArm", b.control("finish.json", dagql.FixtureBarrierRequest{Key: "finish", Point: dagql.FixtureBeforeFinish, Action: dagql.FixturePause}), nil, &finish))
		})
		b := s.b
		s.await(ctx, t, "finish", finish.Generation)
		require.NoError(t, b.fixture("barrierRelease", "finish-wait.json", nil, nil))
		_, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(s.rHandle)).Entries(ctx)
		require.NoError(t, err)

		// Damage exactly one saved owner link while the engine is stopped.
		b.stop()
		out := b.stateExec([]string{"sqlite"}, `
			db="$(find /state -name dagql-cache.db | head -n1)"
			[ -n "$db" ] || { echo "no dagql-cache.db under /state" >&2; find /state -maxdepth 2 >&2; exit 1; }
			before="$(sqlite3 "$db" "SELECT count(*) FROM result_snapshot_links WHERE result_id = $ROW")"
			sqlite3 "$db" "UPDATE result_snapshot_links SET ref_key = 'b7-missing-snapshot' WHERE result_id = $ROW"
			after="$(sqlite3 "$db" "SELECT ref_key FROM result_snapshot_links WHERE result_id = $ROW")"
			echo "links=$before now=$after"
		`, map[string]string{"ROW": strconv.FormatUint(s.rID, 10)})
		require.Contains(t, out, "links=1 now=b7-missing-snapshot", "exactly R's one saved link was damaged")
		b.start()

		var reset fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &reset), "the engine booted")
		t.Logf("after the damaged restore: %+v", reset.Persistence)
		require.Equal(t, dagql.CachePersistenceResetImportFailure, reset.Persistence.PersistenceResetReason, "a failed owner attachment resets the whole cache")
		require.True(t, strings.Contains(reset.Persistence.LocalCacheResetReason, "import_failure"), "the engine recorded the local reset: %q", reset.Persistence.LocalCacheResetReason)
		// The new cache has only what this session has made since the boot.
		for _, row := range reset.Rows {
			require.False(t, row.Imported, "no imported row survived: nothing was repaired one by one (row %d)", row.ResultID)
			for _, link := range row.SnapshotLinks {
				require.NotEqual(t, "b7-missing-snapshot", link.RefKey, "the damaged link is gone (row %d)", row.ResultID)
			}
		}
		require.ErrorContains(t, b.fixture("report", "", []string{s.rHandle}, nil), "missing shared result", "R is gone with the rest")
		require.Empty(t, reset.Parts, "nothing was demanded to repair it")
		require.NotNil(t, reset.Transport)
		require.Zero(t, reset.Transport.FixtureHosts, "no remote request was made to repair anything")
	})
}
