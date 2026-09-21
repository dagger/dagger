package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// pipelineProbeSource extends the CacheProbe module (design §2). build takes
// an ordinary File and a variant, mounts the File in a normal Container, runs
// one deterministic command on a digest-pinned image, and returns two
// separately addressable output Directories. Each measured function records
// its body entry first. Design §2 step 6, an uncalled contextual method
// invoked on B through node(id:) after B's notes change and again after a
// restart, is carried by TestSchemaRecovery and TestSchemaRecoveryCold on the
// same module's Report (noteFile, noteDirectory), so Build has no summary.
var pipelineProbeSource = transferProbeSource + `
type Build struct { Variant string; Dirs []*dagger.Directory }
func(m *CacheProbe) Build(ctx context.Context, input *dagger.File,
 // +optional
 // +default="same"
 variant string,
)(*Build,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}
 ctr:=dag.Container().From("` + alpineImage + `").
  WithMountedFile("/input/data.json", input).
  WithEnvVariable("VARIANT", variant).
  WithExec([]string{"sh","-ec","mkdir -p /out/copy /out/manifest; cp /input/data.json /out/copy/data.json; printf 'variant=%s' \"$VARIANT\" > /out/manifest/manifest.txt"})
 return &Build{Variant:variant,Dirs:[]*dagger.Directory{ctr.Directory("/out/copy"),ctr.Directory("/out/manifest")}},nil
}
`

// pipelineScenario is the ordinary two-engine proof: identical module source
// and origin bytes on A and B, different checkouts, clients and engines.
type pipelineScenario struct {
	a, b  *fixtureEngine
	url   string
	input string
}

type pipelineBuild struct {
	ID      string
	Variant string
	Dirs    []struct{ ID string }
}

func newPipelineScenario(ctx context.Context, t *testctx.T, name string) *pipelineScenario {
	outer := connect(ctx, t)
	s := &pipelineScenario{
		a:     newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:     newFixtureEngine(ctx, t, outer, name+"-b", true),
		input: `{"input":"` + identity.NewID() + `"}`,
	}
	s.url = "https://" + fixturetransport.OriginHost + "/" + identity.NewID() + "/data.json"
	for _, e := range []*fixtureEngine{s.a, s.b} {
		e.hostFile("dagger.json", `{"name":"cache-probe","engineVersion":"latest","sdk":{"source":"go"},"source":".dagger"}`)
		e.hostFile(".dagger/main.go", pipelineProbeSource)
		e.hostFile("notes.txt", "operation notes")
		e.writeFile("origins/data.json", s.input)
		scriptOrigin(t, e, fixturetransport.Response{URL: s.url, BodyFile: "origins/data.json", Headers: map[string]string{"ETag": `"v1"`}})
	}
	return s
}

// build is the ordinary selection: the engine's own client, its own served
// Module and its own resolved http File.
func (s *pipelineScenario) build(ctx context.Context, t *testctx.T, e *fixtureEngine, variant string) pipelineBuild {
	t.Helper()
	inputID, err := e.client.HTTP(s.url).ID(ctx)
	require.NoError(t, err)
	var data struct {
		Probe struct{ Build pipelineBuild } `json:"cacheProbe"`
	}
	require.NoError(t, e.client.Do(ctx, &dagger.Request{Query: `query($input:ID!,$variant:String!){cacheProbe{build(input:$input,variant:$variant){id variant dirs{id}}}}`, Variables: map[string]any{"input": inputID, "variant": variant}}, &dagger.Response{Data: &data}))
	require.Equal(t, variant, data.Probe.Build.Variant)
	require.Len(t, data.Probe.Build.Dirs, 2)
	return data.Probe.Build
}

func pipelineBodies(t *testctx.T, e *fixtureEngine, function string) uint64 {
	t.Helper()
	var report transferFixtureReport
	require.NoError(t, e.fixture("report", "", nil, &report))
	var count uint64
	for _, body := range report.Bodies {
		if strings.EqualFold(body.Function, function) {
			count += body.Count
		}
	}
	return count
}

