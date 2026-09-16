package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	enginecore "github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type RemoteCacheTransferSuite struct{}

func TestRemoteCacheTransferSuite(t *testing.T) {
	testctx.New(t, Middleware()[1:]...).RunTests(RemoteCacheTransferSuite{})
}

type transferFixtureMapping struct {
	Ordinal  dagql.TransferOrdinal `json:"ordinal"`
	ResultID uint64                `json:"resultID"`
	Type     *dagql.ResultCallType `json:"type"`
	Handle   string                `json:"handle"`
}
type transferFixtureReport struct {
	Persistence enginecore.RemoteCacheFixturePersistence `json:"persistence"`
	Parts       []dagql.TransferFixturePartEvent         `json:"parts"`
	Rows        []dagql.TransferFixtureRow               `json:"rows"`
	Bodies      []struct {
		Parent, Function, Client string
		Receiver, Count          uint64
	} `json:"bodies"`
}

func transferFixture(ctx context.Context, client *dagger.Client, op, path string, ids []string, out any) error {
	var data struct {
		Value string `json:"_remoteCacheFixture"`
	}
	err := client.Do(ctx, &dagger.Request{Query: `query($op:String!,$path:String!,$ids:[ID!]!){_remoteCacheFixture(operation:$op,path:$path,ids:$ids)}`, Variables: map[string]any{"op": op, "path": path, "ids": ids}}, &dagger.Response{Data: &data})
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data.Value), out)
}

func transferFixtureSelected(ctx context.Context, client *dagger.Client, path string, ids, outputs []string, out any) error {
	var data struct {
		Value string `json:"_remoteCacheFixture"`
	}
	err := client.Do(ctx, &dagger.Request{Query: `query($path:String!,$ids:[ID!]!,$outputs:[ID!]!){_remoteCacheFixture(operation:"export",path:$path,ids:$ids,outputIDs:$outputs)}`, Variables: map[string]any{"path": path, "ids": ids, "outputs": outputs}}, &dagger.Response{Data: &data})
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data.Value), out)
}

const transferProbeSource = `package main
import (
 "context"
 "dagger/cache-probe/internal/dagger"
 "github.com/Khan/genqlient/graphql"
)
type CacheProbe struct{}
type Code string
type Status string
const Ready Status = "READY"
type Report struct { Seed string; Code Code; Status Status; Platform dagger.Platform; Artifact *dagger.Directory }
type Named interface { DaggerObject; Seed(context.Context)(string,error) }
func(m *CacheProbe) Echo(ctx context.Context, item Named)(Named,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}; return item,nil
}
func(r *Report) WithSeed(ctx context.Context, seed string)(*Report,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}; copy:=*r;copy.Seed=seed;return &copy,nil
}
func(r *Report) Label(ctx context.Context)(string,error){
 if err:=recordBody(ctx);err!=nil{return "",err};return "label:"+r.Seed,nil
}
func recordBody(ctx context.Context) error {
 return dag.GraphQLClient().MakeRequest(ctx,&graphql.Request{Query: "{_remoteCacheFixture(operation:\"recordBody\")}"},&graphql.Response{})
}
func(m *CacheProbe) Report(ctx context.Context, seed string)(*Report,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}
 return &Report{Seed:seed,Code:Code(seed),Status:Ready,Platform:dagger.Platform("linux/amd64"),Artifact:dag.Directory().WithNewFile("payload.txt", "selected artifact:"+seed)},nil
}
func(r *Report) NoteFile(ctx context.Context,
 // +defaultPath="notes.txt"
 file *dagger.File,
)(string,error){
 if err:=recordBody(ctx);err!=nil{return "",err}
 return file.Contents(ctx)
}
func(r *Report) NoteDirectory(ctx context.Context,
 // +defaultPath="."
 directory *dagger.Directory,
)(string,error){
 if err:=recordBody(ctx);err!=nil{return "",err}
 return directory.File("notes.txt").Contents(ctx)
}
`

func (RemoteCacheTransferSuite) TestSchemaRecovery(ctx context.Context, t *testctx.T) {
	runTransferSchemaRecovery(ctx, t, false, false)
}

func (RemoteCacheTransferSuite) TestSchemaRecoveryCold(ctx context.Context, t *testctx.T) {
	runTransferSchemaRecovery(ctx, t, true, false)
}

