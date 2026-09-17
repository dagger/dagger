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
}