// export runs the pipeline once on A and exports the build's closure with
// dirs[0]'s selected chain; dirs[1] travels unselected.
func (s *pipelineScenario) export(ctx context.Context, t *testctx.T) fixtureExportSelectedResult {
	t.Helper()
	require.NoError(t, s.a.client.ModuleSource(".").AsModule().Serve(ctx))
	built := s.build(ctx, t, s.a, "same")
	require.Equal(t, uint64(1), pipelineBodies(t, s.a, "build"))
	// Explicitly sync the selected output on A before capture.
	_, err := dagger.Ref[*dagger.Directory](s.a.client, dagger.ID(built.Dirs[0].ID)).Sync(ctx)
	require.NoError(t, err)
	var exported fixtureExportSelectedResult
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export.json", map[string]any{"bundle": "pipeline.json", "outputs": []map[string]any{{"handle": built.Dirs[0].ID, "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{built.ID}, &exported))
	require.Len(t, exported.Outputs, 1, "the unselected sibling caused no export open")
	s.a.copyFixtureTo(s.b, "pipeline.json")
	return exported
}

func (s *pipelineScenario) importOnB(t *testctx.T) []transferFixtureMapping {
	t.Helper()
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "pipeline.json", nil, &imported))
	require.NotEmpty(t, imported)
	return imported
}

// rowOf resolves an ordinary returned ID to its row.
func rowOf(t *testctx.T, e *fixtureEngine, handle string) dagql.TransferFixtureRow {
	t.Helper()
	var report fixtureControlsReport
	require.NoError(t, e.fixture("report", "", []string{handle}, &report))
	require.Len(t, report.Rows, 1)
	return report.Rows[0]
}

// execEntries counts real exec-producer entries of the pipeline: lazy-enter
// events on withExec rows inside the dependency closure of the given rows.
// The SDK runtime's own imported execs, which a cold B may legitimately run to
// serve the Module, are outside that closure and are not the saved exec.
func execEntries(report fixtureControlsReport, roots ...uint64) int {
	deps := map[uint64][]uint64{}
	for _, row := range report.Rows {
		deps[row.ResultID] = row.DependencyIDs
	}
	closure := map[uint64]bool{}
	queue := append([]uint64{}, roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if closure[id] {
			continue
		}
		closure[id] = true
		queue = append(queue, deps[id]...)
	}
	n := 0
	for _, event := range report.Parts {
		if event.Kind == dagql.PartEventLazyEnter && event.Field == "withExec" && closure[event.ResultID] {
			n++
		}
	}
	return n
}

// dirRows resolves a build's two output Directories to their rows.
func (s *pipelineScenario) dirRows(t *testctx.T, built pipelineBuild) []uint64 {
	t.Helper()
	var rows []uint64
	for _, dir := range built.Dirs {
		rows = append(rows, rowOf(t, s.b, dir.ID).ResultID)
	}
	return rows
}

// hit asserts the headline: B's own ordinary selection returns the transferred
// result without entering the function, and a changed variant enters it once.
func (s *pipelineScenario) hit(ctx context.Context, t *testctx.T) pipelineBuild {
	t.Helper()
	// D and H: A's and B's own setups, from different checkouts and clients,
	// record the same Module source digest and the same resolved http File
	// identity. Neither is manufactured or copied from A.
	aModule, err := s.a.client.ModuleSource(".").Digest(ctx)
	require.NoError(t, err)
	bModule, err := s.b.client.ModuleSource(".").Digest(ctx)
	require.NoError(t, err)
	require.Equal(t, aModule, bModule, "D: the Module source digest")
	aInput, err := s.a.client.HTTP(s.url).Digest(ctx)
	require.NoError(t, err)
	bInput, err := s.b.client.HTTP(s.url).Digest(ctx)
	require.NoError(t, err)
	require.Equal(t, aInput, bInput, "H: the resolved http File identity")
	t.Logf("D=%s H=%s", bModule, bInput)

	built := s.build(ctx, t, s.b, "same")
	require.Zero(t, pipelineBodies(t, s.b, "build"), "B's ordinary call used the transferred result")
	row := rowOf(t, s.b, built.ID)
	require.True(t, row.Imported, "the returned row is the imported one, by its normal match")
	var metadata fixtureControlsReport
	require.NoError(t, s.b.fixture("report", "", nil, &metadata))
	require.Zero(t, execEntries(metadata, s.dirRows(t, built)...), "saved metadata runs nothing")
	return built
}

