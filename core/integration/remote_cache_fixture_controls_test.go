package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	enginecore "github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type fixtureControlsReport struct {
	transferFixtureReport
	Reached   []dagql.FixtureObservation             `json:"reached"`
	Controls  dagql.TransferFixtureControls          `json:"controls"`
	Transport *fixturetransport.Report               `json:"transport"`
	Storage   *enginecore.RemoteCacheFixtureStorage  `json:"storage"`
	Renewal   *enginecore.RemoteCacheFixtureRenewals `json:"renewal"`
}

// reachedAt returns the journalled observations of one point.
func (r fixtureControlsReport) reachedAt(point dagql.FixtureBarrierPoint) []dagql.FixtureObservation {
	var out []dagql.FixtureObservation
	for _, o := range r.Reached {
		if o.Point == point {
			out = append(out, o)
		}
	}
	return out
}

// fixtureExportSelectedResult is the exportSelected operation's reply.
type fixtureExportSelectedResult struct {
	Roots   []transferFixtureMapping `json:"roots"`
	Outputs []struct {
		Ordinal dagql.TransferOrdinal      `json:"ordinal"`
		Address dagql.PersistedPartAddress `json:"address"`
		Layers  []struct {
			Digest      string `json:"digest"`
			Size        int64  `json:"size"`
			CopiedBytes int64  `json:"copiedBytes"`
		} `json:"layers"`
	} `json:"outputs"`
}

