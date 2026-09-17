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

// gitTreesScenario is one fixed repository behind the fixture's Git host: a
// bare copy staged in A's fixture volume and copied, byte for byte, to B's,
// with the advertisement its own upload-pack produces for the visibility
// probe. Commit hashes are therefore equal on both engines.
type gitTreesScenario struct {
	a, b       *fixtureEngine
	repo       string
	first, tip string
}

const gitTreesStageScript = `
	git config --global user.email dagger@example.com
	git config --global user.name "Dagger Tests"
	git config --global init.defaultBranch main
	mkdir -p /work /fixture/git /fixture/origins
	cd /work && git init -q
	echo original > tracked && echo gone > deleted && git add . && git commit -q -m first
	git tag v1
	echo second > tracked && git add . && git commit -q -m second
	git tag v2
	git clone -q --bare /work /fixture/git/repo.git
	{ printf '001e# service=git-upload-pack\n0000'; git upload-pack --advertise-refs --stateless-rpc /fixture/git/repo.git; } > /fixture/origins/info-refs
	chmod -R a+rX /fixture/git
	printf '%s %s' "$(git rev-parse HEAD~1)" "$(git rev-parse HEAD)"
`

func newGitTreesScenario(ctx context.Context, t *testctx.T, name string, stageOnB bool) *gitTreesScenario {
	outer := connect(ctx, t)
	s := &gitTreesScenario{
		a:    newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:    newFixtureEngine(ctx, t, outer, name+"-b", true),
		repo: "https://" + fixturetransport.GitHost + "/repo.git",
	}
	out, err := outer.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedCache("/fixture", s.a.volume).
		WithEnvVariable("CACHEBUST", identity.NewID()).
		WithExec([]string{"sh", "-ec", gitTreesStageScript}).Stdout(ctx)
	require.NoError(t, err)
	hashes := strings.Fields(out)
	require.Len(t, hashes, 2)
	s.first, s.tip = hashes[0], hashes[1]
	s.advertise(t, s.a)
	if stageOnB {
		s.stageOn(t, s.b)
	}
	return s
}

func (s *gitTreesScenario) advertise(t *testctx.T, e *fixtureEngine) {
	scriptOrigin(t, e, fixturetransport.Response{
		URL: s.repo + "/info/refs?service=git-upload-pack", BodyFile: "origins/info-refs",
		Headers: map[string]string{"Content-Type": "application/x-git-upload-pack-advertisement", "Cache-Control": "no-cache"},
	})
}

// stageOn copies A's exact bare repository and advertisement to another
// engine's fixture volume, so the fallback there fetches the same objects.
func (s *gitTreesScenario) stageOn(t *testctx.T, e *fixtureEngine) {
	s.a.volumeExec("mkdir -p /destination/git /destination/origins; cp -a /fixture/git/. /destination/git/; cp /fixture/origins/info-refs /destination/origins/", nil, map[string]*dagger.CacheVolume{"/destination": e.volume})
	s.advertise(t, e)
}

// transfer exports one tree from A with its selected chain and imports it on B.
func (s *gitTreesScenario) transfer(ctx context.Context, t *testctx.T, tree *dagger.Directory) (handle string, rowID uint64) {
	_, err := tree.Sync(ctx)
	require.NoError(t, err)
	id, err := tree.ID(ctx)
	require.NoError(t, err)
	bundle := "tree-" + identity.NewID() + ".json"
	var exported fixtureExportSelectedResult
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export-"+bundle, map[string]any{"bundle": bundle, "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, &exported))
	require.Len(t, exported.Outputs, 1)
	s.a.copyFixtureTo(s.b, bundle)
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", bundle, nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	return imported[0].Handle, imported[0].ResultID
}

