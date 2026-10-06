package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// rebuildOnAnotherEngine builds values on engine A, exports them with no
// blob, imports them into engine B and evaluates each imported row there, so
// B must rebuild every part from its saved operation. check reads the
// rebuilt values through B's client, by name.
func rebuildOnAnotherEngine(ctx context.Context, t *testctx.T, build func(*dagger.Client) map[string]dagger.ID, check func(*dagger.Client, map[string]string)) {
	onAnotherEngine(ctx, t, true, build, check)
}

// onAnotherEngine builds values on engine A, exports them with no blob and
// imports them into engine B, evaluating each imported row there when rebuild
// is set. check gets B's client and the imported handles, by name.
func onAnotherEngine(ctx context.Context, t *testctx.T, rebuild bool, build func(*dagger.Client) map[string]dagger.ID, check func(*dagger.Client, map[string]string)) {
	outer := connect(ctx, t)
	type running struct {
		upstream, tunnel *dagger.Service
		client           *dagger.Client
	}
	start := func(state string, volume *dagger.CacheVolume) *running {
		ctr := devEngineContainerWithStateKey(outer, state, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		e := &running{upstream: devEngineContainerAsService(ctr)}
		tunnel, err := outer.Host().Tunnel(e.upstream).Start(ctx)
		require.NoError(t, err)
		e.tunnel = tunnel
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		e.client, err = dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		return e
	}
	id := identity.NewID()
	aVolume := outer.CacheVolume("rebuild-a-" + id)
	bVolume := outer.CacheVolume("rebuild-b-" + id)
	a := start("rebuild-a-state-"+id, aVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &a.client, &a.upstream, &a.tunnel)) }()
	b := start("rebuild-b-state-"+id, bVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &b.client, &b.upstream, &b.tunnel)) }()

	roots := build(a.client)
	for name, root := range roots {
		var exported []transferFixtureMapping
		require.NoError(t, transferFixtureSelected(ctx, a.client, "rebuild-"+name+".json", []string{string(root)}, []string{}, &exported), name)
		require.NotEmpty(t, exported, name)
	}
	_, err := outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/rebuild-*.json /destination/bundles/"}).Sync(ctx)
	require.NoError(t, err)

	handles := map[string]string{}
	for name := range roots {
		var imported []transferFixtureMapping
		require.NoError(t, transferFixture(ctx, b.client, "import", "rebuild-"+name+".json", []string{}, &imported), name)
		require.NotEmpty(t, imported, name)
		handle := imported[0].Handle
		if rebuild {
			var evaluated bool
			require.NoError(t, transferFixture(ctx, b.client, "evaluate", "", []string{handle}, &evaluated), name)
			require.True(t, evaluated, name)
		}
		handles[name] = handle
	}
	var report transferFixtureReport
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &report))
	for _, event := range report.Parts {
		require.NotEqual(t, "provider-read", event.Kind, "nothing was offered for download: %+v", event)
	}
	check(b.client, handles)
}

// engineResultID is the engine result a handle names.
func engineResultID(t *testctx.T, raw string) uint64 {
	t.Helper()
	var id call.ID
	require.NoError(t, id.Decode(raw))
	require.True(t, id.IsHandle())
	return id.EngineResultID()
}

// selectHidden selects field(args) on the object with ID id, a field the SDK
// does not generate, and returns the ID of its result.
func selectHidden(ctx context.Context, t *testctx.T, c *dagger.Client, id dagger.ID, typ, field string, args map[string]any) dagger.ID {
	t.Helper()
	decls, params := []string{"$id: ID!"}, []string{}
	vars := map[string]any{"id": id}
	for _, name := range slices.Sorted(maps.Keys(args)) {
		var argType string
		switch args[name].(type) {
		case dagger.ID:
			argType = "ID!"
		case []dagger.ID:
			argType = "[ID!]!"
		case string:
			argType = "String!"
		default:
			t.Fatalf("argument %s has unsupported type %T", name, args[name])
		}
		decls = append(decls, "$"+name+": "+argType)
		params = append(params, name+": $"+name)
		vars[name] = args[name]
	}
	var result struct {
		Node map[string]struct{ ID dagger.ID }
	}
	query := fmt.Sprintf("query(%s) { node(id: $id) { ... on %s { %s(%s) { id } } } }", strings.Join(decls, ", "), typ, field, strings.Join(params, ", "))
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: &result}), field)
	require.NotEmpty(t, result.Node[field].ID, field)
	return result.Node[field].ID
}

