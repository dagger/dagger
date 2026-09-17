package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// workspaceProbeSource is the Workspace pattern of design §2: an ordinary
// constructor that captures a source Directory from the injected Workspace
// and derives a second field with a real exec, and one measured downstream
// method. New stays uninstrumented: constructor conversion clears the function
// name, and the body recorder rejects it.
var workspaceProbeSource = `package main
import (
 "context"
 "dagger/project/internal/dagger"
 "github.com/Khan/genqlient/graphql"
)
type Project struct { Source *dagger.Directory; Built *dagger.Directory }
type Summary struct { Text string }
func New(
 // Injected by Dagger.
 ws *dagger.Workspace,
) *Project {
 source := ws.Directory("app")
 built := dag.Container().From("` + alpineImage + `").
  WithMountedDirectory("/src", source).
  WithExec([]string{"sh","-ec","mkdir /out; cat /src/*.txt > /out/all.txt"}).
  Directory("/out")
 return &Project{Source: source, Built: built}
}
func recordBody(ctx context.Context) error {
 return dag.GraphQLClient().MakeRequest(ctx,&graphql.Request{Query: "{_remoteCacheFixture(operation:\"recordBody\")}"},&graphql.Response{})
}
func(p *Project) Describe(ctx context.Context,
 // +optional
 // +default="plain"
 style string,
)(*Summary,error){
 if err:=recordBody(ctx);err!=nil{return nil,err}
 all,err:=p.Built.File("all.txt").Contents(ctx); if err!=nil{return nil,err}
 return &Summary{Text: style+":"+all},nil
}
`

type workspaceProject struct {
	ID       string
	Describe struct{ ID, Text string }
	Source   struct{ ID string }
	Built    struct{ ID string }
}

func workspaceCheckout(e *fixtureEngine, bytes string) {
	e.hostFile("dagger.json", `{"name":"project","engineVersion":"latest","sdk":{"source":"go"},"source":".dagger"}`)
	e.hostFile(".dagger/main.go", workspaceProbeSource)
	e.hostFile("app/one.txt", bytes)
	// Workspace detection anchors on the git root; a worktree-style gitfile
	// is accepted as that boundary.
	e.hostFile(".git", "gitdir: /nonexistent\n")
}

func workspaceDescribe(ctx context.Context, t *testctx.T, e *fixtureEngine, style string) workspaceProject {
	t.Helper()
	// The engine's own current Workspace, as the CLI would supply it.
	var current struct {
		Workspace struct{ ID string } `json:"currentWorkspace"`
	}
	require.NoError(t, e.client.Do(ctx, &dagger.Request{Query: `{currentWorkspace{id}}`}, &dagger.Response{Data: &current}))
	var data struct{ Project workspaceProject }
	require.NoError(t, e.client.Do(ctx, &dagger.Request{Query: `query($ws:ID!,$style:String!){project(ws:$ws){id describe(style:$style){id text} source{id} built{id}}}`, Variables: map[string]any{"ws": current.Workspace.ID, "style": style}}, &dagger.Response{Data: &data}))
	return data.Project
}

