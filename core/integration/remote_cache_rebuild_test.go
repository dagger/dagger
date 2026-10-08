package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// rebuildOnAnotherEngine builds values on engine A, exports them with no
// blob, imports them into engine B and evaluates each imported row there, so
// B must rebuild every part from its saved operation. check reads the
// rebuilt values through B's client, by name. It returns the exported record
// of each value, by name.
func rebuildOnAnotherEngine(ctx context.Context, t *testctx.T, build func(*dagger.Client) map[string]core.ID, check func(*dagger.Client, map[string]string)) map[string]dagql.PersistedRecord {
	return onAnotherEngine(ctx, t, true, func(a *dagger.Client, _ func() *dagger.Client) map[string]core.ID {
		return build(a)
	}, check)
}

// onAnotherEngine builds values on engine A, exports them with no blob and
// imports them into engine B, evaluating each imported row there when rebuild
// is set. build can open more sessions on A with connectA. check gets B's
// client and the imported handles, by name. It returns the exported record of
// each value, by name.
func onAnotherEngine(ctx context.Context, t *testctx.T, rebuild bool, build func(a *dagger.Client, connectA func() *dagger.Client) map[string]core.ID, check func(*dagger.Client, map[string]string)) map[string]dagql.PersistedRecord {
	outer := connect(ctx, t)
	type running struct {
		upstream, tunnel *core.Service
		endpoint         string
		client           *dagger.Client
	}
	start := func(state string, volume *core.CacheVolume) *running {
		ctr := devEngineContainerWithStateKey(outer, state, func(ctr *core.Container) *core.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		e := &running{upstream: devEngineContainerAsService(ctr)}
		tunnel, err := core.NewQuery(outer).Host().Tunnel(e.upstream).Start(ctx)
		require.NoError(t, err)
		e.tunnel = tunnel
		e.endpoint, err = tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		e.client, err = dagger.Connect(ctx, dagger.WithRunnerHost(e.endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		return e
	}
	id := identity.NewID()
	aVolume := core.NewQuery(outer).CacheVolume("rebuild-a-" + id)
	bVolume := core.NewQuery(outer).CacheVolume("rebuild-b-" + id)
	a := start("rebuild-a-state-"+id, aVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &a.client, &a.upstream, &a.tunnel)) }()
	var sessionsA []*dagger.Client
	defer func() {
		for _, c := range sessionsA {
			require.NoError(t, closeClientBounded(ctx, c))
		}
	}()
	connectA := func() *dagger.Client {
		c, err := dagger.Connect(ctx, dagger.WithRunnerHost(a.endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		sessionsA = append(sessionsA, c)
		return c
	}
	b := start("rebuild-b-state-"+id, bVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &b.client, &b.upstream, &b.tunnel)) }()

	roots := build(a.client, connectA)
	for name, root := range roots {
		var exported []transferFixtureMapping
		require.NoError(t, transferFixtureSelected(ctx, a.client, "rebuild-"+name+".json", []string{string(root)}, []string{}, &exported), name)
		require.NotEmpty(t, exported, name)
	}
	_, err := core.NewQuery(outer).Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/rebuild-*.json /destination/bundles/"}).Sync(ctx)
	require.NoError(t, err)
	records := map[string]dagql.PersistedRecord{}
	rootOrdinals := map[string]dagql.TransferOrdinal{}
	for name := range roots {
		raw, err := core.NewQuery(outer).Container().From(alpineImage).WithMountedCache("/source", aVolume).
			WithEnvVariable("READ", identity.NewID()).WithExec([]string{"cat", "/source/bundles/rebuild-" + name + ".json"}).Stdout(ctx)
		require.NoError(t, err, name)
		var bundle dagql.ValueBundle
		require.NoError(t, json.Unmarshal([]byte(raw), &bundle), name)
		require.Len(t, bundle.Roots, 1, name)
		rootOrdinals[name] = bundle.Roots[0].Ordinal
		for _, value := range bundle.Values {
			if value.Ordinal == bundle.Roots[0].Ordinal {
				records[name] = value.Record
			}
		}
		require.Contains(t, records, name)
	}

	handles := map[string]string{}
	for name := range roots {
		var imported []transferFixtureMapping
		require.NoError(t, transferFixture(ctx, b.client, "import", "rebuild-"+name+".json", []string{}, &imported), name)
		// The bundle's one root, and only it, lands on B.
		require.NotEmpty(t, imported, name)
		var importedRoots []dagql.TransferOrdinal
		for _, mapping := range imported {
			if mapping.Root {
				importedRoots = append(importedRoots, mapping.Ordinal)
			}
		}
		require.Equal(t, []dagql.TransferOrdinal{rootOrdinals[name]}, importedRoots, name)
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
	return records
}

// requireRebuildRoute asserts that every part of an exported value can be
// rebuilt on another engine, as that engine routes a demand for it: by the
// value's saved operation, or by delegation to its parent.
func requireRebuildRoute(t *testctx.T, record dagql.PersistedRecord) {
	t.Helper()
	family, ok := dagql.PersistedObjectFamilyByName(record.Envelope.ObjectCodec)
	require.True(t, ok, record.Envelope.ObjectCodec)
	router, ok := family.Transfer.(dagql.PersistedPartRouter)
	require.True(t, ok, record.Envelope.ObjectCodec)
	var payload struct {
		Parts map[dagql.PartKey]json.RawMessage `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	parts := []dagql.PartKey{"snapshot"}
	if payload.Parts != nil {
		parts = slices.Collect(maps.Keys(payload.Parts))
	}
	require.NotEmpty(t, parts)
	for _, part := range parts {
		route, err := router.RouteParts(dagql.PersistedPayloadVisit{Call: record.Call, Payload: record.Envelope.ObjectJSON}, part)
		require.NoError(t, err, part)
		require.True(t, route.HasLazyOperation || route.Delegation != nil, "part %s has neither a saved operation nor a delegation", part)
	}
}

// savedOperation is the operation an exported value keeps to rebuild itself:
// its kind, for a Directory or File, and its arguments. A Container's
// operation is named by its call's field.
func savedOperation(t *testctx.T, record dagql.PersistedRecord) (string, json.RawMessage) {
	t.Helper()
	var payload struct {
		LazyKind string          `json:"lazyKind"`
		LazyJSON json.RawMessage `json:"lazyJSON"`
	}
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	return payload.LazyKind, payload.LazyJSON
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
func selectHidden(ctx context.Context, t *testctx.T, c *dagger.Client, id core.ID, typ, field string, args map[string]any) core.ID {
	t.Helper()
	decls, params := []string{"$id: ID!"}, []string{}
	vars := map[string]any{"id": id}
	for _, name := range slices.Sorted(maps.Keys(args)) {
		var argType string
		switch args[name].(type) {
		case core.ID:
			argType = "ID!"
		case []core.ID:
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
		Node map[string]struct{ ID core.ID }
	}
	query := fmt.Sprintf("query(%s) { node(id: $id) { ... on %s { %s(%s) { id } } } }", strings.Join(decls, ", "), typ, field, strings.Join(params, ", "))
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: &result}), field)
	require.NotEmpty(t, result.Node[field].ID, field)
	return result.Node[field].ID
}

// requirePending asserts whether each value still has work to run on the
// engine c is connected to, without running it.
func requirePending(ctx context.Context, t *testctx.T, c *dagger.Client, want bool, ids ...core.ID) {
	t.Helper()
	handles := make([]string, len(ids))
	for i, id := range ids {
		handles[i] = string(id)
	}
	var pending []bool
	require.NoError(t, transferFixture(ctx, c, "pending", "", handles, &pending))
	require.Len(t, pending, len(ids))
	for i, got := range pending {
		require.Equal(t, want, got, "value %d", i)
	}
}

// syncedID evaluates value and returns its ID.
func syncedID[T interface {
	Sync(context.Context) (T, error)
	ID(context.Context) (core.ID, error)
}](ctx context.Context, t *testctx.T, value T) core.ID {
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
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		ctr := core.NewQuery(c).Container().From(alpineImage).WithNewFile("/data", "rebuilt "+seed)
		tarball, err := ctr.AsTarball().Sync(ctx)
		require.NoError(t, err)
		manifest, err := ctr.Manifest().Sync(ctx)
		require.NoError(t, err)
		tarballID, err := tarball.ID(ctx)
		require.NoError(t, err)
		manifestID, err := manifest.ID(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"tarball": tarballID, "manifest": manifestID}
	}, func(c *dagger.Client, handles map[string]string) {
		tarball := core.Ref[*core.File](core.NewQuery(c), core.ID(handles["tarball"]))
		contents, err := core.NewQuery(c).Container().Import(tarball).File("/data").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "rebuilt "+seed, contents)
		manifest, err := core.Ref[*core.File](core.NewQuery(c), core.ID(handles["manifest"])).Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, manifest, `"layers"`)
	})
}

// A Docker build is rebuilt on another engine when its blob is missing.
func (RemoteCacheTransferSuite) TestRebuildDockerBuild(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		built, err := core.NewQuery(c).Directory().
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN echo "+seed+" > /built\n").
			DockerBuild().
			Sync(ctx)
		require.NoError(t, err)
		builtID, err := built.ID(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"build": builtID}
	}, func(c *dagger.Client, handles map[string]string) {
		contents, err := core.Ref[*core.Container](core.NewQuery(c), core.ID(handles["build"])).File("/built").Contents(ctx)
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
	manifestOf := func(c *dagger.Client) (*core.Container, *core.File) {
		ctr := core.NewQuery(c).Container().From(alpineImage).WithNewFile("/data", "per engine "+seed)
		return ctr, ctr.Manifest()
	}
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		_, manifest := manifestOf(c)
		manifest, err := manifest.Sync(ctx)
		require.NoError(t, err)
		id, err := manifest.ID(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"manifest": id}
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
	records := rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		before := core.NewQuery(c).Directory().WithNewFile("data", "before\n")
		patch, err := before.WithNewFile("data", seed+"\n").Changes(before).AsPatch().ID(ctx)
		require.NoError(t, err)
		requirePending(ctx, t, c, false, patch) // the patch is written at the call
		return map[string]core.ID{"patch": patch}
	}, func(c *dagger.Client, handles map[string]string) {
		contents, err := core.Ref[*core.File](core.NewQuery(c), core.ID(handles["patch"])).Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, contents, "+"+seed)
	})
	kind, _ := savedOperation(t, records["patch"])
	require.Equal(t, "changeset.asPatch", kind)
	requireRebuildRoute(t, records["patch"])
}

// Changeset merges are rebuilt on another engine when their blob is missing.
func (RemoteCacheTransferSuite) TestRebuildChangesetMerges(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	records := rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		change := func(path string) core.ID {
			before := core.NewQuery(c).Directory().WithNewFile(path, "0\n")
			return syncedID(ctx, t, before.WithNewFile(path, seed+"\n").Changes(before))
		}
		ours, theirs, third := change("ours"), change("theirs"), change("third")
		merge := func(field string, changes any) core.ID {
			merged := selectHidden(ctx, t, c, ours, "Changeset", field, map[string]any{"changes": changes})
			// The merge runs at the call. Check before the next merge, which
			// may run this one.
			requirePending(ctx, t, c, false, merged)
			return merged
		}
		return map[string]core.ID{
			"__mergeWithChangeset":      merge("__mergeWithChangeset", theirs),
			"__mergeWithChangesets":     merge("__mergeWithChangesets", []core.ID{theirs, third}),
			"__mergeForWorkspaceCommit": merge("__mergeForWorkspaceCommit", theirs),
		}
	}, func(c *dagger.Client, handles map[string]string) {
		for field, handle := range handles {
			paths := []string{"ours", "theirs"}
			if field == "__mergeWithChangesets" {
				paths = append(paths, "third")
			}
			for _, path := range paths {
				contents, err := core.Ref[*core.Directory](core.NewQuery(c), core.ID(handle)).File(path).Contents(ctx)
				require.NoError(t, err, field)
				require.Equal(t, seed+"\n", contents, field)
			}
		}
	})
	for field, record := range records {
		kind, raw := savedOperation(t, record)
		require.Equal(t, "changeset.merge", kind, field)
		var merge struct{ Workspace bool }
		require.NoError(t, json.Unmarshal(raw, &merge), field)
		require.Equal(t, field == "__mergeForWorkspaceCommit", merge.Workspace, field)
		requireRebuildRoute(t, record)
	}
}

// A merge conflict is reported by the call that asks for the merge, as before
// merges saved their operation.
func (RemoteCacheTransferSuite) TestChangesetMergeConflictAtCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	seed := identity.NewID()
	change := func(contents string) *core.Changeset {
		before := core.NewQuery(c).Directory().WithNewFile("data", "base "+seed+"\n")
		return before.WithNewFile("data", contents).Changes(before)
	}
	_, err := change("ours\n").WithChangeset(change("theirs\n")).ID(ctx)
	require.ErrorContains(t, err, "conflict")
}

// rebuildGitRepo is a repository with one commit of file.txt. Its dates are
// fixed, so another engine that runs it again makes the same commit.
func rebuildGitRepo(c *dagger.Client, seed string) *core.GitRef {
	return core.NewQuery(c).Container().From(alpineImage).
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
func rebuildGitChanges(head *core.GitRef, contents string) *core.Changeset {
	before := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true})
	return before.WithNewFile("file.txt", contents).Changes(before)
}

// requireRebuiltCommit asserts that the Git storage in dir has HEAD sha and
// file.txt contents.
func requireRebuiltCommit(ctx context.Context, t *testctx.T, dir *core.Directory, sha, contents string) {
	t.Helper()
	head := dir.AsGit().Head()
	got, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, got)
	file, err := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true}).File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, file)
}

// GitRef.withCommit's Git storage is rebuilt on another engine when its blob is
// missing, with the same commit.
func (RemoteCacheTransferSuite) TestRebuildGitCommitDirectory(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	var sha string
	records := rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
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
		requirePending(ctx, t, c, false, committed) // the commit is made at the call
		sha, err = core.Ref[*core.Directory](core.NewQuery(c), committed).AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"commit": committed}
	}, func(c *dagger.Client, handles map[string]string) {
		requireRebuiltCommit(ctx, t, core.Ref[*core.Directory](core.NewQuery(c), core.ID(handles["commit"])), sha, "edited "+seed+"\n")
	})
	kind, _ := savedOperation(t, records["commit"])
	require.Equal(t, "gitCommit", kind)
	requireRebuildRoute(t, records["commit"])
}

// Workspace.__pullDirectory is rebuilt on another engine when its blob is
// missing, with the same history.
func (RemoteCacheTransferSuite) TestRebuildWorkspacePullDirectory(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	var sha string
	records := rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		head := rebuildGitRepo(c, seed)
		asWorkspace := core.GitRefAsWorkspaceOpts{Cwd: "/"}
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
		requirePending(ctx, t, c, false, pulled) // the pull runs at the call
		sha, err = core.Ref[*core.Directory](core.NewQuery(c), pulled).AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"pull": pulled}
	}, func(c *dagger.Client, handles map[string]string) {
		requireRebuiltCommit(ctx, t, core.Ref[*core.Directory](core.NewQuery(c), core.ID(handles["pull"])), sha, "pulled "+seed+"\n")
	})
	kind, _ := savedOperation(t, records["pull"])
	require.Equal(t, "workspace.pull", kind)
	requireRebuildRoute(t, records["pull"])
}

// A file write and metadata edits over evaluated parents are rebuilt on
// another engine when the written bytes are not available there.
func (RemoteCacheTransferSuite) TestRebuildContainerMutations(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		written, err := core.NewQuery(c).Container().WithNewFile("/data", seed).Sync(ctx)
		require.NoError(t, err)
		user, err := written.WithUser("app").Sync(ctx)
		require.NoError(t, err)
		return map[string]core.ID{"edited": syncedID(ctx, t, user.WithShell([]string{"/bin/ash"}))}
	}, func(c *dagger.Client, handles map[string]string) {
		ctr := core.Ref[*core.Container](core.NewQuery(c), core.ID(handles["edited"]))
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
// compat mount keeps it over a pending parent too, and runs on first read.
func (RemoteCacheTransferSuite) TestRebuildContainerWritesOverEvaluatedParent(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	paths := map[string]string{
		"evaluated-withDirectory":                     "/d/source",
		"evaluated-withFile":                          "/f",
		"evaluated-withNewFile":                       "/n",
		"evaluated-__withMountedPathDockerfileCompat": "/m/source",
		"pending-__withMountedPathDockerfileCompat":   "/m/source",
	}
	records := rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]core.ID {
		source := core.NewQuery(c).Directory().WithNewFile("source", seed)
		sourceID := syncedID(ctx, t, source)
		// Distinct roots keep the evaluated parent from being the pending one.
		evaluated, err := core.NewQuery(c).Container().WithRootfs(core.NewQuery(c).Directory().WithNewDirectory("evaluated")).Sync(ctx)
		require.NoError(t, err)
		pending := core.NewQuery(c).Container().WithRootfs(core.NewQuery(c).Directory().WithNewDirectory("pending"))
		mounted := func(parent *core.Container) core.ID {
			parentID, err := parent.ID(ctx)
			require.NoError(t, err)
			return selectHidden(ctx, t, c, parentID, "Container", "__withMountedPathDockerfileCompat", map[string]any{"path": "/m", "source": sourceID})
		}
		id := func(ctr *core.Container) core.ID {
			id, err := ctr.ID(ctx)
			require.NoError(t, err)
			return id
		}
		written := map[string]core.ID{
			"evaluated-withDirectory":                     id(evaluated.WithDirectory("/d", source)),
			"evaluated-withFile":                          id(evaluated.WithFile("/f", source.File("source"))),
			"evaluated-withNewFile":                       id(evaluated.WithNewFile("/n", seed)),
			"evaluated-__withMountedPathDockerfileCompat": mounted(evaluated),
			"pending-__withMountedPathDockerfileCompat":   mounted(pending),
		}
		// The work runs when it always did: at the call over a built parent, on
		// first read over a pending one.
		for name, id := range written {
			requirePending(ctx, t, c, strings.HasPrefix(name, "pending-"), id)
			// Container.user settles the metadata only; no part is read.
			_, err := core.Ref[*core.Container](core.NewQuery(c), id).User(ctx)
			require.NoError(t, err, name)
		}
		return written
	}, func(c *dagger.Client, handles map[string]string) {
		require.Len(t, handles, len(paths))
		for name, handle := range handles {
			contents, err := core.Ref[*core.Container](core.NewQuery(c), core.ID(handle)).File(paths[name]).Contents(ctx)
			require.NoError(t, err, name)
			require.Equal(t, seed, contents, name)
		}
	})
	require.Len(t, records, len(paths))
	for name, record := range records {
		_, raw := savedOperation(t, record)
		require.NotEmpty(t, raw, "%s must keep its saved operation", name)
		requireRebuildRoute(t, record)
	}
}

// The result of directory().withFile("go.mod", <src>.file("go.mod")) is a cache
// hit for a src whose other files changed: in a second session on the engine
// that made it, and on another engine after it is merged there, because the
// file's content digest travels with the result.
func (RemoteCacheTransferSuite) TestUnchangedFileFromChangedSourceHitsOnAnotherEngine(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	// goModSource is evaluated first, as a host directory is.
	goModSource := func(c *dagger.Client, variant string) *core.Directory {
		src, err := core.NewQuery(c).Directory().
			WithNewFile("go.mod", "module "+seed+"\n").
			WithNewFile("main.go", "package main // "+variant+"\n").
			Sync(ctx)
		require.NoError(t, err)
		return src
	}
	withGoMod := func(c *dagger.Client, src *core.Directory) core.ID {
		id, err := core.NewQuery(c).Directory().WithFile("go.mod", src.File("go.mod")).ID(ctx)
		require.NoError(t, err)
		return id
	}
	onAnotherEngine(ctx, t, false, func(c *dagger.Client, connectA func() *dagger.Client) map[string]core.ID {
		src := goModSource(c, "v1")
		var before transferFixtureReport
		require.NoError(t, transferFixture(ctx, c, "report", "", []string{}, &before))
		first := withGoMod(c, src)
		// A hit would answer with a result the engine already had.
		for _, row := range before.Rows {
			require.NotEqual(t, row.ResultID, engineResultID(t, string(first)), "the first call runs")
		}
		again := connectA()
		require.Equal(t, engineResultID(t, string(first)), engineResultID(t, string(withGoMod(again, goModSource(again, "v2")))), "a second session on the same engine hits")
		return map[string]core.ID{"go.mod": first}
	}, func(c *dagger.Client, handles map[string]string) {
		require.Equal(t, engineResultID(t, handles["go.mod"]), engineResultID(t, string(withGoMod(c, goModSource(c, "v2")))), "the same call on the other engine after the merge")
	})
}