//nolint:gocyclo // one assertion block per recovery scenario; splitting hides the order of the scenarios
func runTransferSchemaRecovery(ctx context.Context, t *testctx.T, cold, defaultGC bool) {
	outer := connect(ctx, t)
	newCheckout := func(t *testctx.T) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".dagger"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger.json"), []byte(`{"name":"cache-probe","engineVersion":"latest","sdk":{"source":"go"},"source":".dagger"}`), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".dagger", "main.go"), []byte(transferProbeSource), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("operation notes"), 0644))
		return dir
	}
	type running struct {
		upstream, tunnel *dagger.Service
		client           *dagger.Client
		endpoint         string
	}
	stop := func(t *testctx.T, e *running) {
		t.Helper()
		if e.client != nil {
			require.NoError(t, e.client.Close())
			e.client = nil
		}
		if e.upstream != nil {
			_, err := e.upstream.Stop(ctx)
			require.NoError(t, err)
			e.upstream = nil
		}
		if e.tunnel != nil {
			_, err := e.tunnel.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			require.NoError(t, err)
			e.tunnel = nil
		}
	}
	start := func(t *testctx.T, state string, volume *dagger.CacheVolume, checkout string) *running {
		ctr := devEngineContainerWithStateKey(outer, state, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		if !defaultGC {
			// Match the persistence suite's bounds when measuring retention
			// across restart independently of host disk pressure.
			ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
		}
		e := &running{upstream: devEngineContainerAsService(ctr)}
		var err error
		e.tunnel, err = outer.Host().Tunnel(e.upstream).Start(ctx)
		require.NoError(t, err)
		e.endpoint, err = e.tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		e.client, err = dagger.Connect(ctx, dagger.WithRunnerHost(e.endpoint), dagger.WithWorkdir(checkout), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		return e
	}
	var lastArtifactID string
	callReport := func(t *testctx.T, client *dagger.Client, seed string) string {
		var data struct {
			Probe struct {
				Report struct {
					ID                           string
					Seed, Code, Status, Platform string
					Artifact                     struct {
						ID   string
						File struct{ Contents string }
					}
				}
			} `json:"cacheProbe"`
		}
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($seed:String!){cacheProbe{report(seed:$seed){id seed code status platform artifact{id file(path:"payload.txt"){contents}}}}}`, Variables: map[string]any{"seed": seed}}, &dagger.Response{Data: &data}))
		require.Equal(t, seed, data.Probe.Report.Seed)
		require.Equal(t, seed, data.Probe.Report.Code)
		require.Equal(t, "READY", data.Probe.Report.Status)
		require.Equal(t, "linux/amd64", data.Probe.Report.Platform)
		require.Equal(t, "selected artifact:"+seed, data.Probe.Report.Artifact.File.Contents)
		lastArtifactID = data.Probe.Report.Artifact.ID
		return data.Probe.Report.ID
	}
	countBody := func(t *testctx.T, client *dagger.Client, function string) uint64 {
		var report transferFixtureReport
		require.NoError(t, transferFixture(ctx, client, "report", "", []string{}, &report))
		var count uint64
		for _, body := range report.Bodies {
			if strings.EqualFold(body.Function, function) {
				count += body.Count
			}
		}
		return count
	}
	aDir := newCheckout(t)
	aVolume := outer.CacheVolume("b2-transfer-a-" + identity.NewID())
	a := start(t, "b2-transfer-a-state-"+identity.NewID(), aVolume, aDir)
	defer stop(t, a)
	require.NoError(t, a.client.ModuleSource(".").AsModule().Serve(ctx))
	aID := callReport(t, a.client, "same")
	require.Equal(t, uint64(1), countBody(t, a.client, "report"))
	var source []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, a.client, "report.json", []string{aID}, []string{lastArtifactID}, &source))
	require.NotEmpty(t, source)
	// The same contextual call succeeds on the native object without import.
	var native struct{ Node struct{ NoteFile string } }
	require.NoError(t, a.client.Do(ctx, &dagger.Request{Query: `query($id:ID!){node(id:$id){... on CacheProbeReport{noteFile}}}`, Variables: map[string]any{"id": aID}}, &dagger.Response{Data: &native}))
	require.Equal(t, "operation notes", native.Node.NoteFile)
	orders := []string{"before", "after"}
	if cold {
		orders = []string{"before"}
	}
	if defaultGC {
		orders = []string{"after"}
	}
	for _, order := range orders {
		t.Run(order, func(ctx context.Context, t *testctx.T) {
			bDir := newCheckout(t)
			bVolume := outer.CacheVolume("b2-transfer-b-" + identity.NewID())
			bState := "b2-transfer-b-state-" + identity.NewID()
			b := start(t, bState, bVolume, bDir)
			defer func() { stop(t, b) }()
			var operational *dagger.Module
			var err error
			if !cold {
				operational = b.client.ModuleSource(".").AsModule()
				operational, err = operational.Sync(ctx)
				require.NoError(t, err)
				// Schema Files now use FileBlobLazy, so warming the SDK no longer
				// evaluates an ordinary empty Directory as an incidental input.
				// Warm that local donor explicitly for both import orders.
				warmScratch, err := b.client.Directory().Sync(ctx)
				require.NoError(t, err)
				warmScratchID, err := warmScratch.ID(ctx)
				require.NoError(t, err)
				var warmReport transferFixtureReport
				require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{string(warmScratchID)}, &warmReport))
				require.Len(t, warmReport.Rows, 1)
				warmRow := warmReport.Rows[0]
				require.False(t, warmRow.Imported)
				require.NotNil(t, warmRow.Call)
				require.Equal(t, "directory", warmRow.Call.Field)
				require.Nil(t, warmRow.Call.Receiver)
				require.Len(t, warmRow.SnapshotLinks, 1)
				require.Equal(t, "snapshot", warmRow.SnapshotLinks[0].Role)
				require.NotEmpty(t, warmRow.SnapshotLinks[0].RefKey)
				t.Logf("acquisition warm scratch donor row=%d ref=%s order=%s", warmRow.ResultID, warmRow.SnapshotLinks[0].RefKey, order)
			}
			require.Equal(t, uint64(0), countBody(t, b.client, "report"))
			if order == "after" {
				require.NoError(t, operational.Serve(ctx))
			}
			_, err = outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
				WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/report.json /destination/bundles/report.json; cp -a /source/blobs /destination/"}).Sync(ctx)
			require.NoError(t, err)
			var imported []transferFixtureMapping
			require.NoError(t, transferFixture(ctx, b.client, "import", "report.json", []string{}, &imported))
			t.Logf("acquisition imported closure rows=%d cold=%t order=%s", len(imported), cold, order)
			require.Equal(t, uint64(0), countBody(t, b.client, "report"))
			if order == "before" {
				if cold {
					operational = b.client.ModuleSource(".").AsModule()
				}
				require.NoError(t, operational.Serve(ctx))
			}
			saved := callReport(t, b.client, "same")
			var acquisition transferFixtureReport
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &acquisition))
			counters := map[string]int{}
			builtinRoute := 0
			for _, event := range acquisition.Parts {
				counters[event.Kind]++
				if event.Field == "_builtinContainer" && (event.Kind == "installed-ready" || event.Kind == "installed-lazy") {
					builtinRoute++
				}
			}
			require.Positive(t, counters["provider-read"], "selected artifact must read its transferred chain")
			require.Positive(t, counters["installed-chain"])
			require.Positive(t, counters["owner-sync"])
			require.Positive(t, counters["settled"])
			if cold {
				require.Positive(t, builtinRoute, "cold SDK builtin FS must acquire a local equivalent or invoke its saved builtin")
				assertColdPartDelegation(t, acquisition)
			}
			t.Logf("acquisition route counters cold=%t builtin=%d counts=%v", cold, builtinRoute, counters)
			var echoed struct {
				Probe struct{ Echo struct{ Seed string } } `json:"cacheProbe"`
			}
			require.NoError(t, b.client.Do(ctx, &dagger.Request{Query: `query($id:ID!){cacheProbe{echo(item:$id){seed}}}`, Variables: map[string]any{"id": saved}}, &dagger.Response{Data: &echoed}))
			require.Equal(t, "same", echoed.Probe.Echo.Seed)
			require.Equal(t, uint64(1), countBody(t, b.client, "echo"))
			require.Equal(t, uint64(0), countBody(t, b.client, "report"), "ordinary call must use transferred result")
			callReport(t, b.client, "different")
			require.Equal(t, uint64(1), countBody(t, b.client, "report"), "changed argument enters function")
			t.Logf("acquisition changed-argument control cold=%t report-body-count=1", cold)
			// The scratch demand happens inside the changed-argument body, after
			// the earlier acquisition report was captured.
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &acquisition))
			scratchHandle := assertScratchAcquisition(t, acquisition, imported, cold)
			entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(scratchHandle)).Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, entries)
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &acquisition))
			assertScratchAcquisition(t, acquisition, imported, cold)
			reportType := ""
			for _, value := range imported {
				if strings.HasSuffix(value.Type.NamedType, "Report") {
					reportType = value.Type.NamedType
					break
				}
			}
			require.NotEmpty(t, reportType)
			loadNotes := func(method, notes string) {
				require.NoError(t, os.WriteFile(filepath.Join(bDir, "notes.txt"), []byte(notes), 0644))
				var data struct{ Node map[string]string }
				require.NoError(t, b.client.Do(ctx, &dagger.Request{Query: fmt.Sprintf(`query($id:ID!){node(id:$id){... on %s{%s}}}`, reportType, method+" platform status"), Variables: map[string]any{"id": saved}}, &dagger.Response{Data: &data}))
				require.Equal(t, notes, data.Node[method])
				require.Equal(t, "linux/amd64", data.Node["platform"])
				require.Equal(t, "READY", data.Node["status"])
			}
			loadNotes("noteFile", "consumer file notes")
			transferBoundToolControl(ctx, t, b.client, saved)
			var checkpoint transferFixtureReport
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{saved}, &checkpoint))
			require.Len(t, checkpoint.Rows, 1)
			require.True(t, checkpoint.Rows[0].Imported)
			require.True(t, checkpoint.Rows[0].Persisted)
			t.Logf("acquisition later controls cold=%t noteFile=passed bound-tool=passed saved-row=%d persisted=true", cold, checkpoint.Rows[0].ResultID)
			if defaultGC {
				// A bounded temporary file outside engine state supplies actual disk
				// pressure; the default policy and imported root remain unchanged.
				pressure := outer.Container().From(alpineImage).WithMountedCache("/fixture", bVolume)
				defer func() {
					_, err := pressure.WithEnvVariable("CLEANUP", identity.NewID()).WithExec([]string{"rm", "-f", "/fixture/gc-pressure"}).Sync(context.WithoutCancel(ctx))
					require.NoError(t, err)
				}()
				minimum, err := b.client.Engine().LocalCache().MinFreeSpace(ctx)
				require.NoError(t, err)
				require.Positive(t, minimum, "default disk policy must define its free-space target")
				stats, err := pressure.WithEnvVariable("STAT", identity.NewID()).WithExec([]string{"stat", "-f", "-c", "%a %S", "/fixture"}).Stdout(ctx)
				require.NoError(t, err)
				var blocks, blockSize int64
				_, err = fmt.Sscanf(stats, "%d %d", &blocks, &blockSize)
				require.NoError(t, err)
				available := blocks * blockSize
				pressureBytes := max(int64(0), available-int64(minimum)) + 2<<30
				t.Logf("default policy pressure: minFree=%d available=%d temporaryBytes=%d cap=%d", minimum, available, pressureBytes, int64(8<<30))
				if pressureBytes > 8<<30 {
					t.Skip("default-policy diagnostic would exceed its 8 GiB temporary disk allocation cap")
				}
				count := (pressureBytes + (1 << 20) - 1) / (1 << 20)
				_, err = pressure.WithEnvVariable("PRESSURE", identity.NewID()).WithExec([]string{"dd", "if=/dev/zero", "of=/fixture/gc-pressure", "bs=1048576", "count=" + strconv.FormatInt(count, 10), "conv=fsync"}).Sync(ctx)
				require.NoError(t, err)
			}
			stop(t, b)
			b = start(t, bState, bVolume, bDir)
			var restored transferFixtureReport
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &restored))
			t.Logf("restart persistence diagnostics: %+v", restored.Persistence)
			require.Equal(t, dagql.CachePersistenceResetNone, restored.Persistence.PersistenceResetReason)
			require.Empty(t, restored.Persistence.LocalCacheResetReason)
			if defaultGC {
				root := checkpoint.Rows[0].ResultID
				t.Logf("default policy saved report: resultID=%d persisted=%t beforeRemoved=%d afterRemoved=%d", root, checkpoint.Rows[0].Persisted, checkpoint.Persistence.RemovedPersistedRootCount, restored.Persistence.RemovedPersistedRootCount)
				if restored.Persistence.RemovedPersistedRootCount == checkpoint.Persistence.RemovedPersistedRootCount {
					retained := false
					for _, row := range restored.Rows {
						retained = retained || (row.ResultID == root && row.Imported && row.Persisted)
					}
					require.True(t, retained, "saved root lost without a reset or a prune decision")
					t.Skip("default disk-derived policy did not remove a persisted root under current host pressure")
				}
				require.Greater(t, restored.Persistence.RemovedPersistedRootCount, checkpoint.Persistence.RemovedPersistedRootCount)
				decisions := restored.Persistence.DiskPrunedResults[len(checkpoint.Persistence.DiskPrunedResults):]
				require.Contains(t, decisions, fmt.Sprintf("dagql.result.%d", root))
				for _, row := range restored.Rows {
					require.NotEqual(t, root, row.ResultID, "pruned saved report must be absent after restart")
				}
				return
			}
			require.Zero(t, restored.Persistence.RemovedPersistedRootCount)
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{saved}, &restored), "saved row must be present immediately after clean restart")
			require.Len(t, restored.Rows, 1)
			require.True(t, restored.Rows[0].Persisted)
			// Module-aware request installs the consumer's current operational Module.
			require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
			loadNotes("noteDirectory", "consumer directory notes after restart")
			require.Equal(t, uint64(1), countBody(t, b.client, "report"))
			t.Logf("acquisition clean-restart control cold=%t saved-row=%d noteDirectory=passed report-body-count=1", cold, restored.Rows[0].ResultID)
			// Create a higher native comparison row after import, then read its
			// result from a client with no installed module candidates.
			writer, err := dagger.Connect(ctx, dagger.WithRunnerHost(b.endpoint), dagger.WithWorkdir(bDir))
			require.NoError(t, err)
			defer writer.Close()
			nativeModule := writer.ModuleSource(".").AsModule().WithDescription("native recorded control")
			require.NoError(t, nativeModule.Serve(ctx))
			nativeID := callReport(t, writer, "native recorded control")
			var graph transferFixtureReport
			require.NoError(t, transferFixture(ctx, writer, "report", "", []string{}, &graph))
			decoded := new(call.ID)
			require.NoError(t, decoded.Decode(nativeID))
			var recorded uint64
			for _, row := range graph.Rows {
				if row.ResultID == decoded.EngineResultID() {
					require.NotNil(t, row.Call.Module)
					recorded = row.Call.Module.ResultRef.ResultID
				}
			}
			require.NotZero(t, recorded)
			var nativeRow dagql.TransferFixtureRow
			for _, row := range graph.Rows {
				if row.ResultID == recorded {
					nativeRow = row
				}
			}
			require.False(t, nativeRow.Imported)
			var lowerEquivalent bool
			for _, row := range graph.Rows {
				if !row.Imported || row.ResultID >= recorded {
					continue
				}
				for _, a := range row.OutputClasses {
					for _, b := range nativeRow.OutputClasses {
						lowerEquivalent = lowerEquivalent || a == b
					}
				}
			}
			require.True(t, lowerEquivalent, "the eligible native recorded Module has a lower imported equivalent")
			bare, err := dagger.Connect(ctx, dagger.WithRunnerHost(b.endpoint), dagger.WithWorkdir(bDir))
			require.NoError(t, err)
			defer bare.Close()
			text := transferContextTool(ctx, t, bare, nativeID)
			require.Contains(t, text, "consumer directory notes after restart")
			require.NotContains(t, text, "local module context belongs to another engine")
			t.Logf("acquisition native-recorded-module control cold=%t recorded-row=%d lower-imported-equivalent=true passed", cold, recorded)
		})
	}

	if defaultGC {
		return
	}
	t.Run("foreign context", func(ctx context.Context, t *testctx.T) {
		volume := outer.CacheVolume("b2-transfer-foreign-" + identity.NewID())
		foreign := start(t, "b2-transfer-foreign-state-"+identity.NewID(), volume, newCheckout(t))
		defer stop(t, foreign)
		// This bare client also uses addendum 2's runtime preparation; it never
		// serves the Module, so schema recovery has no installed candidates.
		_, err := foreign.client.ModuleSource(".").AsModule().Sync(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(0), countBody(t, foreign.client, "report"))
		raw, err := outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).
			WithEnvVariable("READ", identity.NewID()).WithExec([]string{"cat", "/source/bundles/report.json"}).Stdout(ctx)
		require.NoError(t, err)
		var bundle dagql.ValueBundle
		require.NoError(t, json.Unmarshal([]byte(raw), &bundle))
		require.Len(t, bundle.Roots, 1)
		root := bundle.Roots[0].Ordinal
		importBundle := func(name string) []transferFixtureMapping {
			t.Helper()
			encoded, err := json.Marshal(bundle)
			require.NoError(t, err)
			_, err = outer.Container().From(alpineImage).WithMountedCache("/destination", volume).
				WithNewFile("/bundle.json", string(encoded)).WithEnvVariable("WRITE", identity.NewID()).
				WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /bundle.json /destination/bundles/" + name}).Sync(ctx)
			require.NoError(t, err)
			var imported []transferFixtureMapping
			require.NoError(t, transferFixture(ctx, foreign.client, "import", name, []string{}, &imported))
			return imported
		}
		assertForeign := func(imported []transferFixtureMapping) {
			t.Helper()
			var handle string
			for _, value := range imported {
				if value.Ordinal == root {
					handle = value.Handle
				}
			}
			require.NotEmpty(t, handle)
			require.Contains(t, transferContextTool(ctx, t, foreign.client, handle), "local module context belongs to another engine")
			require.Equal(t, uint64(0), countBody(t, foreign.client, "noteDirectory"))
		}
		// No installed equivalent: the frame explicitly names an imported Module.
		assertForeign(importBundle("explicit.json"))
		var recordedModule uint64
		for i := range bundle.Values {
			value := &bundle.Values[i]
			if value.Ordinal != root {
				continue
			}
			require.NotNil(t, value.Record.Call.Module)
			recordedModule = value.Record.Call.Module.ResultRef.ResultID
			// Keep this root distinct so ordinary node loading retains this frame.
			var changed bool
			for _, arg := range value.Record.Call.Args {
				if arg.Name == "seed" {
					arg.Value.StringValue = "expired module"
					changed = true
				}
			}
			require.True(t, changed)
			value.Record.Call.ExtraDigests = nil
		}
		require.NotZero(t, recordedModule)
		for i := range bundle.Values {
			if uint64(bundle.Values[i].Ordinal) == recordedModule {
				bundle.Values[i].ExpiresAtUnix = time.Now().Add(-time.Hour).Unix()
			}
		}
		// The recorded row is ineligible; its earlier imported equivalent is used.
		assertForeign(importBundle("expired.json"))
	})
}

func assertScratchAcquisition(t *testctx.T, report transferFixtureReport, imported []transferFixtureMapping, cold bool) string {
	t.Helper()
	mapping := map[uint64]transferFixtureMapping{}
	for _, value := range imported {
		mapping[value.ResultID] = value
	}
	var scratch *dagql.TransferFixtureRow
	for i := range report.Rows {
		row := &report.Rows[i]
		if _, ok := mapping[row.ResultID]; !ok || !row.Imported || row.Call == nil || row.Call.Type.NamedType != "Directory" || row.Call.Field != "directory" || row.Call.Receiver != nil {
			continue
		}
		require.Nil(t, scratch, "ambiguous imported Query.directory row")
		scratch = row
	}
	require.NotNil(t, scratch)
	platform := ""
	for _, input := range scratch.Call.ImplicitInputs {
		if input.Name == "engineDefaultPlatform" {
			platform = input.Value.StringValue
		}
	}
	require.Equal(t, "linux/amd64", platform)
	entries := 0
	for _, event := range report.Parts {
		if event.ResultID != scratch.ResultID {
			continue
		}
		require.NotEqual(t, "provider-read", event.Kind)
		require.NotEqual(t, "selected-chain", event.Kind)
		if event.Kind == "lazy-enter" {
			require.Equal(t, dagql.PersistedPartAddress{Part: "snapshot"}, event.Address)
			entries++
		}
	}
	want := 0
	if cold {
		want = 1
	}
	require.Equal(t, want, entries, "scratch saved-operation entries")
	t.Logf("acquisition scratch cold=%t ordinal=%d row=%d address={\"part\":\"snapshot\"} group=%s platform=%s lazy-enter=%d provider-reads=0", cold, mapping[scratch.ResultID].Ordinal, scratch.ResultID, dagql.LazyGroupWhole, platform, entries)
	return mapping[scratch.ResultID].Handle
}

// Keep acquisition hops separate from body entries. Every observation is
// keyed by the exact row and full address, not an aggregate field-name count.
func assertColdPartDelegation(t *testctx.T, report transferFixtureReport) {
	t.Helper()
	rows := map[uint64]dagql.TransferFixtureRow{}
	for _, row := range report.Rows {
		rows[row.ResultID] = row
	}
	type key struct {
		row     uint64
		address string
	}
	keyOf := func(event dagql.TransferFixturePartEvent) key {
		raw, err := json.Marshal(event.Address)
		require.NoError(t, err)
		return key{event.ResultID, string(raw)}
	}
	selected, installed, operations := map[key]int{}, map[key]int{}, map[key]int{}
	fsFields := map[string]int{}
	mountWriters := map[string]int{}
	for _, event := range report.Parts {
		k := keyOf(event)
		if event.Kind == "lazy-enter" {
			operations[k]++
		}
		if event.Kind != "selected-delegation" && event.Kind != "installed-delegation" {
			require.Nil(t, event.Source, "ordinary events cannot carry delegation provenance")
			continue
		}
		require.NotNil(t, event.Source)
		row := rows[event.ResultID]
		require.NotNil(t, row.Call)
		require.NotNil(t, row.Call.Receiver)
		require.Equal(t, row.Call.Receiver.ResultID, event.Source.ResultID)
		require.Contains(t, row.DependencyIDs, event.Source.ResultID)
		require.Equal(t, event.Address.Part, event.Source.Address.Part)
		require.Empty(t, event.Source.Address.OutputPath)
		require.NotEqual(t, dagql.PartKey("metadata"), event.Address.Part)
		if event.Kind == "selected-delegation" {
			selected[k]++
			continue
		}
		installed[k]++
		if event.Address.Part == "fs" {
			fsFields[event.Field]++
		}
		t.Logf("acquisition delegation target=%d address=%s field=%s parent=%d source=%+v", event.ResultID, k.address, event.Field, event.Source.ResultID, event.Source.Address)
	}
	for k, count := range installed {
		require.Equal(t, 1, count, "duplicate delegation installation: %+v", k)
		require.Positive(t, selected[k])
		for p, bodies := range operations {
			if p.row == k.row {
				require.Zero(t, bodies, "metadata-only child entered a Lazy operation: %+v", p)
			}
		}
	}
	require.GreaterOrEqual(t, fsFields["withMountedCache"], 2, "both SDK cache children delegate inherited fs")
	require.GreaterOrEqual(t, fsFields["__withSystemEnvVariable"], 2, "both SDK environment children delegate inherited fs")
	for k, count := range operations {
		row := rows[k.row]
		if row.Call == nil {
			continue
		}
		var address dagql.PersistedPartAddress
		require.NoError(t, json.Unmarshal([]byte(k.address), &address))
		if row.Call.Field == "withMountedFile" || row.Call.Field == "withMountedDirectory" {
			require.NotNil(t, row.Call.Receiver)
			t.Logf("operation mount-route row=%d address=%s field=%s parent=%d entries=%d", k.row, k.address, row.Call.Field, row.Call.Receiver.ResultID, count)
		}
		if (row.Call.Field == "withMountedFile" && address.Part == "mount:/schema.json") || (row.Call.Field == "withMountedDirectory" && address.Part == "mount:/src") {
			require.Equal(t, 1, count, "mount write operation repeats: %+v", k)
			mountWriters[row.Call.Field]++
			t.Logf("operation mount-write row=%d address=%s field=%s entries=%d", k.row, k.address, row.Call.Field, count)
		}
	}
	require.Positive(t, mountWriters["withMountedFile"])
	require.Positive(t, mountWriters["withMountedDirectory"])
	// The exact imported Host input gets B's own matching capture. Check both
	// the content class and the installed snapshot, while retaining distinct rows.
	hostMatches := 0
	for _, event := range report.Parts {
		row := rows[event.ResultID]
		if event.Kind != "installed-ready" || !row.Imported || row.Call == nil || row.Call.Field != "directory" || row.Call.Receiver == nil {
			continue
		}
		parent := rows[row.Call.Receiver.ResultID]
		if parent.Call == nil || parent.Call.Type.NamedType != "Host" {
			continue
		}
		matched := false
		for _, local := range report.Rows {
			if local.Imported || local.ResultID == row.ResultID || local.Call.Type.NamedType != "Directory" {
				continue
			}
			equalClass, equalSnapshot := false, false
			for _, class := range row.OutputClasses {
				equalClass = equalClass || slices.Contains(local.OutputClasses, class)
			}
			for _, a := range row.SnapshotLinks {
				for _, b := range local.SnapshotLinks {
					equalSnapshot = equalSnapshot || (a.RefKey == b.RefKey && a.Role == b.Role)
				}
			}
			if equalClass && equalSnapshot {
				matched = true
				t.Logf("acquisition Host input imported=%d address=%+v local=%d snapshot=%v", row.ResultID, event.Address, local.ResultID, row.SnapshotLinks)
			}
		}
		require.True(t, matched, "imported Host input %d lacks a matching B capture", row.ResultID)
		hostMatches++
	}
	require.Positive(t, hostMatches)
	t.Logf("acquisition SDK inherited-fs hops=%v; operation mount-writer rows=%v", fsFields, mountWriters)
}

func transferContextTool(ctx context.Context, t *testctx.T, client *dagger.Client, handle string) string {
	t.Helper()
	var loaded struct{ Node struct{ ID string } }
	require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($id:ID!){node(id:$id){id}}`, Variables: map[string]any{"id": handle}}, &dagger.Response{Data: &loaded}))
	require.NotEmpty(t, loaded.Node.ID)
	// A bare schema cannot validate an inline fragment on an uninstalled
	// module type. Bound tools dispatch through the recovered schema.
	model := cannedRecordingModel(ctx, t, client, client.LLM().WithPrompt("read the notes").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindToolCall, CallID: "notes", ToolName: "noteDirectory"}}).
		WithToolResult("notes", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "finished"}}))
	var result struct {
		LLM struct {
			WithTools struct {
				WithPrompt struct{ Loop struct{ Transcript string } }
			}
		}
	}
	require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($id:ID!,$model:String!){llm(model:$model){withTools(object:$id){withPrompt(prompt:"read the notes"){loop{transcript}}}}}`, Variables: map[string]any{"id": handle, "model": model}}, &dagger.Response{Data: &result}))
	return result.LLM.WithTools.WithPrompt.Loop.Transcript
}

func transferBoundToolControl(ctx context.Context, t *testctx.T, client *dagger.Client, saved string) {
	t.Helper()
	var binding struct {
		LLM struct{ WithTools struct{ PortableID string } }
	}
	require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($id:ID!){llm{withTools(object:$id){portableID}}}`, Variables: map[string]any{"id": saved}}, &dagger.Response{Data: &binding}))
	bound := new(call.ID)
	require.NoError(t, bound.Decode(binding.LLM.WithTools.PortableID))
	var objectID string
	for cur := bound; cur != nil; cur = cur.Receiver() {
		if cur.Field() != "withTools" {
			continue
		}
		for _, arg := range cur.Args() {
			if arg.Name() == "object" {
				id := arg.Value().(*call.LiteralID).Value()
				require.False(t, id.IsHandle(), "portable binding carries a recipe for lazy loading")
				var err error
				objectID, err = id.Encode()
				require.NoError(t, err)
			}
		}
	}
	require.NotEmpty(t, objectID)
	model := cannedRecordingModel(ctx, t, client, client.LLM().WithPrompt("change the seed and read its label").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindToolCall, CallID: "change", ToolName: "withSeed", Arguments: dagger.JSON(`{"seed":"bound"}`)}}).
		WithToolResult("change", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindToolCall, CallID: "read", ToolName: "label"}}).
		WithToolResult("read", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "finished"}}))
	var result struct {
		LLM struct {
			WithTools struct {
				Tools      string
				WithPrompt struct{ Loop struct{ Transcript string } }
			}
		}
	}
	require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($id:ID!,$model:String!){llm(model:$model){withTools(object:$id){tools withPrompt(prompt:"change the seed and read its label"){loop{transcript}}}}}`, Variables: map[string]any{"id": objectID, "model": model}}, &dagger.Response{Data: &result}))
	require.Contains(t, result.LLM.WithTools.Tools, "## withSeed")
	require.Contains(t, result.LLM.WithTools.Tools, "## label")
	require.Contains(t, result.LLM.WithTools.WithPrompt.Loop.Transcript, "label:bound")
	require.Contains(t, result.LLM.WithTools.WithPrompt.Loop.Transcript, "finished")
}