// TestFixtureControls is the native half of the fixture's own correctness:
// the controls act on a real engine, its real stores and real restarts. The
// protocol half runs in process (core/schema TestFixtureControls, the dagql
// TestFixtureBarrier and TestFixtureHold cases, engine/server
// TestRemoteCacheFixtureRenewal, engine/fixturetransport).
func (RemoteCacheTransferSuite) TestFixtureControls(ctx context.Context, t *testctx.T) {
	t.Run("AbsentGate", func(ctx context.Context, t *testctx.T) {
		// A disabled engine registers no field, and so no control at all.
		outer := connect(ctx, t)
		plain := newFixtureEngine(ctx, t, outer, "plain", false)
		err := plain.fixture("report", "", nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "_remoteCacheFixture")
	})

	t.Run("MalformedRecords", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		b := newFixtureEngine(ctx, t, outer, "malformed", true)
		require.ErrorContains(t, b.fixture("reticulate", "", nil, nil), "unknown fixture operation")
		require.ErrorContains(t, b.fixture("gc", "record.json", nil, nil), "does not accept a path")
		require.ErrorContains(t, b.fixture("hold", "", nil, nil), "requires handles")
		for _, path := range []string{"/abs.json", "../escape.json", "a/../b.json"} {
			require.Error(t, b.fixture("barrierArm", path, nil, nil), path)
		}
		b.writeFile("control/unknown-field.json", `{"key":"k","point":"beforeFinish","action":"pause","extra":true}`)
		require.ErrorContains(t, b.fixture("barrierArm", "unknown-field.json", nil, nil), "unknown field")
		b.writeFile("control/trailing.json", `{"key":"k","point":"beforeFinish","action":"pause"} {}`)
		require.ErrorContains(t, b.fixture("barrierArm", "trailing.json", nil, nil), "trailing content")
		b.volumeExec(`mkdir -p /fixture/control; printf '%s' '{"key":"k","point":"beforeFinish","action":"pause"}' > /tmp-outside.json; ln -s /etc/hostname /fixture/control/escape.json`, nil, nil)
		require.Error(t, b.fixture("barrierArm", "escape.json", nil, nil), "a symlink out of the fixture root is not followed")
	})

	t.Run("BarrierActions", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "faults-a", true)
		b := newFixtureEngine(ctx, t, outer, "faults-b", true)

		// The closed action set, as a real engine reports it.
		require.ErrorContains(t, b.fixture("barrierArm", b.control("bad-action.json", dagql.FixtureBarrierRequest{Key: "k", Point: dagql.FixtureBeforeFinish, Action: "failAnything"}), nil, nil), "unknown fixture barrier action")
		require.ErrorContains(t, b.fixture("barrierArm", b.control("bad-pair.json", dagql.FixtureBarrierRequest{Key: "k", Point: dagql.FixtureBeforeFinish, Action: dagql.FixtureFailChainRead}), nil, nil), "legal only at")
		require.ErrorContains(t, b.fixture("barrierArm", b.control("bad-point.json", dagql.FixtureBarrierRequest{Key: "k", Point: "afterEverything", Action: dagql.FixturePause}), nil, nil), "unknown fixture barrier point")

		// One imported Directory per error action. Each fault fires once at a
		// real operation; the second read shows the real cleanup left the row
		// able to finish.
		faults := []struct {
			action dagql.FixtureBarrierAction
			point  dagql.FixtureBarrierPoint
			// bookkeeping faults follow a published install: the retry must
			// not read the chain again.
			bookkeeping bool
		}{
			{dagql.FixtureFailChainOpen, dagql.FixtureChainReaderOpen, false},
			{dagql.FixtureFailChainRead, dagql.FixtureChainRead, false},
			{dagql.FixtureFailChainClose, dagql.FixtureChainClose, false},
			{dagql.FixtureFailOwnerAttachBefore, dagql.FixtureBeforeOwnerAttach, true},
			{dagql.FixtureFailOwnerAttachAfter, dagql.FixtureAfterOwnerAttach, true},
		}
		handles := make([]string, len(faults))
		outputs := make([]map[string]any, len(faults))
		for i, fault := range faults {
			a.hostFile(fmt.Sprintf("d%d/notes.txt", i), "fault "+string(fault.action)+" "+identity.NewID())
			dir, err := a.client.Host().Directory(fmt.Sprintf("d%d", i)).Sync(ctx)
			require.NoError(t, err)
			id, err := dir.ID(ctx)
			require.NoError(t, err)
			handles[i] = string(id)
			outputs[i] = map[string]any{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}
		}
		var exported struct {
			Roots   []transferFixtureMapping `json:"roots"`
			Outputs []struct {
				Layers []struct {
					Size, CopiedBytes int64
				} `json:"layers"`
			} `json:"outputs"`
		}
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "faults.json", "outputs": outputs}), handles, &exported))
		// The mapping lists the whole exported closure, the shared Host row
		// included; the selected outputs are exactly the five addresses.
		require.GreaterOrEqual(t, len(exported.Roots), len(faults))
		require.Len(t, exported.Outputs, len(faults), "every selected address was exported")
		for _, output := range exported.Outputs {
			require.NotEmpty(t, output.Layers)
			for _, layer := range output.Layers {
				require.Equal(t, layer.Size, layer.CopiedBytes, "each unique layer was copied whole, once")
			}
		}
		a.copyFixtureTo(b, "faults.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "faults.json", nil, &imported))
		require.GreaterOrEqual(t, len(imported), len(faults))

		for i, fault := range faults {
			row := imported[i]
			require.Equal(t, "Directory", row.Type.NamedType)
			armed := b.armBarrier(dagql.FixtureBarrierRequest{Key: "fault-" + string(fault.action), Point: fault.point, Selector: dagql.FixtureBarrierSelector{ResultID: row.ResultID}, Action: fault.action})

			_, firstErr := dagger.Ref[*dagger.Directory](b.client, dagger.ID(row.Handle)).Entries(ctx)
			reached := armed.await(ctx, t)
			require.Equal(t, fault.point, reached.Event.Point)
			require.Equal(t, row.ResultID, reached.Event.ResultID)
			t.Logf("fixture fault %s at %s: first read err=%v", fault.action, fault.point, firstErr)

			var before fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &before))
			entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(row.Handle)).Entries(ctx)
			require.NoError(t, err, "after the one-shot %s the row finishes by its ordinary route", fault.action)
			require.Contains(t, entries, "notes.txt")
			var after fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &after))
			require.Len(t, partEventsOf(after.transferFixtureReport, row.ResultID, dagql.PartEventInstalledChain), 1, "the output was installed exactly once")
			require.Len(t, partEventsOf(after.transferFixtureReport, row.ResultID, dagql.PartEventSettled), 1)
			require.Empty(t, partEventsOf(after.transferFixtureReport, row.ResultID, dagql.PartEventLazyEnter))
			// The exhausted demand's error keeps the injected cause; an error
			// that had lost it would still read as an unavailable part.
			switch fault.action {
			case dagql.FixtureFailChainOpen:
				require.ErrorContains(t, firstErr, "imported filesystem part is unavailable")
				require.ErrorContains(t, firstErr, dagql.ErrFixtureTransport.Error())
			case dagql.FixtureFailChainRead:
				require.ErrorContains(t, firstErr, "imported filesystem part is unavailable")
				require.ErrorContains(t, firstErr, "unexpected EOF")
			case dagql.FixtureFailChainClose:
				require.NoError(t, firstErr, "a Close error after a fully read and verified blob does not fail the install")
			}
			if fault.bookkeeping {
				require.ErrorContains(t, firstErr, dagql.ErrFixtureLocalStorage.Error(), "a failed owner attachment fails that demand")
				require.Equal(t,
					len(partEventsOf(before.transferFixtureReport, row.ResultID, dagql.PartEventProviderRead)),
					len(partEventsOf(after.transferFixtureReport, row.ResultID, dagql.PartEventProviderRead)),
					"the retry completed bookkeeping only: no second content read")
			}
		}
	})

	t.Run("NoStaleBarrierOrHoldAfterRestart", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		b := newFixtureEngine(ctx, t, outer, "restart", true)
		b.hostFile("held/notes.txt", "held across nothing")
		dir, err := b.client.Host().Directory("held").Sync(ctx)
		require.NoError(t, err)
		id, err := dir.ID(ctx)
		require.NoError(t, err)
		var hold dagql.TransferFixtureHold
		require.NoError(t, b.fixture("hold", "", []string{string(id)}, &hold))
		never := b.armBarrier(dagql.FixtureBarrierRequest{Key: "never", Point: dagql.FixtureLazyEntry, Selector: dagql.FixtureBarrierSelector{ResultID: 1 << 60}, Action: dagql.FixturePause})
		var report fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &report))
		require.Equal(t, dagql.TransferFixtureControls{HoldTokens: 1, ArmedBarriers: 1}, report.Controls)

		b.restart()
		require.NoError(t, b.fixture("report", "", nil, &report))
		require.Equal(t, dagql.CachePersistenceResetNone, report.Persistence.PersistenceResetReason)
		require.Equal(t, dagql.TransferFixtureControls{}, report.Controls, "neither a hold token nor an armed barrier survives a restart")
		require.ErrorContains(t, never.release(), "not armed")
		require.ErrorContains(t, b.fixture("releaseHold", b.control("old-hold.json", map[string]any{"token": hold.Token}), nil, nil), "unknown fixture hold token")

		// The collection control runs the engine's real metadata collection
		// and numbers its own generations from one in each cache lifetime.
		var gc struct {
			Generation                      uint64
			SnapshotsBefore, SnapshotsAfter uint64
		}
		require.NoError(t, b.fixture("gc", "", nil, &gc))
		require.Equal(t, uint64(1), gc.Generation)
		require.Positive(t, gc.SnapshotsBefore)
		require.LessOrEqual(t, gc.SnapshotsAfter, gc.SnapshotsBefore)
	})

	t.Run("ObserverOverflow", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "overflow-a", true)
		b := newFixtureEngine(ctx, t, outer, "overflow-b", true)
		a.hostFile("src/notes.txt", "overflow "+identity.NewID())
		dir, err := a.client.Host().Directory("src").Sync(ctx)
		require.NoError(t, err)
		id, err := dir.ID(ctx)
		require.NoError(t, err)
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "overflow.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, nil))
		a.copyFixtureTo(b, "overflow.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "overflow.json", nil, &imported))

		require.NoError(t, b.fixture("observe", b.control("cap.json", map[string]any{"cap": 1}), nil, nil))
		_, err = dagger.Ref[*dagger.Directory](b.client, dagger.ID(imported[0].Handle)).Entries(ctx)
		require.NoError(t, err, "the bound never changes a request")
		err = b.fixture("report", "", nil, nil)
		require.ErrorContains(t, err, "fixture observation bound exceeded", "one install records several events against a bound of one")
		require.NoError(t, b.fixture("observe", b.control("cap.json", map[string]any{"cap": 4096}), nil, nil))
		require.NoError(t, b.fixture("report", "", nil, nil), "a new bound starts a new scenario")
	})

	t.Run("TransportShape", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		b := newFixtureEngine(ctx, t, outer, "transport", true)
		body := "origin body " + identity.NewID()
		b.writeFile("origins/data.txt", body)
		url := "https://" + fixturetransport.OriginHost + "/data.txt"
		var script struct{ Generation uint64 }
		require.NoError(t, b.fixture("transport", b.control("script.json", fixturetransport.Script{Responses: []fixturetransport.Response{{URL: url, BodyFile: "origins/data.txt", Headers: map[string]string{"ETag": `"v1"`}}}}), nil, &script))
		require.Equal(t, uint64(1), script.Generation)

		// An ordinary http call: its URL and identity are the caller's own.
		contents, err := b.client.HTTP(url).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, body, contents)
		_, err = b.client.HTTP("https://" + fixturetransport.OriginHost + "/missing.txt").Contents(ctx)
		require.Error(t, err, "an unknown fixture URL fails explicitly")
		require.Contains(t, err.Error(), "no scripted response")

		var report fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &report))
		require.NotNil(t, report.Transport)
		var served, refused bool
		for _, request := range report.Transport.Requests {
			switch {
			case request.URL == url && request.Status == 200 && request.Method == "GET":
				served = true
				require.Equal(t, int64(len(body)), request.BodyBytesRead, "the client read the actual file stream")
				require.True(t, request.Closed)
			case strings.HasSuffix(request.URL, "/missing.txt"):
				refused = true
				require.NotEmpty(t, request.Error)
			}
		}
		require.True(t, served, "the scripted origin request reached the dispatcher: %+v", report.Transport.Requests)
		require.True(t, refused)
		require.Equal(t, uint64(len(report.Transport.Requests)), report.Transport.FixtureHosts, "every fixture-host request was answered here; none was delegated")
	})

	t.Run("GitVisibility", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		b := newFixtureEngine(ctx, t, outer, "git", true)
		// A fixed bare repository and the advertisement its own upload-pack
		// produces, staged in B's fixture volume before B's first Git call.
		_, err := outer.Container().From(golangImage).
			WithExec([]string{"apk", "add", "git"}).
			WithMountedCache("/fixture", b.volume).
			WithEnvVariable("CACHEBUST", identity.NewID()).
			WithExec([]string{"sh", "-ec", `
				git config --global user.email dagger@example.com
				git config --global user.name "Dagger Tests"
				git config --global init.defaultBranch main
				mkdir -p /work /fixture/git /fixture/origins
				cd /work && git init -q && echo "fixture tree" > notes.txt && git add notes.txt && git commit -q -m first
				git clone -q --bare /work /fixture/git/repo.git
				{ printf '001e# service=git-upload-pack\n0000'; git upload-pack --advertise-refs --stateless-rpc /fixture/git/repo.git; } > /fixture/origins/info-refs
				chmod -R a+rX /fixture/git
			`}).Sync(ctx)
		require.NoError(t, err)
		repo := "https://" + fixturetransport.GitHost + "/repo.git"
		advertisement := repo + "/info/refs?service=git-upload-pack"
		require.NoError(t, b.fixture("transport", b.control("script.json", fixturetransport.Script{Responses: []fixturetransport.Response{{
			URL: advertisement, BodyFile: "origins/info-refs",
			Headers: map[string]string{"Content-Type": "application/x-git-upload-pack-advertisement", "Cache-Control": "no-cache"},
		}}}), nil, nil))

		// B's first ordinary Git selection of that stable URL.
		entries, err := b.client.Git(repo).Branch("main").Tree().Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "notes.txt")

		var report fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &report))
		require.NotNil(t, report.Transport)
		var probed bool
		for _, request := range report.Transport.Requests {
			if request.URL == advertisement && request.Status == 200 {
				probed = true
				require.Positive(t, request.BodyBytesRead, "the scripted advertisement was read and parsed")
			}
		}
		require.True(t, probed, "the visibility probe reached the dispatcher: %+v", report.Transport.Requests)
		require.Equal(t, uint64(len(report.Transport.Requests)), report.Transport.FixtureHosts, "no fixture-host request was delegated")

		_, err = b.client.Git("https://" + fixturetransport.GitHost + "/unknown.git").Branch("main").Tree().Entries(ctx)
		require.Error(t, err, "an unknown fixture repository fails without reaching the network")
	})
}