// syncedID evaluates value and returns its ID.
func syncedID[T interface {
	Sync(context.Context) (T, error)
	ID(context.Context) (dagger.ID, error)
}](ctx context.Context, t *testctx.T, value T) dagger.ID {
	t.Helper()
	value, err := value.Sync(ctx)
	require.NoError(t, err)
	id, err := value.ID(ctx)
	require.NoError(t, err)
	return id
}

// The image files of a container are rebuilt on another engine when their
// blobs are missing.
func (RemoteCacheTransferSuite) TestRebuildContainerImageFiles(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		ctr := c.Container().From(alpineImage).WithNewFile("/data", "rebuilt "+seed)
		tarball, err := ctr.AsTarball().Sync(ctx)
		require.NoError(t, err)
		manifest, err := ctr.Manifest().Sync(ctx)
		require.NoError(t, err)
		tarballID, err := tarball.ID(ctx)
		require.NoError(t, err)
		manifestID, err := manifest.ID(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"tarball": tarballID, "manifest": manifestID}
	}, func(c *dagger.Client, handles map[string]string) {
		tarball := dagger.Ref[*dagger.File](c, dagger.ID(handles["tarball"]))
		contents, err := c.Container().Import(tarball).File("/data").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "rebuilt "+seed, contents)
		manifest, err := dagger.Ref[*dagger.File](c, dagger.ID(handles["manifest"])).Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, manifest, `"layers"`)
	})
}

// A Docker build is rebuilt on another engine when its blob is missing.
func (RemoteCacheTransferSuite) TestRebuildDockerBuild(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		built, err := c.Directory().
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN echo "+seed+" > /built\n").
			DockerBuild().
			Sync(ctx)
		require.NoError(t, err)
		builtID, err := built.ID(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"build": builtID}
	}, func(c *dagger.Client, handles map[string]string) {
		contents, err := dagger.Ref[*dagger.Container](c, dagger.ID(handles["build"])).File("/built").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seed+"\n", contents)
	})
}

// A manifest names the layer digests of the engine that built it, and a rebuild
// elsewhere does not reproduce them. Another engine therefore never answers
// manifest from it: it builds its own, and every blob that manifest names is
// available through layer.
func (RemoteCacheTransferSuite) TestManifestStaysOnEngine(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	manifestOf := func(c *dagger.Client) (*dagger.Container, *dagger.File) {
		ctr := c.Container().From(alpineImage).WithNewFile("/data", "per engine "+seed)
		return ctr, ctr.Manifest()
	}
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		_, manifest := manifestOf(c)
		manifest, err := manifest.Sync(ctx)
		require.NoError(t, err)
		id, err := manifest.ID(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"manifest": id}
	}, func(c *dagger.Client, handles map[string]string) {
		ctr, manifest := manifestOf(c)
		id, err := manifest.ID(ctx)
		require.NoError(t, err)
		require.NotEqual(t, engineResultID(t, handles["manifest"]), engineResultID(t, string(id)), "the other engine's manifest must not answer this one's call")
		contents, err := manifest.Contents(ctx)
		require.NoError(t, err)
		var parsed struct {
			Config struct{ Digest string }
			Layers []struct{ Digest string }
		}
		require.NoError(t, json.Unmarshal([]byte(contents), &parsed))
		require.NotEmpty(t, parsed.Layers)
		digests := []string{parsed.Config.Digest}
		for _, layer := range parsed.Layers {
			digests = append(digests, layer.Digest)
		}
		for _, digest := range digests {
			size, err := ctr.Layer(digest).Size(ctx)
			require.NoError(t, err, digest)
			require.Positive(t, size, digest)
		}
	})
}

// Changeset.asPatch is rebuilt on another engine when its blob is missing.
func (RemoteCacheTransferSuite) TestRebuildChangesetPatch(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		before := c.Directory().WithNewFile("data", "before\n")
		patch := before.WithNewFile("data", seed+"\n").Changes(before).AsPatch()
		return map[string]dagger.ID{"patch": syncedID(ctx, t, patch)}
	}, func(c *dagger.Client, handles map[string]string) {
		contents, err := dagger.Ref[*dagger.File](c, dagger.ID(handles["patch"])).Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, contents, "+"+seed)
	})
}