// TestPipeline is the ordinary two-engine proof (design §2): a Module function
// with a real File input and a real exec, exported by A and hit by B's own
// ordinary call.
func (RemoteCacheTransferSuite) TestPipeline(ctx context.Context, t *testctx.T) {
	// Warm order: B resolves its runtime, Module and http input first.
	t.Run("Warm", func(ctx context.Context, t *testctx.T) {
		s := newPipelineScenario(ctx, t, "pipeline-warm")
		s.export(ctx, t)
		require.NoError(t, s.b.client.ModuleSource(".").AsModule().Serve(ctx))
		_, err := s.b.client.HTTP(s.url).Sync(ctx)
		require.NoError(t, err)
		s.importOnB(t)
		built := s.hit(ctx, t)

		// The selected output arrives by its chain and the exec never runs.
		copied, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(built.Dirs[0].ID)).File("data.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.input, copied)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		dir0 := rowOf(t, s.b, built.Dirs[0].ID)
		t.Logf("dirs[0] row=%d imported=%t events: %v", dir0.ResultID, dir0.Imported, partKindsOf(report.transferFixtureReport, dir0.ResultID))
		require.Zero(t, execEntries(report, s.dirRows(t, built)...), "the saved exec never ran")
		require.Zero(t, pipelineBodies(t, s.b, "build"))

		// The changed-variant control proves this body is observable.
		s.build(ctx, t, s.b, "changed")
		require.Equal(t, uint64(1), pipelineBodies(t, s.b, "build"), "a changed variant enters the function once")
	})

	// Cold order: B imports first, with no prior AsModule, SDK warming, Serve
	// or origin resolution.
	t.Run("Cold", func(ctx context.Context, t *testctx.T) {
		s := newPipelineScenario(ctx, t, "pipeline-cold")
		exported := s.export(ctx, t)
		imported := s.importOnB(t)
		require.NoError(t, s.b.client.ModuleSource(".").AsModule().Serve(ctx))
		built := s.hit(ctx, t)
		copied, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(built.Dirs[0].ID)).File("data.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.input, copied)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		require.Zero(t, execEntries(report, s.dirRows(t, built)...), "the saved exec never ran")
		builtin := 0
		for _, event := range report.Parts {
			if event.Field == "_builtinContainer" && (event.Kind == dagql.PartEventInstalledReady || event.Kind == dagql.PartEventInstalledLazy || event.Kind == dagql.PartEventInstalledChain) {
				builtin++
				t.Logf("cold builtin route: row=%d %s %s", event.ResultID, event.Kind, event.Address.Part)
			}
		}
		require.Positive(t, builtin, "the cold SDK builtin filesystem was obtained by a recorded route")

		// Folded row, from core/schema's TestBuiltinMetadataSelectors: a file
		// and a directory selected from the builtin Container by absolute and
		// by relative path read on B what they read on A. The builtin row is
		// found in A's exported closure by its recorded call and on B by the
		// same transfer ordinal.
		var aRows fixtureControlsReport
		require.NoError(t, s.a.fixture("report", "", nil, &aRows))
		builtinRows := map[uint64]bool{}
		for _, row := range aRows.Rows {
			if row.Call != nil && row.Call.Field == "_builtinContainer" {
				builtinRows[row.ResultID] = true
			}
		}
		var aHandle, bHandle string
		for _, root := range exported.Roots {
			if builtinRows[root.ResultID] {
				aHandle = root.Handle
				for _, mapping := range imported {
					if mapping.Ordinal == root.Ordinal {
						bHandle = mapping.Handle
					}
				}
				break
			}
		}
		require.NotEmpty(t, aHandle, "the SDK builtin Container is in the exported closure")
		require.NotEmpty(t, bHandle)
		selections := func(client *dagger.Client, handle string) map[string]string {
			ctr := dagger.Ref[*dagger.Container](client, dagger.ID(handle))
			out := map[string]string{}
			release, err := ctr.File("/etc/os-release").Contents(ctx)
			require.NoError(t, err)
			out["absolute file"] = release
			entries, err := ctr.Directory("/etc/ssl").Entries(ctx)
			require.NoError(t, err)
			out["absolute directory"] = strings.Join(entries, ",")
			workdir, err := ctr.Workdir(ctx)
			require.NoError(t, err)
			out["workdir"] = workdir
			entries, err = ctr.Directory(".").Entries(ctx)
			require.NoError(t, err)
			out["relative directory"] = strings.Join(entries, ",")
			return out
		}
		want := selections(s.a.client, aHandle)
		require.NotEmpty(t, want["absolute file"])
		require.Equal(t, want, selections(s.b.client, bHandle), "builtin selections read on B what they read on A")
	})

	// The selected chain fails at its content reader: the retained exec runs
	// once on the private receiver, reading its exact imported File input,
	// and the bytes are the same. dirs[1] was never selected and its own
	// demand runs the same saved exec's other output without a second entry.
	// Design §2 step 5: a receiver-owned input does not depend on its donor.
	// B's own http File shares its snapshot with the imported input; then
	// B's File loses every owner and is collected, the origin is gone, the
	// output's chain fails, and the saved exec still runs from the imported
	// input it owns, before and after a clean restart.
	t.Run("DonorReleased", func(ctx context.Context, t *testctx.T) {
		for _, restart := range []bool{false, true} {
			name := map[bool]string{false: "BeforeRestart", true: "AfterRestart"}[restart]
			t.Run(name, func(ctx context.Context, t *testctx.T) { pipelineDonorReleased(ctx, t, restart) })
		}
	})

	t.Run("FailedChain", func(ctx context.Context, t *testctx.T) {
		t.Run("RetainedExec", pipelineRetainedExec)
		contentClassificationCases(t)
	})
}

