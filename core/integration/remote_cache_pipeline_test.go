package core

import (
	"context"
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
// its body entry first; summary stays uncalled on A.
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
func(b *Build) Summary(ctx context.Context,
 // +defaultPath="notes.md"
 notes *dagger.File,
)(string,error){
 if err:=recordBody(ctx);err!=nil{return "",err}
 text,err:=notes.Contents(ctx); if err!=nil{return "",err}
 return b.Variant+":"+text,nil
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
		e.hostFile("notes.md", "summary notes")
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
func (s *pipelineScenario) export(ctx context.Context, t *testctx.T) {
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
	require.Zero(t, pipelineBodies(t, s.a, "summary"), "summary stays uncalled on A")
	s.a.copyFixtureTo(s.b, "pipeline.json")
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
		if event.Kind == "lazy-enter" && event.Field == "withExec" && closure[event.ResultID] {
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
		s.export(ctx, t)
		s.importOnB(t)
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
			if event.Field == "_builtinContainer" && (event.Kind == "installed-ready" || event.Kind == "installed-lazy" || event.Kind == "installed-chain") {
				builtin++
				t.Logf("cold builtin route: row=%d %s %s", event.ResultID, event.Kind, event.Address.Part)
			}
		}
		require.Positive(t, builtin, "the cold SDK builtin filesystem was obtained by a recorded route")
	})

	// The selected chain fails at its content reader: the retained exec runs
	// once on the private receiver, reading its exact imported File input,
	// and the bytes are the same. dirs[1] was never selected and its own
	// demand runs the same saved exec's other output without a second entry.
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
		require.NoError(t, s.b.fixture("barrierArm", s.b.control("chain.json", dagql.FixtureBarrierRequest{Key: "chain", Point: dagql.FixtureChainReaderOpen, Action: dagql.FixtureFailChainOpen}), nil, nil))

		copied, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(built.Dirs[0].ID)).File("data.json").Contents(ctx)
		require.NoError(t, err, "the retained exec restores the output")
		require.Equal(t, s.input, copied)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("dirs[0] row=%d events: %v", dir0.ResultID, partKindsOf(report.transferFixtureReport, dir0.ResultID))
		for _, event := range report.Parts {
			if event.Kind == "lazy-enter" || strings.HasPrefix(event.Kind, "installed-") {
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