// Changeset merges are rebuilt on another engine when their blob is missing.
func (RemoteCacheTransferSuite) TestRebuildChangesetMerges(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		change := func(path string) dagger.ID {
			before := c.Directory().WithNewFile(path, "0\n")
			return syncedID(ctx, t, before.WithNewFile(path, seed+"\n").Changes(before))
		}
		ours, theirs, third := change("ours"), change("theirs"), change("third")
		merge := func(field string, changes any) dagger.ID {
			return selectHidden(ctx, t, c, ours, "Changeset", field, map[string]any{"changes": changes})
		}
		return map[string]dagger.ID{
			"__mergeWithChangeset":      merge("__mergeWithChangeset", theirs),
			"__mergeWithChangesets":     merge("__mergeWithChangesets", []dagger.ID{theirs, third}),
			"__mergeForWorkspaceCommit": merge("__mergeForWorkspaceCommit", theirs),
		}
	}, func(c *dagger.Client, handles map[string]string) {
		for field, handle := range handles {
			paths := []string{"ours", "theirs"}
			if field == "__mergeWithChangesets" {
				paths = append(paths, "third")
			}
			for _, path := range paths {
				contents, err := dagger.Ref[*dagger.Directory](c, dagger.ID(handle)).File(path).Contents(ctx)
				require.NoError(t, err, field)
				require.Equal(t, seed+"\n", contents, field)
			}
		}
	})
}

// A merge conflict is reported by the call that asks for the merge, as before
// merges saved their operation.
func (RemoteCacheTransferSuite) TestChangesetMergeConflictAtCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	seed := identity.NewID()
	change := func(contents string) *dagger.Changeset {
		before := c.Directory().WithNewFile("data", "base "+seed+"\n")
		return before.WithNewFile("data", contents).Changes(before)
	}
	_, err := change("ours\n").WithChangeset(change("theirs\n")).ID(ctx)
	require.ErrorContains(t, err, "conflict")
}

// rebuildGitRepo is a repository with one commit of file.txt. Its dates are
// fixed, so another engine that runs it again makes the same commit.
func rebuildGitRepo(c *dagger.Client, seed string) *dagger.GitRef {
	return c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/repo").
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithExec([]string{"sh", "-ec", `
		git init -b main
		git config user.name Author
		git config user.email author@example.com
		printf '%s\n' "$1" > file.txt
		git add .
		git commit -m base
		`, "sh", seed}).
		Directory("/repo").AsGit().Head()
}

// rebuildGitChanges replaces file.txt in head's tree with contents.
func rebuildGitChanges(head *dagger.GitRef, contents string) *dagger.Changeset {
	before := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	return before.WithNewFile("file.txt", contents).Changes(before)
}

// requireRebuiltCommit asserts that the Git storage in dir has HEAD sha and
// file.txt contents.
func requireRebuiltCommit(ctx context.Context, t *testctx.T, dir *dagger.Directory, sha, contents string) {
	t.Helper()
	head := dir.AsGit().Head()
	got, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, got)
	file, err := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, file)
}

// GitRef.withCommit's Git storage is rebuilt on another engine when its blob is
// missing, with the same commit.
func (RemoteCacheTransferSuite) TestRebuildGitCommitDirectory(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	var sha string
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		head := rebuildGitRepo(c, seed)
		headID, err := head.ID(ctx)
		require.NoError(t, err)
		committed := selectHidden(ctx, t, c, headID, "GitRef", "__withCommitDirectory", map[string]any{
			"changes":     syncedID(ctx, t, rebuildGitChanges(head, "edited "+seed+"\n")),
			"message":     "edit",
			"date":        workspaceCommitDate,
			"authorName":  "Author",
			"authorEmail": "author@example.com",
		})
		sha, err = dagger.Ref[*dagger.Directory](c, committed).AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"commit": committed}
	}, func(c *dagger.Client, handles map[string]string) {
		requireRebuiltCommit(ctx, t, dagger.Ref[*dagger.Directory](c, dagger.ID(handles["commit"])), sha, "edited "+seed+"\n")
	})
}

// Workspace.__pullDirectory is rebuilt on another engine when its blob is
// missing, with the same history.
func (RemoteCacheTransferSuite) TestRebuildWorkspacePullDirectory(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	var sha string
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		head := rebuildGitRepo(c, seed)
		asWorkspace := dagger.GitRefAsWorkspaceOpts{Cwd: "/"}
		source := head.WithCommit(rebuildGitChanges(head, "pulled "+seed+"\n"), "pull me", workspaceCommitDate, "Author", "author@example.com").AsWorkspace(asWorkspace)
		sourceID, err := source.ID(ctx)
		require.NoError(t, err)
		parentID, err := head.AsWorkspace(asWorkspace).ID(ctx)
		require.NoError(t, err)
		pulled := selectHidden(ctx, t, c, parentID, "Workspace", "__pullDirectory", map[string]any{
			"source":         sourceID,
			"committerName":  "Committer",
			"committerEmail": "committer@example.com",
		})
		sha, err = dagger.Ref[*dagger.Directory](c, pulled).AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"pull": pulled}
	}, func(c *dagger.Client, handles map[string]string) {
		requireRebuiltCommit(ctx, t, dagger.Ref[*dagger.Directory](c, dagger.ID(handles["pull"])), sha, "pulled "+seed+"\n")
	})
}

