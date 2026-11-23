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
// two calls of each of three module functions read the session's workspace
// the same ways: a directory, a file, a glob and its Git HEAD. They read a
// Git workspace pinned to a commit, and then a host checkout of the same
// commit on the same engine.
func (WorkspaceSuite) TestWorkspaceReadsSharedAcrossClients(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	nonce := rand.Text()
	checkout := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/work").
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \".dagger/modules/probe\"\n").
		WithNewFile(".dagger/modules/probe/dagger.json", `{"name":"probe","engineVersion":"latest","sdk":{"source":"dang"}}`).
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
  pub look(ws: Workspace, pattern: String!, n: Int!): [String!]! {
    if (ws != null) {
      ws.glob(pattern)
    } else {
      raise "no workspace"
    }
  }
  pub head(ws: Workspace, n: Int!): String! {
    if (ws != null) {
      ws.git.head.commitSha
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

	// The same reads from the main client and from two calls of each
	// function, which differ only in n, so that each is a call and a client
	// of its own.
	mainReads := func(dir string) string {
		return `currentWorkspace {
    directory(path: "` + dir + `", include: ["notes.txt"]) { file(path: "notes.txt") { contents } }
    file(path: "` + dir + `/other.txt") { contents }
    glob(pattern: "` + strings.TrimPrefix(dir, "/") + `/*.txt")
    git { head { commitSha } }
  }
`
	}
	functionReads := func(dir string) string {
		return `probe {
    a: read(dir: "` + dir + `", n: 1)
    b: read(dir: "` + dir + `", n: 2)
    ga: look(pattern: "` + strings.TrimPrefix(dir, "/") + `/*.txt", n: 1)
    gb: look(pattern: "` + strings.TrimPrefix(dir, "/") + `/*.txt", n: 2)
    ha: head(n: 1)
    hb: head(n: 2)
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
	// Each session's reads, by span name, and which of the workspaces and
	// queries each session is: the directory read's path says which workspace,
	// and a function call's span says it is a query with function calls. A
	// span may be exported when it starts and again when it ends; the last
	// export with an outcome is the call's. A session reports a call it has
	// reported already only once, so the clients sharing a read may show
	// fewer spans than clients.
	readNames := []string{"Workspace.directory", "Workspace.file", "Workspace.glob", "Workspace.git", "WorkspaceGit.head"}
	reads := map[string]map[string]map[string]read{} // session -> read -> span
	workspaceOf := map[string]string{}
	withFunctions := map[string]bool{}
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		if strings.HasPrefix(s.Name, "Probe.") {
			withFunctions[s.Session] = true
		}
		outcome := s.Attrs["dagger.io/cache.outcome"]
		payload := s.Attrs["dagger.io/dag.call"]
		if !slices.Contains(readNames, s.Name) || outcome == "" || payload == "" {
			continue
		}
		var frame callpbv1.Call
		require.NoError(t, frame.Decode(payload))
		var path string
		for _, arg := range frame.GetArgs() {
			if arg.GetName() == "path" || arg.GetName() == "pattern" {
				path = arg.GetValue().GetString_()
			}
		}
		// Module loading reads the workspace too, at other paths.
		switch {
		case s.Name == "Workspace.directory" && path == "/git-data", s.Name == "Workspace.directory" && path == "/host-data":
			workspaceOf[s.Session] = path
		case s.Name == "Workspace.directory" || s.Name == "Workspace.file" || s.Name == "Workspace.glob":
			if !strings.Contains(path, "-data") {
				continue
			}
		}
		if reads[s.Session] == nil {
			reads[s.Session] = map[string]map[string]read{}
		}
		if reads[s.Session][s.Name] == nil {
			reads[s.Session][s.Name] = map[string]read{}
		}
		reads[s.Session][s.Name][s.SpanID] = read{
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

	var gitSessions, hostSessions int
	for session, byName := range reads {
		switch workspaceOf[session] {
		case "/git-data":
			gitSessions++
			for _, name := range readNames {
				executed, others, perClient := outcomes(byName[name])
				t.Logf("Git workspace, session with function calls %t: %s: %d executed, then %v", withFunctions[session], name, executed, others)
				require.Zero(t, perClient, "%s: a read of a workspace its commit decides must not be keyed by its client", name)
				switch {
				case !withFunctions[session]:
					require.Equal(t, 1, executed, "%s: the first session makes the read", name)
				case name == "WorkspaceGit.head":
					// Workspace.git's result is kept past the session, and
					// its clients share its children. head isn't kept
					// itself, but its result is the workspace's own ref,
					// which the kept result holds: the second session
					// makes it at most once.
					require.LessOrEqual(t, executed, 1, "%s: the clients of a session must share a read of a workspace its commit decides", name)
				default:
					require.Zero(t, executed, "%s: clients and sessions must share a read of a workspace its commit decides", name)
					require.NotEmpty(t, others, name)
					require.Subset(t, []string{"hit", "joined"}, others, name)
				}
			}
		case "/host-data":
			hostSessions++
			for _, name := range readNames {
				executed, others, perClient := outcomes(byName[name])
				t.Logf("host checkout: %s: %d executed, then %v", name, executed, others)
				require.Equal(t, 3, executed, "%s: each client must make its own read of a host workspace, whose files can change between clients", name)
				require.Empty(t, others, name)
				if name != "WorkspaceGit.head" {
					require.Equal(t, 3, perClient, "%s: a read of a host workspace must stay keyed by its client", name)
				}
			}
		}
	}
	require.Equal(t, 2, gitSessions, "both Git sessions must report their reads")
	require.Equal(t, 1, hostSessions, "the host session must report its reads")
}

// TestWorkspaceReadsThroughWorkdirSharedAcrossSessions asserts that a
// workspace made from a Git workspace by withWorkdir shares its reads across
// sessions as the Git workspace does, its files being decided by the commit
// and its working directory, while one made from a host workspace keeps each
// client's reads its own. Module loading reads a module's files through such
// a workspace.
func (WorkspaceSuite) TestWorkspaceReadsThroughWorkdirSharedAcrossSessions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	nonce := rand.Text()
	checkout := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/work").
		WithNewFile("dagger.toml", "").
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

	reads := func(dir string) string {
		return `{
  currentWorkspace {
    withWorkdir(path: "` + dir + `") {
      directory(path: ".", include: ["notes.txt"]) { file(path: "notes.txt") { contents } }
      file(path: "other.txt") { contents }
      glob(pattern: "*.txt")
    }
  }
}
`
	}
	query := func(ctr *core.Container, doc string, args ...string) {
		out, err := ctr.
			WithNewFile("/query.graphql", doc).
			WithEnvVariable("CACHEBUSTER", rand.Text()).
			WithExec(append([]string{"/bin/dagger", "query", "--doc", "/query.graphql"}, args...), core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, nonce, "the read must see the workspace's files: %s", out)
	}
	// Two sessions read the Git workspace through withWorkdir, one after the
	// other, and then one session reads the host checkout the same way.
	gitWorkspace := []string{"-W", repo + "@" + sha}
	query(client, reads("git-data"), gitWorkspace...)
	query(client, reads("git-data"), gitWorkspace...)
	query(client.WithMountedDirectory("/work", checkout).WithWorkdir("/work"), reads("host-data"))

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
	readNames := []string{"Workspace.directory", "Workspace.file", "Workspace.glob"}
	sessionReads := map[string]map[string]map[string]read{} // session -> read -> span
	workspaceOf := map[string]string{}
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		payload := s.Attrs["dagger.io/dag.call"]
		if payload == "" {
			continue
		}
		var frame callpbv1.Call
		require.NoError(t, frame.Decode(payload))
		arg := func(name string) string {
			for _, a := range frame.GetArgs() {
				if a.GetName() == name {
					return a.GetValue().GetString_()
				}
			}
			return ""
		}
		if s.Name == "Workspace.withWorkdir" {
			workspaceOf[s.Session] = arg("path")
			continue
		}
		outcome := s.Attrs["dagger.io/cache.outcome"]
		if !slices.Contains(readNames, s.Name) || outcome == "" {
			continue
		}
		switch s.Name {
		case "Workspace.directory":
			if arg("path") != "." {
				continue
			}
		case "Workspace.file":
			if arg("path") != "other.txt" {
				continue
			}
		case "Workspace.glob":
			if arg("pattern") != "*.txt" {
				continue
			}
		}
		if sessionReads[s.Session] == nil {
			sessionReads[s.Session] = map[string]map[string]read{}
		}
		if sessionReads[s.Session][s.Name] == nil {
			sessionReads[s.Session][s.Name] = map[string]read{}
		}
		sessionReads[s.Session][s.Name][s.SpanID] = read{
			outcome: outcome,
			perClient: slices.ContainsFunc(frame.GetImplicitInputs(), func(input *callpbv1.Argument) bool {
				return input.GetName() == "cachePerClient"
			}),
		}
	}
	executedIn := func(byName map[string]map[string]read) (executed map[string]int, others map[string][]string, perClient int) {
		executed, others = map[string]int{}, map[string][]string{}
		for name, spans := range byName {
			for _, r := range spans {
				if r.outcome == "executed" {
					executed[name]++
				} else {
					others[name] = append(others[name], r.outcome)
				}
				if r.perClient {
					perClient++
				}
			}
		}
		return executed, others, perClient
	}

	var gitMade, gitServed, hostSessions int
	for session, byName := range sessionReads {
		executed, others, perClient := executedIn(byName)
		t.Logf("workspace at %q: executed %v, then %v", workspaceOf[session], executed, others)
		switch workspaceOf[session] {
		case "git-data":
			require.Zero(t, perClient, "a read of a workspace its commit decides must not be keyed by its client")
			for _, name := range readNames {
				require.Contains(t, byName, name, "every read must report its outcome")
			}
			if executed["Workspace.directory"] > 0 {
				gitMade++
				for _, name := range readNames {
					require.Equal(t, 1, executed[name], "%s: the first session makes the read", name)
				}
				continue
			}
			gitServed++
			for _, name := range readNames {
				require.Zero(t, executed[name], "%s: a later session must share the read through a workspace made by withWorkdir", name)
				require.Subset(t, []string{"hit", "joined"}, others[name], name)
			}
		case "host-data":
			hostSessions++
			for _, name := range readNames {
				require.Equal(t, 1, executed[name], "%s: the host session makes its own read", name)
			}
			require.Equal(t, len(readNames), perClient, "a read of a host workspace must stay keyed by its client")
		}
	}
	require.Equal(t, 1, gitMade, "the first Git session makes the reads, and only that one")
	require.Equal(t, 1, gitServed, "the second Git session must report its reads, served")
	require.Equal(t, 1, hostSessions, "the host session must report its reads")
}