// gitFacts reads what identifies a checkout: the tracked file, whether .git
// survived, and, when it did, HEAD and the tags the checkout knows.
func gitFacts(ctx context.Context, t *testctx.T, client *dagger.Client, tree *dagger.Directory) map[string]string {
	t.Helper()
	facts := map[string]string{}
	tracked, err := tree.File("tracked").Contents(ctx)
	require.NoError(t, err)
	facts["tracked"] = tracked
	entries, err := tree.Entries(ctx)
	require.NoError(t, err)
	facts["entries"] = strings.Join(entries, ",")
	if strings.Contains(","+facts["entries"]+",", ",.git/,") {
		out, err := client.Container().From(golangImage).WithExec([]string{"apk", "add", "git"}).
			WithMountedDirectory("/tree", tree).WithWorkdir("/tree").
			WithExec([]string{"sh", "-ec", "git config --global --add safe.directory /tree; git rev-parse HEAD; git tag -l | sort | tr '\\n' ' '"}).Stdout(ctx)
		require.NoError(t, err)
		facts["git"] = strings.TrimSpace(out)
	}
	return facts
}

// TestGitTrees is the native Git trees row (design §5 as amended by B1): both
// backends, Ref and Commit, download and fallback. It carries the rows author
// B folded in from core's TestGitLazyOperationsEvaluate,
// TestGitLazyOperationsRemoteEvaluate and TestValueTransferPartsGitTrees; the
// subtests name them.
func (RemoteCacheTransferSuite) TestGitTrees(ctx context.Context, t *testctx.T) {
	type treeCase struct {
		name string
		tree func(s *gitTreesScenario, client *dagger.Client) *dagger.Directory
	}
	remoteTrees := []treeCase{
		{"RefDiscardOnCall", func(s *gitTreesScenario, c *dagger.Client) *dagger.Directory {
			return c.Git(s.repo).Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true, Depth: 1, IncludeTags: true})
		}},
		{"RefKeepGitDirWithTags", func(s *gitTreesScenario, c *dagger.Client) *dagger.Directory {
			return c.Git(s.repo, dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 1, IncludeTags: true})
		}},
		{"CommitKeepGitDirWithTags", func(s *gitTreesScenario, c *dagger.Client) *dagger.Directory {
			return c.Git(s.repo, dagger.GitOpts{KeepGitDir: true}).Commit(s.first).Tree(dagger.GitCommitTreeOpts{Depth: 1, IncludeTags: true})
		}},
	}

	// Folded row, from TestValueTransferPartsGitTrees (remote x download): B
	// has no repository behind the Git host at all, so the tree can only come
	// from the offered chain.
	t.Run("RemoteDownload", func(ctx context.Context, t *testctx.T) {
		s := newGitTreesScenario(ctx, t, "git-download", false)
		for _, tc := range remoteTrees {
			want := gitFacts(ctx, t, s.a.client, tc.tree(s, s.a.client))
			handle, rowID := s.transfer(ctx, t, tc.tree(s, s.a.client))
			got := gitFacts(ctx, t, s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
			require.Equal(t, want, got, tc.name)
			var report fixtureControlsReport
			require.NoError(t, s.b.fixture("report", "", nil, &report))
			require.Len(t, partEventsOf(report.transferFixtureReport, rowID, "installed-chain"), 1, tc.name)
			require.Empty(t, partEventsOf(report.transferFixtureReport, rowID, "lazy-enter"), "%s: no checkout ran on B", tc.name)
		}
	})

	// Folded rows, from TestGitLazyOperationsRemoteEvaluate (remote over HTTP
	// x fallback) and TestGitLazyOperationsEvaluate (Ref and Commit, discard
	// on the call, depth 1, tags included): the offered chain's reader fails,
	// the saved checkout runs on B against the same repository through the
	// real Git executable, and the re-evaluated tree equals A's eager tree.
	t.Run("RemoteFallback", func(ctx context.Context, t *testctx.T) {
		s := newGitTreesScenario(ctx, t, "git-fallback", true)
		for _, tc := range remoteTrees {
			want := gitFacts(ctx, t, s.a.client, tc.tree(s, s.a.client))
			handle, rowID := s.transfer(ctx, t, tc.tree(s, s.a.client))
			require.NoError(t, s.b.fixture("barrierArm", s.b.control("chain-"+tc.name+".json", dagql.FixtureBarrierRequest{Key: "chain-" + tc.name, Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: rowID}, Action: dagql.FixtureFailChainOpen}), nil, nil))
			got := gitFacts(ctx, t, s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
			require.Equal(t, want, got, tc.name)
			var report fixtureControlsReport
			require.NoError(t, s.b.fixture("report", "", nil, &report))
			t.Logf("%s: R=%d events: %v", tc.name, rowID, partKindsOf(report.transferFixtureReport, rowID))
			require.Len(t, partEventsOf(report.transferFixtureReport, rowID, "lazy-enter"), 1, "%s: the saved checkout ran once", tc.name)
			require.Empty(t, partEventsOf(report.transferFixtureReport, rowID, "installed-chain"), tc.name)
		}
	})
	// The local backend: a repository that is an ordinary Directory, built
	// once on A by a real git run. Its chain travels selected too, so B has
	// the same objects and hashes without running that exec; only the tree's
	// own chain fails, by row.
	localRepo := func(ctx context.Context, t *testctx.T, client *dagger.Client) (*dagger.Directory, string, string) {
		ctr := client.Container().From(golangImage).WithExec([]string{"apk", "add", "git"}).
			WithEnvVariable("NONCE", identity.NewID()).
			WithExec([]string{"sh", "-ec", `
				git config --global user.email dagger@example.com
				git config --global user.name "Dagger Tests"
				git config --global init.defaultBranch main
				mkdir /repo && cd /repo && git init -q
				echo original > tracked && echo gone > deleted && echo "$NONCE" > nonce && git add . && git commit -q -m first
				git tag v1
				echo second > tracked && git add . && git commit -q -m second
				git tag v2
				git branch alternate
				printf '%s %s' "$(git rev-parse HEAD~1)" "$(git rev-parse HEAD)" > /hashes
			`})
		hashes, err := ctr.File("/hashes").Contents(ctx)
		require.NoError(t, err)
		fields := strings.Fields(hashes)
		require.Len(t, fields, 2)
		return ctr.Directory("/repo"), fields[0], fields[1]
	}
	// transferWithRepo exports a tree with its own chain and its repository
	// Directory's, imports both on B and returns the tree's handle and row.
	var lastRepoHandle string
	transferWithRepo := func(ctx context.Context, t *testctx.T, s *gitTreesScenario, repo, tree *dagger.Directory, more ...string) (string, uint64) {
		_, err := tree.Sync(ctx)
		require.NoError(t, err)
		snapshot := dagql.PersistedPartAddress{Part: "snapshot"}
		var handles []string
		var outputs []map[string]any
		for _, dir := range []*dagger.Directory{tree, repo} {
			id, err := dir.ID(ctx)
			require.NoError(t, err)
			handles = append(handles, string(id))
		}
		handles = append(handles, more...)
		for _, handle := range handles {
			outputs = append(outputs, map[string]any{"handle": handle, "address": snapshot})
		}
		bundle := "local-" + identity.NewID() + ".json"
		require.NoError(t, s.a.fixture("exportSelected", s.a.control("export-"+bundle, map[string]any{"bundle": bundle, "outputs": outputs}), handles, nil))
		s.a.copyFixtureTo(s.b, bundle)
		var imported []transferFixtureMapping
		require.NoError(t, s.b.fixture("import", bundle, nil, &imported))
		require.GreaterOrEqual(t, len(imported), 2)
		require.Equal(t, "Directory", imported[0].Type.NamedType)
		require.Equal(t, "Directory", imported[1].Type.NamedType)
		lastRepoHandle = imported[1].Handle
		return imported[0].Handle, imported[0].ResultID
	}
	failTreeChain := func(t *testctx.T, s *gitTreesScenario, key string, rowID uint64) {
		require.NoError(t, s.b.fixture("barrierArm", s.b.control("chain-"+key+".json", dagql.FixtureBarrierRequest{Key: "chain-" + key, Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: rowID}, Action: dagql.FixtureFailChainOpen}), nil, nil))
	}
	ranOnce := func(t *testctx.T, s *gitTreesScenario, name string, rowID uint64) {
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("%s: R=%d events: %v", name, rowID, partKindsOf(report.transferFixtureReport, rowID))
		require.Len(t, partEventsOf(report.transferFixtureReport, rowID, "lazy-enter"), 1, "%s: the saved operation ran once", name)
		require.Empty(t, partEventsOf(report.transferFixtureReport, rowID, "installed-chain"), name)
	}

	// Folded rows, from TestGitLazyOperationsEvaluate (local x Ref and Commit
	// x fallback, discard on the call, depth 1, tags included) and from
	// TestValueTransferPartsGitTrees (local x download).
	t.Run("Local", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		s := &gitTreesScenario{a: newFixtureEngine(ctx, t, outer, "git-local-a", true), b: newFixtureEngine(ctx, t, outer, "git-local-b", true)}
		repo, first, _ := localRepo(ctx, t, s.a.client)
		trees := map[string]func() *dagger.Directory{
			"RefDiscardOnCall": func() *dagger.Directory {
				return repo.AsGit().Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true, Depth: 1, IncludeTags: true})
			},
			"RefKeepGitDirWithTags": func() *dagger.Directory {
				return repo.AsGit().Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 1, IncludeTags: true})
			},
			"CommitKeepGitDirWithTags": func() *dagger.Directory {
				return repo.AsGit().Commit(first).Tree(dagger.GitCommitTreeOpts{Depth: 1, IncludeTags: true})
			},
		}
		for name, tree := range trees {
			want := gitFacts(ctx, t, s.a.client, tree())
			// Download: the tree comes from its chain and nothing runs.
			handle, rowID := transferWithRepo(ctx, t, s, repo, tree())
			got := gitFacts(ctx, t, s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
			require.Equal(t, want, got, "%s download", name)
			var report fixtureControlsReport
			require.NoError(t, s.b.fixture("report", "", nil, &report))
			require.Empty(t, partEventsOf(report.transferFixtureReport, rowID, "lazy-enter"), "%s download: no checkout ran", name)
		}
		// Fallback needs rows B has not installed yet, so it gets its own B.
		s.b = newFixtureEngine(ctx, t, outer, "git-local-fallback-b", true)
		for name, tree := range trees {
			want := gitFacts(ctx, t, s.a.client, tree())
			handle, rowID := transferWithRepo(ctx, t, s, repo, tree())
			failTreeChain(t, s, name, rowID)
			got := gitFacts(ctx, t, s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
			require.Equal(t, want, got, "%s fallback: the re-evaluated tree equals the eager one", name)
			ranOnce(t, s, name, rowID)
		}
	})

	// Folded row, from TestGitLazyOperationsEvaluate: the cleaned tree of a
	// dirty repository restores tracked and deleted files and leaves the
	// source's index and dirty file untouched. An uncommitted Changeset's
	// "before" side is that cleaned tree.
	t.Run("LocalCleaned", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		s := &gitTreesScenario{a: newFixtureEngine(ctx, t, outer, "git-cleaned-a", true), b: newFixtureEngine(ctx, t, outer, "git-cleaned-b", true)}
		clean, _, _ := localRepo(ctx, t, s.a.client)
		dirty := s.a.client.Container().From(alpineImage).WithDirectory("/repo", clean).
			WithExec([]string{"sh", "-ec", "cd /repo && echo dirty > tracked && rm deleted && echo stray > untracked"}).Directory("/repo")
		cleaned := dirty.AsGit().Uncommitted().Before()
		read := func(client *dagger.Client, dir *dagger.Directory) map[string]string {
			out := map[string]string{}
			for _, name := range []string{"tracked", "deleted"} {
				contents, err := dir.File(name).Contents(ctx)
				require.NoError(t, err, name)
				out[name] = contents
			}
			entries, err := dir.Entries(ctx)
			require.NoError(t, err)
			out["entries"] = strings.Join(entries, ",")
			return out
		}
		want := read(s.a.client, cleaned)
		require.Equal(t, "second\n", want["tracked"], "the cleaned tree restores the tracked file")
		require.Equal(t, "gone\n", want["deleted"], "and the deleted one")
		require.NotContains(t, want["entries"], "untracked")

		handle, rowID := transferWithRepo(ctx, t, s, dirty, cleaned)
		failTreeChain(t, s, "cleaned", rowID)
		got := read(s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
		require.Equal(t, want, got, "the re-evaluated cleaned tree equals the eager one")
		ranOnce(t, s, "cleaned", rowID)

		// The source repository on B is untouched by that evaluation.
		source := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(lastRepoHandle))
		stillDirty, err := source.File("tracked").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "dirty\n", stillDirty, "the source's dirty file is untouched")
		wantIndex, err := dirty.File(".git/index").Digest(ctx)
		require.NoError(t, err)
		gotIndex, err := source.File(".git/index").Digest(ctx)
		require.NoError(t, err)
		require.Equal(t, wantIndex, gotIndex, "and so is its index")
	})
	// Folded row, from TestGitBundleLazyOperationEvaluate: a full and an
	// incremental bundle. The target repository and the bundle's source both
	// reach B by their chains; the tree's own chain fails, so B re-evaluates
	// the checkout, which has to import the bundle again with the real Git
	// executable, and the result equals A's.
	t.Run("LocalBundle", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		s := &gitTreesScenario{a: newFixtureEngine(ctx, t, outer, "git-bundle-a", true), b: newFixtureEngine(ctx, t, outer, "git-bundle-b", true)}
		source, _, tip := localRepo(ctx, t, s.a.client)
		git := func(script string, from *dagger.Directory) *dagger.Directory {
			ctr := s.a.client.Container().From(golangImage).WithExec([]string{"apk", "add", "git"})
			if from != nil {
				ctr = ctr.WithDirectory("/repo", from)
			}
			return ctr.WithExec([]string{"sh", "-ec", "git config --global init.defaultBranch main; mkdir -p /repo; cd /repo; " + script}).Directory("/repo")
		}
		for _, tc := range []struct {
			name   string
			target *dagger.Directory
			bundle func() *dagger.GitBundle
		}{
			{"Full", git("git init -q", nil), func() *dagger.GitBundle { return source.AsGit().Bundle([]string{"main", "alternate"}) }},
			{"Incremental", git("git checkout -q main; git reset -q --hard v1; git branch -D alternate; git tag -d v2", source), func() *dagger.GitBundle {
				return source.AsGit().Bundle([]string{"main", "alternate"}, dagger.GitRepositoryBundleOpts{Base: source.AsGit().Tag("v1")})
			}},
		} {
			tree := func() *dagger.Directory {
				return tc.target.AsGit().WithBundle(tc.bundle()).Branch("alternate").Tree(dagger.GitRefTreeOpts{Depth: 1})
			}
			want := gitFacts(ctx, t, s.a.client, tree())
			require.Contains(t, want["git"], tip, "%s: the imported branch is at the bundle's tip", tc.name)
			require.Equal(t, "second\n", want["tracked"])
			// The bundle is a File with no saved producer of its own, so it
			// travels by its chain like the two repositories.
			sourceID, err := source.ID(ctx)
			require.NoError(t, err)
			bundleID, err := tc.bundle().AsFile().ID(ctx)
			require.NoError(t, err)
			handle, rowID := transferWithRepo(ctx, t, s, tc.target, tree(), string(sourceID), string(bundleID))
			failTreeChain(t, s, "bundle-"+tc.name, rowID)
			got := gitFacts(ctx, t, s.b.client, dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(handle)))
			require.Equal(t, want, got, "%s: the re-evaluated tree equals the eager one", tc.name)
			ranOnce(t, s, "bundle "+tc.name, rowID)
			var report fixtureControlsReport
			require.NoError(t, s.b.fixture("report", "", nil, &report))
			imports := 0
			for _, event := range report.Parts {
				if event.Kind == "lazy-enter" {
					t.Logf("  %s lazy-enter row=%d field=%s", tc.name, event.ResultID, event.Field)
					if event.Field == "__withBundleDirectory" {
						imports++
					}
				}
			}
			require.Positive(t, imports, "%s: the bundle import itself was re-evaluated on B", tc.name)
		}
	})
}