// A file write and metadata edits over evaluated parents are rebuilt on
// another engine when the written bytes are not available there.
func (RemoteCacheTransferSuite) TestRebuildContainerMutations(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		written, err := c.Container().WithNewFile("/data", seed).Sync(ctx)
		require.NoError(t, err)
		user, err := written.WithUser("app").Sync(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"edited": syncedID(ctx, t, user.WithShell([]string{"/bin/ash"}))}
	}, func(c *dagger.Client, handles map[string]string) {
		ctr := dagger.Ref[*dagger.Container](c, dagger.ID(handles["edited"]))
		user, err := ctr.User(ctx)
		require.NoError(t, err)
		require.Equal(t, "app", user)
		shell, err := ctr.Shell().Args(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/bin/ash"}, shell)
		contents, err := ctr.File("/data").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seed, contents)
	})
}

// Container mutations that write over an evaluated parent run at the call and
// keep their saved operation, so another engine rebuilds them; a Dockerfile
// compat mount does over a pending parent too.
func (RemoteCacheTransferSuite) TestRebuildContainerWritesOverEvaluatedParent(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	paths := map[string]string{
		"evaluated-withDirectory":                     "/d/source",
		"evaluated-withFile":                          "/f",
		"evaluated-withNewFile":                       "/n",
		"evaluated-__withMountedPathDockerfileCompat": "/m/source",
		"pending-__withMountedPathDockerfileCompat":   "/m/source",
	}
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		source := c.Directory().WithNewFile("source", seed)
		sourceID := syncedID(ctx, t, source)
		// Distinct roots keep the evaluated parent from being the pending one.
		evaluated, err := c.Container().WithRootfs(c.Directory().WithNewDirectory("evaluated")).Sync(ctx)
		require.NoError(t, err)
		pending := c.Container().WithRootfs(c.Directory().WithNewDirectory("pending"))
		mounted := func(parent *dagger.Container) dagger.ID {
			parentID, err := parent.ID(ctx)
			require.NoError(t, err)
			return selectHidden(ctx, t, c, parentID, "Container", "__withMountedPathDockerfileCompat", map[string]any{"path": "/m", "source": sourceID})
		}
		id := func(ctr *dagger.Container) dagger.ID {
			id, err := ctr.ID(ctx)
			require.NoError(t, err)
			return id
		}
		return map[string]dagger.ID{
			"evaluated-withDirectory":                     id(evaluated.WithDirectory("/d", source)),
			"evaluated-withFile":                          id(evaluated.WithFile("/f", source.File("source"))),
			"evaluated-withNewFile":                       id(evaluated.WithNewFile("/n", seed)),
			"evaluated-__withMountedPathDockerfileCompat": mounted(evaluated),
			"pending-__withMountedPathDockerfileCompat":   mounted(pending),
		}
	}, func(c *dagger.Client, handles map[string]string) {
		require.Len(t, handles, len(paths))
		for name, handle := range handles {
			contents, err := dagger.Ref[*dagger.Container](c, dagger.ID(handle)).File(paths[name]).Contents(ctx)
			require.NoError(t, err, name)
			require.Equal(t, seed, contents, name)
		}
	})
}

// The result of directory().withFile("go.mod", <src>.file("go.mod")) is a cache
// hit on another engine after it is merged there, for a src whose other files
// changed: the file's content digest travels with the result.
func (RemoteCacheTransferSuite) TestUnchangedFileFromChangedSourceHitsOnAnotherEngine(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	withGoMod := func(c *dagger.Client, variant string) dagger.ID {
		src, err := c.Directory().
			WithNewFile("go.mod", "module "+seed+"\n").
			WithNewFile("main.go", "package main // "+variant+"\n").
			Sync(ctx)
		require.NoError(t, err)
		id, err := c.Directory().WithFile("go.mod", src.File("go.mod")).ID(ctx)
		require.NoError(t, err)
		return id
	}
	onAnotherEngine(ctx, t, false, func(c *dagger.Client) map[string]dagger.ID {
		return map[string]dagger.ID{"go.mod": withGoMod(c, "v1")}
	}, func(c *dagger.Client, handles map[string]string) {
		require.Equal(t, engineResultID(t, handles["go.mod"]), engineResultID(t, string(withGoMod(c, "v2"))), "the same call with a changed source hits the merged result")
	})
}
