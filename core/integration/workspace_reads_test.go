package core

import (
	"context"
	"crypto/rand"
	"slices"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// TestWorkspaceReadsSharedAcrossClients asserts that the clients and sessions
// reading a Git workspace share its reads, its files being decided by its
// commit, while each client of a host workspace makes its own.
//
// Each module function call is a client of its own. Here the main client and
// two calls of one module function read the same directory and file of the
// session's workspace: from a Git workspace pinned to a commit, and then from
// a host checkout of the same commit on the same engine.
func (WorkspaceSuite) TestWorkspaceReadsSharedAcrossClients(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	nonce := rand.Text()
	checkout := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/work").
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \".dagger/modules/probe\"\n").
		WithNewFile(".dagger/modules/probe/dagger.json", `{"name":"probe","sdk":{"source":"dang"}}`).
		// The workspace is optional, so the engine passes the session's in a
		// query that leaves it out.
		WithNewFile(".dagger/modules/probe/main.dang", `type Probe {
  pub read(ws: Workspace, dir: String!, n: Int!): String! {
    if (ws != null) {
      ws.directory(dir, include: ["notes.txt"]).file("notes.txt").contents + ws.file(dir + "/other.txt").contents
    } else {
      raise "no workspace"
    }
  }
}
`).
		WithNewFile("git-data/notes.txt", nonce).
		WithNewFile("git-data/other.txt", "other").
		WithNewFile("host-data/notes.txt", nonce).
		WithNewFile("host-data/other.txt", "other").
		WithExec([]string{"git", "init", "-q"}).
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "commit", "-qm", "init"}).
		Directory("/work")
	repo := workspaceSelectionRemoteRepo(ctx, t, c, makeGitDir(c, checkout, "main"))
	sha, err := core.NewQuery(c).Git(repo).Ref("main").CommitSHA(ctx)
	require.NoError(t, err)

	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.Stop(ctx) })
	client := telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud)

	// The same reads from the main client and from two calls of read, which
	// differ only in n, so that each is a call and a client of its own.
	mainReads := func(dir string) string {
		return `currentWorkspace {
    directory(path: "` + dir + `", include: ["notes.txt"]) { file(path: "notes.txt") { contents } }
    file(path: "` + dir + `/other.txt") { contents }
  }
`
	}
	functionReads := func(dir string) string {
		return `probe {
    a: read(dir: "` + dir + `", n: 1)
    b: read(dir: "` + dir + `", n: 2)
  }
`
	}
	query := func(ctr *core.Container, doc string, clients int, args ...string) {
		out, err := ctr.
			WithNewFile("/query.graphql", doc).
			WithEnvVariable("CACHEBUSTER", rand.Text()).
			WithExec(append([]string{"/bin/dagger", "query", "--doc", "/query.graphql"}, args...), core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, clients, strings.Count(out, nonce), "every client must read the workspace's files: %s", out)
	}
	// A first session reads the Git workspace from its main client alone.
	// Once it has ended, a second session makes the same reads from its main
	// client and two function calls. Reads its commit decides are kept past
	// the session, so the second session makes none of them again. Two
	// clients making a read at the same moment can each make it, so the
	// sessions run in turn.
	gitWorkspace := []string{"-W", repo + "@" + sha}
	query(client, "{\n"+mainReads("/git-data")+"}", 1, gitWorkspace...)
	query(client, "{\n"+mainReads("/git-data")+functionReads("/git-data")+"}", 3, gitWorkspace...)
	// The host checkout, read by the same three clients in one session.
	query(client.WithMountedDirectory("/work", checkout).WithWorkdir("/work"), "{\n"+mainReads("/host-data")+functionReads("/host-data")+"}", 3)

	type span struct {
		Session string            `json:"session"`
		SpanID  string            `json:"spanID"`
		Name    string            `json:"name"`
		Attrs   map[string]string `json:"attrs"`
	}
	type read struct {
		outcome   string
		perClient bool
	}
	// A span may be exported when it starts and again when it ends; the last
	// export with an outcome is the call's. A session reports a call it has
	// reported already only once, so the clients sharing a read may show
	// fewer spans than clients.
	reads := map[string]map[string]map[string]read{} // read -> session -> span
	withFunctions := map[string]bool{}
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		if strings.HasPrefix(s.Name, "Probe.read") {
			withFunctions[s.Session] = true
		}
		if s.Name != "Workspace.directory" && s.Name != "Workspace.file" {
			continue
		}
		outcome := s.Attrs["dagger.io/cache.outcome"]
		payload := s.Attrs["dagger.io/dag.call"]
		if outcome == "" || payload == "" {
			continue
		}
		var frame callpbv1.Call
		require.NoError(t, frame.Decode(payload))
		var path string
		for _, arg := range frame.GetArgs() {
			if arg.GetName() == "path" {
				path = arg.GetValue().GetString_()
			}
		}
		key := s.Name + "(" + path + ")"
		if reads[key] == nil {
			reads[key] = map[string]map[string]read{}
		}
		if reads[key][s.Session] == nil {
			reads[key][s.Session] = map[string]read{}
		}
		reads[key][s.Session][s.SpanID] = read{
			outcome: outcome,
			perClient: slices.ContainsFunc(frame.GetImplicitInputs(), func(input *callpbv1.Argument) bool {
				return input.GetName() == "cachePerClient"
			}),
		}
	}
	outcomes := func(spans map[string]read) (executed int, others []string, perClient int) {
		for _, r := range spans {
			if r.outcome == "executed" {
				executed++
			} else {
				others = append(others, r.outcome)
			}
			if r.perClient {
				perClient++
			}
		}
		return executed, others, perClient
	}

	for _, key := range []string{"Workspace.directory(/git-data)", "Workspace.file(/git-data/other.txt)"} {
		require.Len(t, reads[key], 2, "%s: both sessions must report the read", key)
		for session, spans := range reads[key] {
			executed, others, perClient := outcomes(spans)
			t.Logf("%s, session with function calls %t: %d executed, then %v", key, withFunctions[session], executed, others)
			require.Zero(t, perClient, "%s: a read of a workspace its commit decides must not be keyed by its client", key)
			if !withFunctions[session] {
				require.Equal(t, 1, executed, "%s: the first session makes the read", key)
				continue
			}
			require.Zero(t, executed, "%s: clients and sessions must share a read of a workspace its commit decides", key)
			require.NotEmpty(t, others, key)
			require.Subset(t, []string{"hit", "joined"}, others, key)
		}
	}
	for _, key := range []string{"Workspace.directory(/host-data)", "Workspace.file(/host-data/other.txt)"} {
		require.Len(t, reads[key], 1, key)
		for _, spans := range reads[key] {
			executed, others, perClient := outcomes(spans)
			t.Logf("%s: %d executed, then %v", key, executed, others)
			require.Equal(t, 3, executed, "%s: each client must make its own read of a host workspace, whose files can change between clients", key)
			require.Empty(t, others, key)
			require.Equal(t, 3, perClient, "%s: a read of a host workspace must stay keyed by its client", key)
		}
	}
}
