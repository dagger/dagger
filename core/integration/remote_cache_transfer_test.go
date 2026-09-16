package core

import (
	"context"
	"encoding/json"
	"fmt"
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

type RemoteCacheTransferSuite struct{}

func TestRemoteCacheTransferSuite(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(RemoteCacheTransferSuite{})
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
type Report struct { Seed string; Code Code; Status Status }
func recordBody(ctx context.Context) error {
 return dag.GraphQLClient().MakeRequest(ctx,&graphql.Request{Query: "{_remoteCacheFixture(operation:\"recordBody\")}"},&graphql.Response{})
}
func(m *CacheProbe) Report(ctx context.Context, seed string)(*Report,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}
 return &Report{Seed:seed,Code:Code(seed),Status:Ready},nil
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
	outer := connect(ctx, t)
	newCheckout := func() string {
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
	stop := func(e *running) {
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
	start := func(state string, volume *dagger.CacheVolume, checkout string) *running {
		ctr := devEngineContainerWithStateKey(outer, state, engineWithConfig(ctx, t, engineConfigWithEnabled(true)), func(ctr *dagger.Container) *dagger.Container {
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
	callReport := func(client *dagger.Client, seed string) string {
		var data struct {
			Probe struct {
				Report struct {
					ID                 string
					Seed, Code, Status string
				}
			} `json:"cacheProbe"`
		}
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: `query($seed:String!){cacheProbe{report(seed:$seed){id seed code status}}}`, Variables: map[string]any{"seed": seed}}, &dagger.Response{Data: &data}))
		require.Equal(t, seed, data.Probe.Report.Seed)
		require.Equal(t, seed, data.Probe.Report.Code)
		require.Equal(t, "READY", data.Probe.Report.Status)
		return data.Probe.Report.ID
	}
	countBody := func(client *dagger.Client, function string) uint64 {
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
	aDir := newCheckout()
	aVolume := outer.CacheVolume("b2-transfer-a-" + identity.NewID())
	a := start("b2-transfer-a-state-"+identity.NewID(), aVolume, aDir)
	defer stop(a)
	require.NoError(t, a.client.ModuleSource(".").AsModule().Serve(ctx))
	aID := callReport(a.client, "same")
	require.Equal(t, uint64(1), countBody(a.client, "report"))
	var source []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, a.client, "export", "report.json", []string{aID}, &source))
	require.NotEmpty(t, source)
	for _, order := range []string{"before", "after"} {
		t.Run(order, func(ctx context.Context, t *testctx.T) {
			bDir := newCheckout()
			bVolume := outer.CacheVolume("b2-transfer-b-" + identity.NewID())
			bState := "b2-transfer-b-state-" + identity.NewID()
			b := start(bState, bVolume, bDir)
			defer func() { stop(b) }()
			if order == "after" {
				require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
			}
			_, err := outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
				WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/report.json /destination/bundles/report.json"}).Sync(ctx)
			require.NoError(t, err)
			var imported []transferFixtureMapping
			require.NoError(t, transferFixture(ctx, b.client, "import", "report.json", []string{}, &imported))
			require.Equal(t, uint64(0), countBody(b.client, "report"))
			if order == "before" {
				require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
			}
			saved := callReport(b.client, "same")
			require.Equal(t, uint64(0), countBody(b.client, "report"), "ordinary call must use transferred result")
			callReport(b.client, "different")
			require.Equal(t, uint64(1), countBody(b.client, "report"), "changed argument enters function")
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
				require.NoError(t, b.client.Do(ctx, &dagger.Request{Query: fmt.Sprintf(`query($id:ID!){node(id:$id){... on %s{%s}}}`, reportType, method), Variables: map[string]any{"id": saved}}, &dagger.Response{Data: &data}))
				require.Equal(t, notes, data.Node[method])
			}
			loadNotes("noteFile", "consumer file notes")
			stop(b)
			b = start(bState, bVolume, bDir)
			// Module-aware request installs the consumer's current operational Module.
			require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
			loadNotes("noteDirectory", "consumer directory notes after restart")
			require.Equal(t, uint64(1), countBody(b.client, "report"))
		})
	}
}
