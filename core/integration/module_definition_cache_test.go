package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ModuleDefinitionSuite struct{}

type servedSourceMap struct {
	Filename string
	Line     int
}

type servedFunction struct {
	Name        string
	Description string
	SourceMap   servedSourceMap
}

type servedObject struct {
	AsObject struct {
		Name        string
		Description string
		Functions   []servedFunction
	}
}

type served struct {
	ID      string
	Objects []servedObject
	Runtime struct{ ID string }
}

func functionNamesOf(objects []servedObject) []string {
	var names []string
	for _, obj := range objects {
		for _, fn := range obj.AsObject.Functions {
			names = append(names, obj.AsObject.Name+"."+fn.Name)
		}
	}
	return names
}

func TestModuleDefinitionSuite(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleDefinitionSuite{})
}

const moduleDefinitionProbeSource = `package main

// Probe answers with fixed strings.
type Probe struct{}

// Hello greets.
func (m *Probe) Hello() string { return "hello" }
`

const moduleDefinitionProbeSourceEdited = moduleDefinitionProbeSource + `
// Bye parts.
func (m *Probe) Bye() string { return "bye" }
`

// definitionComputedLine is the log line the engine writes when it computes
// a definition through the runtime (core/schema/modulesource.go).
const definitionComputedLine = "module definition computed"

// definitionEngine is one nested engine with its fixture volume, whose
// stderr the entrypoint appends to engine.log on that volume, so the test
// reads the engine's own log rather than what the outer client relays.
type definitionEngine struct {
	name     string
	volume   *dagger.CacheVolume
	service  *dagger.Service
	tunnel   *dagger.Service
	endpoint string
}

func startDefinitionEngine(ctx context.Context, t *testctx.T, outer *dagger.Client, name string) *definitionEngine {
	t.Helper()
	e := &definitionEngine{name: name, volume: outer.CacheVolume("module-definition-" + name + "-" + identity.NewID())}
	ctr := devEngineContainerWithStateKey(outer, "module-definition-"+name+"-state-"+identity.NewID(), func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithMountedCache("/transfer-fixture", e.volume).
			WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture").
			WithEntrypoint([]string{"sh", "-c", `exec /usr/local/bin/dagger-entrypoint.sh "$@" 2>>/transfer-fixture/engine.log`, "dagger-engine"})
	})
	// Retained rows must outlive the sessions that made them for the row
	// counts below to mean anything, independently of the host's disk
	// pressure; the predecessor's two-engine test pins the same bounds.
	ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
	e.service = devEngineContainerAsService(ctr)
	var err error
	e.tunnel, err = outer.Host().Tunnel(e.service).Start(ctx)
	require.NoError(t, err)
	e.endpoint, err = e.tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = e.tunnel.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		_, _ = e.service.Stop(ctx)
	})
	return e
}