// TestWorkspaceCapture is the native Workspace pattern row (design §2 and §5):
// B's own ordinary constructor, over the same workspace bytes in a different
// checkout, matches the transferred aggregate, the exported downstream method
// is not entered, changed workspace bytes do not hit, and the exact imported
// source and built fields keep B-local owners across donor release and a
// restart.
func (RemoteCacheTransferSuite) TestWorkspaceCapture(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	a := newFixtureEngine(ctx, t, outer, "workspace-a", true)
	b := newFixtureEngine(ctx, t, outer, "workspace-b", true)
	bytes := "workspace bytes " + identity.NewID()
	workspaceCheckout(a, bytes)
	workspaceCheckout(b, bytes)

	require.NoError(t, a.client.ModuleSource(".").AsModule().Serve(ctx))
	made := workspaceDescribe(ctx, t, a, "plain")
	require.Equal(t, "plain:"+bytes, made.Describe.Text)
	require.Equal(t, uint64(1), pipelineBodies(t, a, "describe"))
	// The downstream method's result is the root: an export's closure follows
	// dependencies, and describe's result depends on the Project, not the
	// reverse.
	require.NoError(t, a.fixture("export", "workspace.json", []string{made.Describe.ID}, nil))
	contentDigests := func(e *fixtureEngine, handle string) []string {
		var out []string
		for _, extra := range rowOf(t, e, handle).Call.ExtraDigests {
			out = append(out, extra.Label+"="+extra.Digest.String())
		}
		return out
	}
	aDigests := contentDigests(a, made.ID)
	t.Logf("A's Project extra digests: %v", aDigests)
	a.copyFixtureTo(b, "workspace.json")

	var imported []transferFixtureMapping
	require.NoError(t, b.fixture("import", "workspace.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
	hit := workspaceDescribe(ctx, t, b, "plain")
	require.Equal(t, "plain:"+bytes, hit.Describe.Text)
	// A call taking a Workspace never matches by recipe: currentWorkspace is a
	// per-call input. New therefore runs on B, and B's Project unites with
	// A's imported one by the content digest a Workspace-taking constructor's
	// object gets over everything it holds.
	bDigests := contentDigests(b, hit.ID)
	t.Logf("B's Project extra digests: %v", bDigests)
	require.Equal(t, aDigests, bDigests, "identical workspace bytes give the constructed Project the same content identity")
	require.Zero(t, pipelineBodies(t, b, "describe"), "the exported downstream method is not entered on B")
	row := rowOf(t, b, hit.Describe.ID)
	t.Logf("B's describe result row=%d imported=%t", row.ResultID, row.Imported)
	require.True(t, row.Imported, "the downstream call returned the transferred result")

	// A changed argument enters the method; changed workspace bytes do not
	// hit at all.
	changedStyle := workspaceDescribe(ctx, t, b, "fancy")
	require.Equal(t, "fancy:"+bytes, changedStyle.Describe.Text)
	require.Equal(t, uint64(1), pipelineBodies(t, b, "describe"), "a changed argument enters the method once")

	// The exact imported fields own B-local snapshots once they are read.
	for name, handle := range map[string]string{"source": hit.Source.ID, "built": hit.Built.ID} {
		entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(handle)).Entries(ctx)
		require.NoError(t, err, name)
		require.NotEmpty(t, entries, name)
		field := rowOf(t, b, handle)
		t.Logf("%s field row=%d imported=%t links=%d", name, field.ResultID, field.Imported, len(field.SnapshotLinks))
	}

	// Release B's local donors: end the session, collect, and restart. The
	// saved aggregate still answers, the method still is not entered, and
	// its fields still read.
	tracked := map[string]uint64{"describe": rowOf(t, b, hit.Describe.ID).ResultID}
	for name, handle := range map[string]string{"source": hit.Source.ID, "built": hit.Built.ID} {
		tracked[name] = rowOf(t, b, handle).ResultID
	}
	present := func(stage string) {
		var all fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &all))
		for name, id := range tracked {
			state := "MISSING"
			for _, row := range all.Rows {
				if row.ResultID == id {
					state = fmt.Sprintf("imported=%t persisted=%t links=%d deps=%v", row.Imported, row.Persisted, len(row.SnapshotLinks), row.DependencyIDs)
				}
			}
			t.Logf("%s: %s row=%d %s", stage, name, id, state)
		}
	}
	present("before the release")
	b.reconnect()
	present("after the session ended")
	require.NoError(t, b.fixture("gc", "", nil, nil))
	present("after the collection")
	// The live half of the same fault: the SDK runtime's cache volumes are
	// imported rows here, because B imported before it first served the
	// Module, and their backing snapshots were created on B at first use. A
	// second ordinary call that mounts them must still work after the
	// session that created them ended and a collection ran.
	require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
	live := workspaceDescribe(ctx, t, b, "after-collection")
	require.Equal(t, "after-collection:"+bytes, live.Describe.Text)
	require.Equal(t, uint64(2), pipelineBodies(t, b, "describe"), "a new argument runs the Module again, mounting its cache volumes")
	b.restart()
	present("after the restart")
	var booted fixtureControlsReport
	require.NoError(t, b.fixture("report", "", nil, &booted))
	require.Equal(t, dagql.CachePersistenceResetNone, booted.Persistence.PersistenceResetReason, "a clean restart after a session end and a real collection keeps the cache")
	require.Empty(t, booted.Persistence.LocalCacheResetReason)
	// The exact imported fields still read after the restart, before anything
	// else is loaded: they are owned on B, not borrowed from a donor.
	for name, handle := range map[string]string{"source": hit.Source.ID, "built": hit.Built.ID} {
		field := rowOf(t, b, handle)
		require.True(t, field.Imported, name)
		require.Len(t, field.SnapshotLinks, 1, "%s keeps its owner link across the restart", name)
		entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(handle)).Entries(ctx)
		require.NoError(t, err, name)
		require.NotEmpty(t, entries, name)
	}
	saved := rowOf(t, b, hit.Describe.ID)
	var restored fixtureControlsReport
	require.NoError(t, b.fixture("report", "", nil, &restored))
	for _, row := range restored.Rows {
		if row.ResultID == saved.ResultID || slices.Contains(saved.DependencyIDs, row.ResultID) {
			t.Logf("after the restart: row=%d field=%s imported=%t persisted=%t extra=%v classes=%v", row.ResultID, row.Call.Field, row.Imported, row.Persisted, row.Call.ExtraDigests, row.OutputClasses)
		}
	}
	require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
	again := workspaceDescribe(ctx, t, b, "plain")
	require.Equal(t, "plain:"+bytes, again.Describe.Text)
	for _, handle := range []string{again.ID, again.Describe.ID} {
		row := rowOf(t, b, handle)
		t.Logf("after the restart, B's new call: row=%d field=%s imported=%t extra=%v classes=%v", row.ResultID, row.Call.Field, row.Imported, row.Call.ExtraDigests, row.OutputClasses)
	}
	require.Equal(t, uint64(2), pipelineBodies(t, b, "describe"), "body entries persist across the restart: only the two changed-argument calls ever entered the method")
	all, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(again.Built.ID)).File("all.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, bytes, all)

	// The non-hit control: different workspace bytes make a different Project.
	b.hostFile("app/one.txt", bytes+" changed")
	b.reconnect()
	require.NoError(t, b.client.ModuleSource(".").AsModule().Serve(ctx))
	other := workspaceDescribe(ctx, t, b, "plain")
	require.True(t, strings.HasSuffix(other.Describe.Text, " changed"))
	require.False(t, rowOf(t, b, other.Describe.ID).Imported, "changed workspace bytes do not match the transferred result")
}