func pipelineRetainedExec(ctx context.Context, t *testctx.T) {
	{
		s := newPipelineScenario(ctx, t, "pipeline-failed")
		s.export(ctx, t)
		require.NoError(t, s.b.client.ModuleSource(".").AsModule().Serve(ctx))
		_, err := s.b.client.HTTP(s.url).Sync(ctx)
		require.NoError(t, err)
		s.importOnB(t)
		built := s.hit(ctx, t)
		dir0 := rowOf(t, s.b, built.Dirs[0].ID)
		s.b.armBarrier(dagql.FixtureBarrierRequest{Key: "chain", Point: dagql.FixtureChainReaderOpen, Action: dagql.FixtureFailChainOpen})

		copied, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(built.Dirs[0].ID)).File("data.json").Contents(ctx)
		require.NoError(t, err, "the retained exec restores the output")
		require.Equal(t, s.input, copied)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("dirs[0] row=%d events: %v", dir0.ResultID, partKindsOf(report.transferFixtureReport, dir0.ResultID))
		for _, event := range report.Parts {
			if event.Kind == dagql.PartEventLazyEnter || strings.HasPrefix(string(event.Kind), "installed-") {
				t.Logf("  %s row=%d field=%s part=%s", event.Kind, event.ResultID, event.Field, event.Address.Part)
			}
		}
		require.Len(t, report.reachedAt(dagql.FixtureChainReaderOpen), 1, "the one offered chain was tried once")
		require.Equal(t, 1, execEntries(report, s.dirRows(t, built)...), "the saved exec ran exactly once")
		require.Zero(t, pipelineBodies(t, s.b, "build"), "the failed demand does not retry the enclosing function")

		manifest, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(built.Dirs[1].ID)).File("manifest.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "variant=same", manifest)
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		require.Equal(t, 1, execEntries(report, s.dirRows(t, built)...), "the sibling output came from the same run")
	}
}

