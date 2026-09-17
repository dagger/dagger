package core

import (
	"context"
	"fmt"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// sharingFinishScenario is one executed Container that A exports as metadata
// only and B has already computed for itself. The two rows differ in recipe,
// because each engine's Host directory is its own call, and are congruent once
// the import unites the two Host directories by content: early sharing is then
// the one way the imported Container R can get snapshots without running.
type sharingFinishScenario struct {
	a, b  *fixtureEngine
	nonce string
	// build is the pipeline both engines run.
	build func(client *dagger.Client) *dagger.Container
}

func newSharingFinishScenario(ctx context.Context, t *testctx.T, name string) *sharingFinishScenario {
	outer := connect(ctx, t)
	s := &sharingFinishScenario{
		a:     newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:     newFixtureEngine(ctx, t, outer, name+"-b", true),
		nonce: identity.NewID(),
	}
	for _, e := range []*fixtureEngine{s.a, s.b} {
		e.hostFile("work/seed.txt", "work seed "+s.nonce)
		e.hostFile("keep/seed.txt", "keep seed "+s.nonce)
	}
	s.build = func(client *dagger.Client) *dagger.Container {
		return client.Container().From(alpineImage).
			WithMountedDirectory("/work", client.Host().Directory("work")).
			WithMountedDirectory("/keep", client.Host().Directory("keep")).
			WithExec([]string{"sh", "-ec", "printf 'rootfs " + s.nonce + "' > /payload; printf 'mount " + s.nonce + "' > /work/out.txt"})
	}
	return s
}

// await waits for one armed barrier on B.
func (s *sharingFinishScenario) await(ctx context.Context, t *testctx.T, key string, generation uint64) dagql.FixtureBarrierReached {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var reached dagql.FixtureBarrierReached
	err := transferFixture(waitCtx, s.b.client, "barrierWait", s.b.control(key+"-wait.json", map[string]any{"key": key, "generation": generation}), []string{}, &reached)
	require.NoError(t, err, "barrier %s was never reached", key)
	return reached
}

// TestSharingFinish is the native sharing pass and Finish row (design §5 as
// amended by B1; batch 6's owed rows 2, 6 and 7).
func (RemoteCacheTransferSuite) TestSharingFinish(ctx context.Context, t *testctx.T) {
	// An encoded Container receiver gets its filesystem, its written mount
	// and its unchanged sibling mount in one pass: every slot is prepared
	// before the first Commit, nothing is read from a provider, the exec
	// never runs on B, and no transient pin is left.
	t.Run("OnePass", func(ctx context.Context, t *testctx.T) {
		s := newSharingFinishScenario(ctx, t, "finish-one")
		a, b := s.a, s.b
		exported := s.build(a.client)
		_, err := exported.Sync(ctx)
		require.NoError(t, err)
		aID, err := exported.ID(ctx)
		require.NoError(t, err)
		require.NoError(t, a.fixture("export", "finish.json", []string{string(aID)}, nil))
		a.copyFixtureTo(b, "finish.json")

		// The donor: B runs the same pipeline over its own Host directories.
		local := s.build(b.client)
		_, err = local.Sync(ctx)
		require.NoError(t, err)
		out, err := local.Directory("/work").File("out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "mount "+s.nonce, out)

		// Find the donor row before R exists: the class's one local Container.
		lID, err := local.ID(ctx)
		require.NoError(t, err)
		var donor fixtureControlsReport
		require.NoError(t, b.fixture("report", "", []string{string(lID)}, &donor))
		require.Len(t, donor.Rows, 1)
		require.False(t, donor.Rows[0].Imported)
		donorRefs := map[string]string{}
		for _, link := range donor.Rows[0].SnapshotLinks {
			donorRefs[link.Role] = link.RefKey
		}
		t.Logf("donor L=%d links=%v", donor.Rows[0].ResultID, donorRefs)
		// The only transient leases now are those of refs B's own pipeline
		// still holds, such as the image pull's. The passes must add none.
		pinsBefore := donor.Storage.TransientPinResources

		// The import starts a cascade of passes, one per imported row that
		// has a local equivalent. Pause every external Finish in turn until
		// the one that finishes R's first receipt: by then R's pass has
		// committed every slot, and no demand for R has been issued yet, so
		// the pass and nothing else installed them.
		arm := func(i int) dagql.FixtureBarrierArmed {
			var armed dagql.FixtureBarrierArmed
			key := fmt.Sprintf("finish-%d", i)
			require.NoError(t, b.fixture("barrierArm", b.control(key+".json", dagql.FixtureBarrierRequest{Key: key, Point: dagql.FixtureBeforeFinish, Action: dagql.FixturePause}), nil, &armed))
			return armed
		}
		armed := arm(0)
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "finish.json", nil, &imported))
		require.NotEmpty(t, imported)
		require.Equal(t, "Container", imported[0].Type.NamedType)
		rHandle, rID := imported[0].Handle, imported[0].ResultID
		var reached dagql.FixtureBarrierReached
		for i := 0; ; i++ {
			require.Less(t, i, 64, "R's receipt never reached its Finish")
			key := fmt.Sprintf("finish-%d", i)
			reached = s.await(ctx, t, key, armed.Generation)
			t.Logf("beforeFinish %d: row=%d pass=%d", i, reached.Event.ResultID, reached.Event.PassID)
			if reached.Event.ResultID == rID {
				require.NoError(t, b.fixture("barrierRelease", key+"-wait.json", nil, nil))
				break
			}
			next := arm(i + 1)
			require.NoError(t, b.fixture("barrierRelease", key+"-wait.json", nil, nil))
			armed = next
		}
		pass := reached.Event.PassID
		require.NotZero(t, pass)

		loaded := dagger.Ref[*dagger.Container](b.client, dagger.ID(rHandle))
		payload, err := loaded.File("/payload").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "rootfs "+s.nonce, payload)
		out, err = loaded.Directory("/work").File("out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "mount "+s.nonce, out)
		keep, err := loaded.Directory("/keep").File("seed.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep seed "+s.nonce, keep)

		var after, row fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &after))
		require.NoError(t, b.fixture("report", "", []string{rHandle}, &row))
		require.Len(t, row.Rows, 1)
		t.Logf("R=%d events: %v", rID, partKindsOf(after.transferFixtureReport, rID))

		// One pass prepared all of R's slots before its first Commit, and
		// released every member before the first external Finish.
		var lastPrepare, firstCommit, allPrepared, membersReleased, firstFinish uint64
		prepares := 0
		for _, o := range after.Reached {
			switch {
			case o.Point == dagql.FixturePrepareDone && o.ResultID == rID:
				prepares++
				lastPrepare = o.Sequence
			case o.Point == dagql.FixtureBeforeCommit && o.ResultID == rID && firstCommit == 0:
				firstCommit = o.Sequence
			case o.Point == dagql.FixtureShareAllPrepared && o.PassID == pass:
				allPrepared = o.Sequence
				require.Equal(t, "count=4", o.Detail, "metadata, filesystem, the written mount and its unchanged sibling in one pass")
			case o.Point == dagql.FixtureShareMembersReleased && o.PassID == pass:
				membersReleased = o.Sequence
			case o.Point == dagql.FixtureBeforeFinish && o.PassID == pass && firstFinish == 0:
				firstFinish = o.Sequence
			}
		}
		require.Equal(t, 4, prepares)
		require.Less(t, lastPrepare, allPrepared, "every slot is prepared")
		require.Less(t, allPrepared, firstCommit, "before the first Commit")
		require.Less(t, firstCommit, membersReleased)
		require.Less(t, membersReleased, firstFinish, "every member is released before the first external Finish")

		// R owns L's exact snapshots under its own positional roles.
		roles := map[string]string{}
		for _, link := range row.Rows[0].SnapshotLinks {
			roles[link.Role] = link.RefKey
		}
		require.Equal(t, donorRefs, roles)
		require.Contains(t, roles, "fs")
		require.Contains(t, roles, "mount_dir:0")
		require.Contains(t, roles, "mount_dir:1")
		require.Len(t, partEventsOf(after.transferFixtureReport, rID, "installed-ready"), 4)
		require.Len(t, partEventsOf(after.transferFixtureReport, rID, "settled"), 4)
		for _, kind := range []string{"lazy-enter", "provider-read", "installed-chain", "selected-chain", "selected-ready"} {
			require.Empty(t, partEventsOf(after.transferFixtureReport, rID, kind), "%s for R", kind)
		}
		require.NotNil(t, after.Storage)
		require.ElementsMatch(t, pinsBefore, after.Storage.TransientPinResources, "the passes and the reads added no transient pin")
		require.NoError(t, b.fixture("gc", "", nil, nil))
		require.NoError(t, b.fixture("report", "", nil, &after))
		require.ElementsMatch(t, pinsBefore, after.Storage.TransientPinResources)
	})
}
