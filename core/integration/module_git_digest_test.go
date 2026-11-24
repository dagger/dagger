package core

import (
	"context"
	"crypto/rand"
	"fmt"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Two engines that check out one commit at one ref must agree on a Git
// module's digest, though the server sent each of them the commit's objects
// packed differently. A Git server may send the same objects in another byte
// form from one fetch to the next, and the checkout's .git keeps the form it
// got.
func (ModuleSuite) TestGitModuleDigestIndependentOfPacking(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	module := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "remote"

[runtime]
source = "dang"
`).
		WithNewFile("main.dang", `type Remote {
  pub hello: String! {
    "hello"
  }
}
`).
		WithNewFile("nonce.txt", rand.Text())
	// A fetch of fewer objects than git's unpack limit (100) is unpacked into
	// loose objects, which every engine compresses the same way. Enough
	// distinct files keep each fetch's pack as the server sent it.
	for i := range 150 {
		module = module.WithNewFile(fmt.Sprintf("data/%03d.txt", i), fmt.Sprintf("file %d\n", i))
	}
	served := makeGitDir(c, module, "main")
	// The same repository, its objects recompressed at another level, so the
	// server packs the same commit into other bytes.
	repacked := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/srv", served).
		WithWorkdir("/srv/repo.git").
		WithExec([]string{"git", "-c", "pack.compression=0", "repack", "-a", "-d", "-f", "-F"}).
		Directory("/srv")

	// A module ref names its host with a dot, as an import path does.
	const gitHost = "gitmodule.test"
	repoURL := "http://" + gitHost + "/repo.git"
	connectEngine := func(gitDir *core.Directory) *dagger.Client {
		gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", gitDir, "", nil)
		devEngine := devEngineContainer(c, func(ctr *core.Container) *core.Container {
			return ctr.WithServiceBinding(gitHost, gitSrv)
		})
		engineSvc, err := core.NewQuery(c).Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = engineSvc.Stop(ctx) })
		endpoint, err := engineSvc.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		t.Cleanup(func() { client.Close() })
		return client
	}
	first := core.NewQuery(connectEngine(served))
	second := core.NewQuery(connectEngine(repacked))

	gitDirDigest := func(q *core.Query) string {
		t.Helper()
		dgst, err := q.Git(repoURL).Branch("main").Tree().Directory(".git").Digest(ctx)
		require.NoError(t, err)
		return dgst
	}
	moduleDigest := func(q *core.Query, ref string) string {
		t.Helper()
		dgst, err := q.ModuleSource(repoURL + "@" + ref).Digest(ctx)
		require.NoError(t, err)
		return dgst
	}

	require.NotEqual(t, gitDirDigest(first), gitDirDigest(second),
		"the two engines' checkouts must hold differently packed .git directories")
	require.Equal(t, moduleDigest(first, "main"), moduleDigest(second, "main"),
		"one commit at one ref must give a module one digest, however its objects were packed")

	// A checkout by commit records a detached HEAD instead of the branch, which
	// a module can read, so it is a different module source.
	sha, err := first.Git(repoURL).Branch("main").CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, moduleDigest(first, "main"), moduleDigest(first, sha),
		"a checkout by commit and one by branch hold different .git directories")
}