func (e *definitionEngine) connect(ctx context.Context, t *testctx.T, dir string) *dagger.Client {
	t.Helper()
	client, err := dagger.Connect(ctx, dagger.WithRunnerHost(e.endpoint), dagger.WithWorkdir(dir), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	return client
}

// log returns the engine's log so far.
func (e *definitionEngine) log(ctx context.Context, t *testctx.T, outer *dagger.Client) string {
	t.Helper()
	out, err := outer.Container().From(alpineImage).WithMountedCache("/fixture", e.volume).
		WithEnvVariable("READ", identity.NewID()).
		WithExec([]string{"sh", "-c", "cat /fixture/engine.log 2>/dev/null || true"}).Stdout(ctx)
	require.NoError(t, err)
	return out
}

func (e *definitionEngine) logCount(ctx context.Context, t *testctx.T, outer *dagger.Client, line string) int {
	t.Helper()
	return strings.Count(e.log(ctx, t, outer), line)
}

// definitionRows returns the fixture report's rows for the _moduleDefinition
// field, keyed by result number, with the Container row each depends on.
type definitionRow struct {
	row     dagql.TransferFixtureRow
	runtime *dagql.TransferFixtureRow
}

func definitionRows(t *testctx.T, report transferFixtureReport) map[uint64]definitionRow {
	t.Helper()
	byID := map[uint64]dagql.TransferFixtureRow{}
	for _, row := range report.Rows {
		byID[row.ResultID] = row
	}
	out := map[uint64]definitionRow{}
	for _, row := range report.Rows {
		if row.Call == nil || row.Call.Field != "_moduleDefinition" {
			continue
		}
		d := definitionRow{row: row}
		for _, dep := range row.DependencyIDs {
			if depRow, ok := byID[dep]; ok && depRow.Call != nil && depRow.Call.Type != nil && depRow.Call.Type.NamedType == "Container" {
				depRow := depRow
				d.runtime = &depRow
			}
		}
		args, _ := json.Marshal(row.Call.Args)
		t.Logf("definition row %d imported=%t persisted=%t receiver=%v args=%s runtime=%v", row.ResultID, row.Imported, row.Persisted, row.Call.Receiver, args, d.runtime != nil)
		out[row.ResultID] = d
	}
	return out
}

func rowNumbers(rows map[uint64]definitionRow) []uint64 {
	var out []uint64
	for id := range rows {
		out = append(out, id)
	}
	return out
}

const servedQuery = `{moduleSource(refString:"."){asModule{id objects{asObject{name description functions{name description sourceMap{filename line}}}} runtime{id}}}}`

// A Go module's type definitions are a cached, content-keyed result. On one
// engine: the first client's load computes them through the runtime, a
// second client's load hits that row without running the runtime, a source
// edit makes a new row, and Dang modules add none. Across engines: the row
// travels in a bundle with its runtime, a cold engine's eager client hits it
// and still evaluates the runtime, and a function then runs in it.
func (ModuleDefinitionSuite) TestCachedAcrossClients(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	a := startDefinitionEngine(ctx, t, outer, "a")

	checkout := t.TempDir()
	writeProbe := func(source string) {
		require.NoError(t, os.MkdirAll(filepath.Join(checkout, ".dagger"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(checkout, "dagger.json"), []byte(`{"name":"probe","engineVersion":"latest","sdk":{"source":"go"},"source":".dagger"}`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(checkout, ".dagger", "main.go"), []byte(source), 0o644))
	}
	writeProbe(moduleDefinitionProbeSource)

	// load connects a fresh client to the engine with the checkout as its
	// working directory, serves the module, reads the served definition and
	// the runtime, and returns the cache's _moduleDefinition rows.
	load := func(t *testctx.T, e *definitionEngine, dir string) (served, map[uint64]definitionRow) {
		t.Helper()
		client := e.connect(ctx, t, dir)
		defer func() { require.NoError(t, client.Close()) }()
		require.NoError(t, client.ModuleSource(".").AsModule().Serve(ctx))
		var data struct {
			ModuleSource struct{ AsModule served }
		}
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: servedQuery}, &dagger.Response{Data: &data}))
		var report transferFixtureReport
		require.NoError(t, transferFixture(ctx, client, "report", "", []string{}, &report))
		rows := definitionRows(t, report)
		t.Logf("engine %s served %v runtime=%t definitions=%v", e.name, functionNamesOf(data.ModuleSource.AsModule.Objects), data.ModuleSource.AsModule.Runtime.ID != "", rowNumbers(rows))
		return data.ModuleSource.AsModule, rows
	}
	functionNames := func(s served) []string { return functionNamesOf(s.Objects) }

	first, firstRows := load(t, a, checkout)
	require.Len(t, firstRows, 1, "the first load computed one definition")
	require.Equal(t, []string{"Probe.hello"}, functionNames(first))
	require.Equal(t, "Probe answers with fixed strings.", first.Objects[0].AsObject.Description)
	hello := first.Objects[0].AsObject.Functions[0]
	require.Equal(t, "Hello greets.", hello.Description)
	require.NotEmpty(t, hello.SourceMap.Filename, "the Go SDK reports a source map for the function")
	require.Positive(t, hello.SourceMap.Line)
	require.NotEmpty(t, first.Runtime.ID, "Module.runtime resolves on the computing client")
	require.Equal(t, 1, a.logCount(ctx, t, outer, definitionComputedLine), "engine A ran the runtime once for the definition")

	second, secondRows := load(t, a, checkout)
	require.Equal(t, rowNumbers(firstRows), rowNumbers(secondRows), "the second client hit the same definition row and computed none")
	require.Equal(t, first.Objects, second.Objects, "the served objects, descriptions and source maps are equal")
	require.NotEmpty(t, second.Runtime.ID, "Module.runtime resolves on the hitting client")
	require.Equal(t, 1, a.logCount(ctx, t, outer, definitionComputedLine), "the second client's load did not run the runtime for the definition")

	writeProbe(moduleDefinitionProbeSourceEdited)
	third, thirdRows := load(t, a, checkout)
	require.ElementsMatch(t, []string{"Probe.hello", "Probe.bye"}, functionNames(third), "the edited source's definition is served")
	require.Len(t, thirdRows, 2, "a source edit is a new definition beside the retained old one")
	for id := range firstRows {
		require.Contains(t, thirdRows, id)
	}
	require.Equal(t, 2, a.logCount(ctx, t, outer, definitionComputedLine), "the edit's definition ran the runtime once")

	// A Dang module's SDK implements ModuleTypes, so its load takes that
	// branch and never reaches the cached-definition field: no row is added.
	// The non-container runtime fallback inside moduleDefViaRuntime has no
	// in-tree SDK to exercise it and is not tested here.
	dangDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dangDir, "dagger.json"), []byte(`{"name":"native","engineVersion":"latest","sdk":{"source":"dang"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dangDir, "main.dang"), []byte("type Native {\n  hello: String! { \"hi\" }\n}\n"), 0o644))
	native, nativeRows := load(t, a, dangDir)
	require.Equal(t, []string{"Native.hello"}, functionNames(native))
	require.ElementsMatch(t, rowNumbers(thirdRows), rowNumbers(nativeRows), "no definition row was added for a ModuleTypes SDK")

	// A module that calls itself (SELF_CALLS) loads and answers a real self
	// call on the same engine, and adds no definition row either.
	selfDir := t.TempDir()
	for _, name := range []string{"dagger.json", "main.dang"} {
		content, err := os.ReadFile(filepath.Join("testdata", "modules", "dang", "self-calls", name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(selfDir, name), content, 0o644))
	}
	selfClient := a.connect(ctx, t, selfDir)
	require.NoError(t, selfClient.ModuleSource(".").AsModule().Serve(ctx))
	var selfCall struct {
		Test struct{ PrintDefault string }
	}
	require.NoError(t, selfClient.Do(ctx, &dagger.Request{Query: `{test{printDefault}}`}, &dagger.Response{Data: &selfCall}))
	require.Equal(t, "Hello Self Calls", strings.TrimSpace(selfCall.Test.PrintDefault))
	var selfReport transferFixtureReport
	require.NoError(t, transferFixture(ctx, selfClient, "report", "", []string{}, &selfReport))
	require.ElementsMatch(t, rowNumbers(thirdRows), rowNumbers(definitionRows(t, selfReport)), "no definition row was added for the self-calling module")
	require.NoError(t, selfClient.Close())
	require.Equal(t, 2, a.logCount(ctx, t, outer, definitionComputedLine))

	// Export the edited module with its runtime's filesystem from A, as a
	// session report's bundle carries them, and import it into cold engine B.
	exporter := a.connect(ctx, t, checkout)
	var exported struct {
		ModuleSource struct{ AsModule served }
	}
	require.NoError(t, exporter.Do(ctx, &dagger.Request{Query: servedQuery}, &dagger.Response{Data: &exported}))
	var mapping []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, exporter, "definition.json", []string{exported.ModuleSource.AsModule.ID}, []string{exported.ModuleSource.AsModule.Runtime.ID}, &mapping))
	require.NotEmpty(t, mapping)
	require.NoError(t, exporter.Close())

	b := startDefinitionEngine(ctx, t, outer, "b")
	_, err := outer.Container().From(alpineImage).WithMountedCache("/source", a.volume).WithMountedCache("/destination", b.volume).
		WithEnvVariable("COPY", identity.NewID()).
		WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/definition.json /destination/bundles/definition.json; cp -a /source/blobs /destination/"}).Sync(ctx)
	require.NoError(t, err)
	importer := b.connect(ctx, t, checkout)
	var imported []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, importer, "import", "definition.json", []string{}, &imported))
	var importedReport transferFixtureReport
	require.NoError(t, transferFixture(ctx, importer, "report", "", []string{}, &importedReport))
	importedRows := definitionRows(t, importedReport)
	require.Len(t, importedRows, 1, "the bundle carried the edited definition")
	var importedDef definitionRow
	for _, d := range importedRows {
		importedDef = d
	}
	require.True(t, importedDef.row.Imported)
	require.NotNil(t, importedDef.runtime, "the definition's runtime row came with it")
	require.True(t, importedDef.runtime.Imported)
	require.NotEmpty(t, importedDef.runtime.Offers, "the runtime row offers its filesystem part")
	require.Zero(t, installedChains(t, importedReport, importedDef.runtime.ResultID), "nothing was downloaded at import")
	require.NoError(t, importer.Close())
	require.Equal(t, 0, b.logCount(ctx, t, outer, definitionComputedLine))

	// An eager client (dagger --eager-runtime) on B hits the imported
	// definition, so no runtime runs for it, and still gets the runtime
	// evaluated: its filesystem is installed from the bundle's download.
	eager, err := engineClientContainer(ctx, t, outer, b.service).
		WithDirectory("/work", outer.Host().Directory(checkout)).
		WithWorkdir("/work").
		WithExec([]string{"dagger", "functions", "--eager-runtime"}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}).Stdout(ctx)
	require.NoError(t, err)
	t.Logf("eager client on B listed:\n%s", eager)
	require.Contains(t, eager, "hello")
	require.Contains(t, eager, "bye")
	require.Equal(t, 0, b.logCount(ctx, t, outer, definitionComputedLine), "the eager client hit the imported definition")
	reporter := b.connect(ctx, t, checkout)
	var eagerReport transferFixtureReport
	require.NoError(t, transferFixture(ctx, reporter, "report", "", []string{}, &eagerReport))
	require.NoError(t, reporter.Close())
	require.Equal(t, 1, installedChains(t, eagerReport, importedDef.runtime.ResultID), "the eager client installed the runtime's filesystem from the bundle's download, not by rebuilding it")

	// A plain client on B then runs a function in that runtime: the call was
	// not exported, so its body runs, in the downloaded filesystem, with no
	// further download and still no definition computed.
	caller := b.connect(ctx, t, checkout)
	require.NoError(t, caller.ModuleSource(".").AsModule().Serve(ctx))
	var called struct {
		Probe struct{ Bye string }
	}
	require.NoError(t, caller.Do(ctx, &dagger.Request{Query: `{probe{bye}}`}, &dagger.Response{Data: &called}))
	require.Equal(t, "bye", called.Probe.Bye)
	var callerReport transferFixtureReport
	require.NoError(t, transferFixture(ctx, caller, "report", "", []string{}, &callerReport))
	require.Equal(t, rowNumbers(importedRows), rowNumbers(definitionRows(t, callerReport)), "B still has only the imported definition")
	require.Equal(t, 1, installedChains(t, callerReport, importedDef.runtime.ResultID), "the runtime's filesystem chain was installed once, by the eager client")
	require.NoError(t, caller.Close())
	require.Equal(t, 0, b.logCount(ctx, t, outer, definitionComputedLine))
}

// installedChains counts the fixture's installed-chain events for one row:
// parts installed from an offer's downloaded layer chain. A part the engine
// computed instead, or derived from one it already had, is recorded as
// installed-ready or installed-lazy. The events are the engine's own and
// cumulative, from the fixture's first operation on that engine.
func installedChains(t *testctx.T, report transferFixtureReport, resultID uint64) int {
	t.Helper()
	chains := 0
	for _, event := range report.Parts {
		if event.ResultID != resultID || !strings.HasPrefix(event.Kind, "installed-") {
			continue
		}
		t.Logf("row %d part event kind=%s field=%s part=%s", event.ResultID, event.Kind, event.Field, event.Address.Part)
		if event.Kind == "installed-chain" {
			chains++
		}
	}
	return chains
}
