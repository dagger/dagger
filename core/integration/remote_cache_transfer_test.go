package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
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
	Rows   []dagql.TransferFixtureRow `json:"rows"`
	Bodies []struct {
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
type Report struct { Seed string; Code Code; Status Status; Platform dagger.Platform }
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
 return &Report{Seed:seed,Code:Code(seed),Status:Ready,Platform:dagger.Platform("linux/amd64")},nil
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
	runTransferSchemaRecovery(ctx, t, false)
}

func (RemoteCacheTransferSuite) TestSchemaRecoveryCold(ctx context.Context, t *testctx.T) {
	t.Skip("addendum 2: fully cold import needs batch 4 acquisition through local-equivalent selection or the batch 1 builtin producer; batch 7 owns acceptance")
	runTransferSchemaRecovery(ctx, t, true)
}

func runTransferSchemaRecovery(ctx context.Context, t *testctx.T, cold bool) {
	outer := connect(ctx, t)
	newCheckout := func(t *testctx.T) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".dagger"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger.json"), []byte(`{"name":"cache-probe","engineVersion":"latest","sdk":{"source":"go"},"source":".dagger"}`), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".dagger", "main.go"), []byte(transferProbeSource), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("producer notes"), 0644))
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
		// Match the persistence suite's limits so host disk pressure does not
		// evict the saved handles whose restart behavior this test measures.
		ctr := devEngineContainerWithStateKey(outer, state, engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0")), func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
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
	callReport := func(t *testctx.T, client *dagger.Client, seed string) string {
		var data struct {
			Probe struct {
				Report struct {
					ID                           string
					Seed, Code, Status, Platform string
				}
			} `json:"cacheProbe"`
		}
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($seed:String!){cacheProbe{report(seed:$seed){id seed code status platform}}}`, Variables: map[string]any{"seed": seed}}, &dagger.Response{Data: &data}))
		require.Equal(t, seed, data.Probe.Report.Seed)
		require.Equal(t, seed, data.Probe.Report.Code)
		require.Equal(t, "READY", data.Probe.Report.Status)
		require.Equal(t, "linux/amd64", data.Probe.Report.Platform)
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
	require.NoError(t, transferFixture(ctx, a.client, "export", "report.json", []string{aID}, &source))
	require.NotEmpty(t, source)
	// The same contextual call succeeds on the native object without import.
	var native struct{ Node struct{ NoteFile string } }
	require.NoError(t, a.client.Do(ctx, &dagger.Request{Query: `query($id:ID!){node(id:$id){... on CacheProbeReport{noteFile}}}`, Variables: map[string]any{"id": aID}}, &dagger.Response{Data: &native}))
	require.Equal(t, "producer notes", native.Node.NoteFile)
	for _, order := range []string{"before", "after"} {
		t.Run(order, func(ctx context.Context, t *testctx.T) {
			bDir := newCheckout(t)
			bVolume := outer.CacheVolume("b2-transfer-b-" + identity.NewID())
			bState := "b2-transfer-b-state-" + identity.NewID()
			b := start(t, bState, bVolume, bDir)
			defer func() { stop(t, b) }()
			operational := b.client.ModuleSource(".").AsModule()
			var err error
			if !cold {
				operational, err = operational.Sync(ctx)
				require.NoError(t, err)
			}
			require.Equal(t, uint64(0), countBody(t, b.client, "report"))
			if order == "after" {
				require.NoError(t, operational.Serve(ctx))
			}
			_, err = outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
				WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/report.json /destination/bundles/report.json"}).Sync(ctx)
			require.NoError(t, err)
			var imported []transferFixtureMapping
			require.NoError(t, transferFixture(ctx, b.client, "import", "report.json", []string{}, &imported))
			require.Equal(t, uint64(0), countBody(t, b.client, "report"))
			if order == "before" {
				require.NoError(t, operational.Serve(ctx))
			}
			saved := callReport(t, b.client, "same")
			var echoed struct {
				Probe struct{ Echo struct{ Seed string } } `json:"cacheProbe"`
			}
			require.NoError(t, b.client.Do(ctx, &dagger.Request{Query: `query($id:ID!){cacheProbe{echo(item:$id){seed}}}`, Variables: map[string]any{"id": saved}}, &dagger.Response{Data: &echoed}))
			require.Equal(t, "same", echoed.Probe.Echo.Seed)
			require.Equal(t, uint64(1), countBody(t, b.client, "echo"))
			require.Equal(t, uint64(0), countBody(t, b.client, "report"), "ordinary call must use transferred result")
			callReport(t, b.client, "different")
			require.Equal(t, uint64(1), countBody(t, b.client, "report"), "changed argument enters function")
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
			stop(t, b)
			b = start(t, bState, bVolume, bDir)
			var restored transferFixtureReport
			require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{saved}, &restored), "saved row must be present immediately after clean restart")
			require.Len(t, restored.Rows, 1)
			// Module-aware request installs the consumer's current operational Module.
			require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
			loadNotes("noteDirectory", "consumer directory notes after restart")
			require.Equal(t, uint64(1), countBody(t, b.client, "report"))
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
		})
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
