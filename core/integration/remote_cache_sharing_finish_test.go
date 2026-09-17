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

// exportAndBuildDonor runs the pipeline on A and exports it as metadata only,
// then runs it on B, whose row is the donor L. It returns L's owner links by
// role and the transient leases B's own held refs account for, such as the
// image pull's: a pass must add none.
func (s *sharingFinishScenario) exportAndBuildDonor(ctx context.Context, t *testctx.T) (donorRefs map[string]string, pinsBefore []string) {
	t.Helper()
	exported := s.build(s.a.client)
	_, err := exported.Sync(ctx)
	require.NoError(t, err)
	aID, err := exported.ID(ctx)
	require.NoError(t, err)
	require.NoError(t, s.a.fixture("export", "finish.json", []string{string(aID)}, nil))
	s.a.copyFixtureTo(s.b, "finish.json")

	local := s.build(s.b.client)
	_, err = local.Sync(ctx)
	require.NoError(t, err)
	out, err := local.Directory("/work").File("out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "mount "+s.nonce, out)
	lID, err := local.ID(ctx)
	require.NoError(t, err)
	var donor fixtureControlsReport
	require.NoError(t, s.b.fixture("report", "", []string{string(lID)}, &donor))
	require.Len(t, donor.Rows, 1)
	require.False(t, donor.Rows[0].Imported)
	donorRefs = map[string]string{}
	for _, link := range donor.Rows[0].SnapshotLinks {
		donorRefs[link.Role] = link.RefKey
	}
	t.Logf("donor L=%d links=%v", donor.Rows[0].ResultID, donorRefs)
	return donorRefs, donor.Storage.TransientPinResources
}

// readAll reads the three trees through R's exact handle.
func (s *sharingFinishScenario) readAll(ctx context.Context, t *testctx.T, rHandle string) {
	t.Helper()
	loaded := dagger.Ref[*dagger.Container](s.b.client, dagger.ID(rHandle))
	payload, err := loaded.File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "rootfs "+s.nonce, payload)
	out, err := loaded.Directory("/work").File("out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "mount "+s.nonce, out)
	keep, err := loaded.Directory("/keep").File("seed.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "keep seed "+s.nonce, keep)
}

// importAndHoldR imports the bundle and leaves R's pass paused at its first
// arrival at point. The import starts a cascade of passes, one per imported
// row that has a local equivalent, and R's row is not known until the import
// returns, so a barrier cannot be selected by row in time. Instead every
// arrival at the point is paused in turn, the next barrier is armed before
// the last is released, and the walk stops at R's. It returns the key whose
// "-wait.json" record releases R's pause.
func (s *sharingFinishScenario) importAndHoldR(ctx context.Context, t *testctx.T, point dagql.FixtureBarrierPoint) (rHandle string, rID uint64, reached dagql.FixtureBarrierReached, key string) {
	t.Helper()
	arm := func(i int) dagql.FixtureBarrierArmed {
		var armed dagql.FixtureBarrierArmed
		key := fmt.Sprintf("hold-%d", i)
		require.NoError(t, s.b.fixture("barrierArm", s.b.control(key+".json", dagql.FixtureBarrierRequest{Key: key, Point: point, Action: dagql.FixturePause}), nil, &armed))
		return armed
	}
	armed := arm(0)
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "finish.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Container", imported[0].Type.NamedType)
	rHandle, rID = imported[0].Handle, imported[0].ResultID
	for i := 0; ; i++ {
		require.Less(t, i, 64, "R never reached %s", point)
		key = fmt.Sprintf("hold-%d", i)
		reached = s.await(ctx, t, key, armed.Generation)
		t.Logf("%s %d: row=%d pass=%d", point, i, reached.Event.ResultID, reached.Event.PassID)
		if reached.Event.ResultID == rID {
			return rHandle, rID, reached, key
		}
		next := arm(i + 1)
		require.NoError(t, s.b.fixture("barrierRelease", key+"-wait.json", nil, nil))
		armed = next
	}
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
		b := s.b
		donorRefs, pinsBefore := s.exportAndBuildDonor(ctx, t)

		// Hold R's first receipt before its external Finish: by then R's pass
		// has committed every slot, and no demand for R has been issued yet,
		// so the pass and nothing else installed them.
		cascade := time.Now()
		rHandle, rID, reached, key := s.importAndHoldR(ctx, t, dagql.FixtureBeforeFinish)
		t.Logf("measurement: import to R's first Finish, every earlier Finish paused and released through the harness: %s", time.Since(cascade))
		require.NoError(t, b.fixture("barrierRelease", key+"-wait.json", nil, nil))
		pass := reached.Event.PassID
		require.NotZero(t, pass)

		s.readAll(ctx, t, rHandle)

		var after, row fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &after))
		require.NoError(t, b.fixture("report", "", []string{rHandle}, &row))
		require.Len(t, row.Rows, 1)
		t.Logf("R=%d events: %v", rID, partKindsOf(after.transferFixtureReport, rID))

		members := map[uint64]string{}
		for _, o := range after.Reached {
			if o.Point == dagql.FixtureShareAllPrepared {
				members[o.PassID] = o.Detail
			}
		}
		t.Logf("measurement: sharing passes and their prepared slots: %v", members)
		t.Logf("measurement: storage after the passes: snapshots=%d blobs=%d ownerLeases=%d transientPins=%d", after.Storage.Snapshots, after.Storage.Blobs, len(after.Storage.OwnerLeases), after.Storage.TransientPins)

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

		// While L lives, a read of R's handle may be served by L, the complete
		// equivalent row. End L's one owner, collect, and read again: R itself
		// now serves all three trees from the snapshots it owns.
		b.reconnect()
		require.NoError(t, b.fixture("gc", "", nil, nil))
		s.readAll(ctx, t, rHandle)
		require.NoError(t, b.fixture("report", "", nil, &after))
		for _, kind := range []string{"lazy-enter", "provider-read", "installed-chain"} {
			require.Empty(t, partEventsOf(after.transferFixtureReport, rID, kind), "after the donor's release: %s", kind)
		}
		require.Len(t, partEventsOf(after.transferFixtureReport, rID, "installed-ready"), 4, "nothing was installed again")
	})

	// A foreground read of R's handle races R's pass, which is held with all
	// of its slots prepared and none committed. The read must succeed without
	// waiting for the pass: while the donor lives the session's lookup may
	// serve it from L, the complete equivalent row, and whichever row serves
	// it, nothing is evaluated. Afterwards every address of R is installed
	// exactly once. G2's failing prefix, where a concurrent decode moves R
	// under a prepared slot, is in process by ruling (batch 6's
	// PrefixCarriedOverRefusedAddress and DecodeWhileFinishPaused): no
	// ordinary native operation demands or decodes R exactly while its donor
	// lives, and an export of a row with an attempt in flight is refused as
	// not ready by batch 2's contract.
	t.Run("ForegroundReadRacesPass", func(ctx context.Context, t *testctx.T) {
		s := newSharingFinishScenario(ctx, t, "finish-race")
		b := s.b
		_, pinsBefore := s.exportAndBuildDonor(ctx, t)
		rHandle, rID, _, key := s.importAndHoldR(ctx, t, dagql.FixtureBeforeCommit)

		done := make(chan error, 1)
		go func() {
			payload, err := dagger.Ref[*dagger.Container](b.client, dagger.ID(rHandle)).File("/payload").Contents(ctx)
			if err == nil && payload != "rootfs "+s.nonce {
				err = fmt.Errorf("read %q", payload)
			}
			done <- err
		}()
		select {
		case err := <-done:
			require.NoError(t, err, "a foreground read racing a held pass succeeds")
		case <-time.After(2 * time.Minute):
			t.Fatal("the foreground read waited for the held pass")
		}
		// Let the pass go, and hold it again at R's first external Finish: the
		// commit phase is then over, so every install of R is recorded before
		// the report below is read.
		var finish dagql.FixtureBarrierArmed
		require.NoError(t, b.fixture("barrierArm", b.control("finish.json", dagql.FixtureBarrierRequest{Key: "finish", Point: dagql.FixtureBeforeFinish, Selector: dagql.FixtureBarrierSelector{ResultID: rID}, Action: dagql.FixturePause}), nil, &finish))
		require.NoError(t, b.fixture("barrierRelease", key+"-wait.json", nil, nil))
		s.await(ctx, t, "finish", finish.Generation)
		require.NoError(t, b.fixture("barrierRelease", "finish-wait.json", nil, nil))
		s.readAll(ctx, t, rHandle)
		var after fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &after))
		t.Logf("R=%d events: %v", rID, partKindsOf(after.transferFixtureReport, rID))
		installs := map[dagql.PartKey]int{}
		for _, index := range partEventsOf(after.transferFixtureReport, rID, "installed-ready") {
			installs[after.Parts[index].Address.Part]++
		}
		require.Len(t, installs, 4, "every address of R is installed: %v", installs)
		for part, n := range installs {
			require.Equal(t, 1, n, "%s is installed exactly once", part)
		}
		require.Empty(t, partEventsOf(after.transferFixtureReport, rID, "lazy-enter"))
		require.NoError(t, b.fixture("gc", "", nil, nil))
		require.NoError(t, b.fixture("report", "", nil, &after))
		require.ElementsMatch(t, pinsBefore, after.Storage.TransientPinResources)
	})
	// A smoke and a measurement (design B1, F2): a receiver whose exec used a
	// service binding. Its encoded value carries the Service ancestry, which
	// the pass's decode preflight must accept or refuse slot by slot without
	// ever starting a service or evaluating anything. R is then read. The
	// slots the pass left alone are counted with their causes; the two leader
	// orders and the decoder closure are author B's, in process.
	t.Run("ServiceBackedReceiver", func(ctx context.Context, t *testctx.T) {
		s := newSharingFinishScenario(ctx, t, "finish-service")
		s.build = func(client *dagger.Client) *dagger.Container {
			www := client.Container().From(busyboxImage).
				WithNewFile("/srv/index.html", "served "+s.nonce).
				WithExposedPort(8080).
				WithDefaultArgs([]string{"httpd", "-f", "-p", "8080", "-h", "/srv"}).
				AsService()
			return client.Container().From(alpineImage).
				WithMountedDirectory("/work", client.Host().Directory("work")).
				WithMountedDirectory("/keep", client.Host().Directory("keep")).
				WithServiceBinding("www", www).
				WithExec([]string{"sh", "-ec", "wget -q -O /work/out.txt http://www:8080/index.html; printf 'rootfs " + s.nonce + "' > /payload; printf 'mount " + s.nonce + "' > /work/out.txt"})
		}
		b := s.b
		_, pinsBefore := s.exportAndBuildDonor(ctx, t)
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "finish.json", nil, &imported))
		require.NotEmpty(t, imported)
		rHandle, rID := imported[0].Handle, imported[0].ResultID

		s.readAll(ctx, t, rHandle)
		var after fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &after))
		t.Logf("R=%d events: %v", rID, partKindsOf(after.transferFixtureReport, rID))
		causes := map[string]int{}
		skipped := 0
		for _, event := range after.Parts {
			if event.Kind == "share-skipped" {
				skipped++
				causes[event.Detail]++
			}
		}
		t.Logf("measurement: %d slots left alone by sharing passes, by cause: %v", skipped, causes)
		installs := len(partEventsOf(after.transferFixtureReport, rID, "installed-ready"))
		t.Logf("measurement: R had %d slots installed by sharing or a ready local source", installs)
		require.Empty(t, partEventsOf(after.transferFixtureReport, rID, "lazy-enter"), "reading a service-backed receiver evaluates nothing")
		require.Empty(t, partEventsOf(after.transferFixtureReport, rID, "provider-read"))
		require.NoError(t, b.fixture("gc", "", nil, nil))
		require.NoError(t, b.fixture("report", "", nil, &after))
		require.ElementsMatch(t, pinsBefore, after.Storage.TransientPinResources)

		// While L lives a read of R's handle may be served by L. End L's
		// owner, collect, and read again: now R itself serves, still with no
		// evaluation, no content read and no service start.
		b.reconnect()
		require.NoError(t, b.fixture("gc", "", nil, nil))
		s.readAll(ctx, t, rHandle)
		require.NoError(t, b.fixture("report", "", nil, &after))
		t.Logf("R=%d events after the donor's release: %v", rID, partKindsOf(after.transferFixtureReport, rID))
		require.Empty(t, partEventsOf(after.transferFixtureReport, rID, "lazy-enter"))
		require.Empty(t, partEventsOf(after.transferFixtureReport, rID, "provider-read"))
	})
}