func pipelineDonorReleased(ctx context.Context, t *testctx.T, restart bool) {
	s := newPipelineScenario(ctx, t, "pipeline-donor")
	b := s.b
	s.export(ctx, t)
	require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
	local, err := b.client.HTTP(s.url).Sync(ctx)
	require.NoError(t, err)
	localID, err := local.ID(ctx)
	require.NoError(t, err)
	donor := rowOf(t, b, string(localID))
	require.False(t, donor.Imported)
	require.Len(t, donor.SnapshotLinks, 1)

	// Walk every external Finish until the imported input's: its share is
	// then committed. The imported input is the imported File row in the
	// donor's output class, which exists once the import has united them.
	arm := func(i int) *armedBarrier {
		return b.armBarrier(dagql.FixtureBarrierRequest{Key: fmt.Sprintf("finish-%d", i), Point: dagql.FixtureBeforeFinish, Action: dagql.FixturePause})
	}
	armed := arm(0)
	imported := s.importOnB(t)
	classes := map[uint64]bool{}
	for _, class := range rowOf(t, b, string(localID)).OutputClasses {
		classes[class] = true
	}
	var inputHandle string
	var inputID uint64
	for _, mapping := range imported {
		if mapping.Type.NamedType != "File" {
			continue
		}
		for _, class := range rowOf(t, b, mapping.Handle).OutputClasses {
			if classes[class] && mapping.ResultID != donor.ResultID {
				inputHandle, inputID = mapping.Handle, mapping.ResultID
			}
		}
	}
	require.NotZero(t, inputID, "the imported input joined the donor's output class")
	for i := 0; ; i++ {
		require.Less(t, i, 64, "the imported input was never shared")
		reached := armed.await(ctx, t)
		found := reached.Event.ResultID == inputID
		held := armed
		if !found {
			armed = arm(i + 1)
		}
		require.NoError(t, held.release())
		if found {
			break
		}
	}
	built := s.hit(ctx, t)
	input := rowOf(t, b, inputHandle)
	require.True(t, input.Imported)
	require.Len(t, input.SnapshotLinks, 1, "the imported input owns a snapshot")
	require.Equal(t, donor.SnapshotLinks[0].RefKey, input.SnapshotLinks[0].RefKey, "it is the donor's exact snapshot, owned independently")

	// Release the donor: its saved edge if it has one, its session, then a
	// real collection. The origin is gone too.
	if donor.Persisted {
		var dropped []dagql.TransferFixtureDroppedRoot
		require.NoError(t, b.fixture("dropRetainedRoots", "", []string{string(localID)}, &dropped))
		require.Len(t, dropped, 1)
		require.True(t, dropped[0].Removed)
	}
	b.reconnect()
	require.NoError(t, b.fixture("gc", "", nil, nil))
	if restart {
		b.restart()
	} else {
		scriptOrigin(t, b)
	}
	var before fixtureControlsReport
	require.NoError(t, b.fixture("report", "", nil, &before))
	for _, row := range before.Rows {
		require.NotEqual(t, donor.ResultID, row.ResultID, "the donor is collected")
	}
	originBefore := uint64(0)
	if before.Transport != nil {
		originBefore = before.Transport.FixtureHosts
	}

	// A new reader with only the saved handles: the output's chain fails and
	// the saved exec runs from the imported input.
	dir0 := rowOf(t, b, built.Dirs[0].ID)
	b.armBarrier(dagql.FixtureBarrierRequest{Key: "chain", Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: dir0.ResultID}, Action: dagql.FixtureFailChainOpen})
	copied, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(built.Dirs[0].ID)).File("data.json").Contents(ctx)
	require.NoError(t, err, "a receiver-owned input does not depend on its donor or on the origin")
	require.Equal(t, s.input, copied)
	var after fixtureControlsReport
	require.NoError(t, b.fixture("report", "", nil, &after))
	require.Equal(t, 1, execEntries(after, dir0.ResultID), "the saved exec ran once")
	require.Empty(t, partEventsOf(after.transferFixtureReport, inputID, dagql.PartEventLazyEnter), "the input was not restored from anywhere: it was already owned")
	require.Empty(t, partEventsOf(after.transferFixtureReport, inputID, dagql.PartEventProviderRead))
	if after.Transport != nil {
		require.Equal(t, originBefore, after.Transport.FixtureHosts, "the origin was not resolved again")
	}
}
