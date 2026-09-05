package core

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type gitPushReceipt struct {
	ID                                 dagger.ID
	Ref, PreviousSHA, SHA, Disposition string
}

func pushGitRef(ctx context.Context, c *dagger.Client, source *dagger.GitRef, destination *dagger.GitRepository, branch string, expected *string) (gitPushReceipt, error) {
	var response struct{ Node struct{ Push gitPushReceipt } }
	id, err := source.ID(ctx)
	if err != nil {
		return gitPushReceipt{}, err
	}
	var dest any
	if destination != nil {
		dest, err = destination.ID(ctx)
		if err != nil {
			return gitPushReceipt{}, err
		}
	}
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $to: ID, $branch: String!, $expected: String) { node(id:$id) { ... on GitRef { push(to:$to, branch:$branch, expectedRemoteSHA:$expected) { id ref previousSHA sha disposition } } } }`,
		Variables: map[string]any{"id": id, "to": dest, "branch": branch, "expected": expected},
	}, &dagger.Response{Data: &response})
	return response.Node.Push, err
}

func pushRemoteSHA(ctx context.Context, t *testctx.T, c *dagger.Client, service *dagger.Service, url, ref string) string {
	t.Helper()
	out, err := c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("remote", service).WithEnvVariable("CACHEBUST", identity.NewID()).
		WithExec([]string{"git", "ls-remote", url, ref}).Stdout(ctx)
	require.NoError(t, err)
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (GitSuite) TestPushWorkspaces(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base", "base"))
	_, err := service.Start(ctx)
	require.NoError(t, err)
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	baseRef := repo.Branch("main")
	baseSHA, err := baseRef.CommitSHA(ctx)
	require.NoError(t, err)
	result, err := pushGitRef(ctx, c, baseRef, nil, "", nil)
	require.NoError(t, err)
	require.Equal(t, "UP_TO_DATE", result.Disposition)
	require.Equal(t, "refs/heads/main", result.Ref)
	ws := baseRef.AsWorkspace().WithNewFile("committed", "yes").WithCommit("new commit", workspaceCommitDate).WithNewFile("pending", "not pushed")
	sha, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	result, err = pushGitRef(ctx, c, ws.Git().Head(), repo, "main", nil)
	require.NoError(t, err)
	require.Equal(t, "FAST_FORWARD", result.Disposition)
	require.Equal(t, baseSHA, result.PreviousSHA)
	require.Equal(t, sha, result.SHA)
	defaulted, err := pushGitRef(ctx, c, ws.Git().Head(), nil, "default-url", nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", defaulted.Disposition)
	require.Equal(t, sha, pushRemoteSHA(ctx, t, c, service, url, "refs/heads/main"))
	result, err = pushGitRef(ctx, c, ws.Git().Head(), repo, "main", nil)
	require.NoError(t, err)
	require.Equal(t, "UP_TO_DATE", result.Disposition, "push is not cached")
	exists, err := repo.Ref(sha).Tree().Exists(ctx, "pending")
	require.NoError(t, err)
	require.False(t, exists)
	// Loading the immutable receipt after the remote changes must not push again.
	_, err = pushGitRef(ctx, c, baseRef, repo, "main", nil)
	require.ErrorContains(t, err, "rejected")
	forced, err := pushGitRef(ctx, c, baseRef, repo, "main", &sha)
	require.NoError(t, err)
	require.Equal(t, "FORCED", forced.Disposition)
	var loaded struct{ Node gitPushReceipt }
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `query($id:ID!) { node(id:$id) { ... on GitPushResult { ref previousSHA sha disposition } } }`, Variables: map[string]any{"id": result.ID}}, &dagger.Response{Data: &loaded}))
	require.Equal(t, sha, loaded.Node.SHA)
	require.Equal(t, baseSHA, pushRemoteSHA(ctx, t, c, service, url, "refs/heads/main"), "loading receipt must not repeat the push")
	// Empty and null both use ordinary push rules: create, no-op, or fast-forward.
	empty := ""
	created, err := pushGitRef(ctx, c, ws.Git().Head(), repo, "feature/new", &empty)
	require.NoError(t, err)
	require.Equal(t, "CREATED", created.Disposition)
	require.Empty(t, created.PreviousSHA)
	repeated, err := pushGitRef(ctx, c, ws.Git().Head(), repo, "feature/new", &empty)
	require.NoError(t, err)
	require.Equal(t, "UP_TO_DATE", repeated.Disposition)
	_, err = pushGitRef(ctx, c, baseRef, repo, "feature/new", &empty)
	require.ErrorContains(t, err, "rejected", "an empty lease must not enable force")
	_, err = pushGitRef(ctx, c, baseRef, repo, "feature/new", &baseSHA)
	require.ErrorContains(t, err, "stale info")
	_, err = pushGitRef(ctx, c, ws.Git().Head(), repo, "feature/new", &baseSHA)
	require.ErrorContains(t, err, "stale lease", "up-to-date pushes must still enforce nonempty leases")
}

func (GitSuite) TestPushValidationAndIsolation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base", "base"))
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	base := repo.Branch("main")
	sha, err := base.CommitSHA(ctx)
	require.NoError(t, err)
	for _, branch := range []string{"--all", "main:other", "bad..ref", "refs/heads/", "a\nb"} {
		_, err := pushGitRef(ctx, c, base, repo, branch, nil)
		require.ErrorContains(t, err, "invalid push destination")
	}
	_, err = pushGitRef(ctx, c, repo.Ref(sha), repo, "", nil)
	require.ErrorContains(t, err, "explicit branch")
	// A directory-backed source may contain arbitrary checkout configuration.
	// The temporary push repository must not inherit it or run its hooks.
	dir := base.Tree().WithNewFile(".git/hooks/pre-push", "#!/bin/sh\nexit 1\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
		WithNewFile(".git/config", "[core]\nrepositoryformatversion = 0\n[push]\nfollowTags = true\n[remote \"origin\"]\npushurl = file:///not-a-destination\n")
	local := dir.AsGit()
	_, err = pushGitRef(ctx, c, local.Head(), local, "main", nil)
	require.ErrorContains(t, err, "destination must be a remote")
	_, err = pushGitRef(ctx, c, local.Head(), nil, "main", nil)
	require.ErrorContains(t, err, "no remote URL")
	result, err := pushGitRef(ctx, c, local.Head(), repo, "isolated", nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", result.Disposition)
	require.Equal(t, sha, result.SHA)
}

func (GitSuite) TestPushGoSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base", "base"))
	_, err := service.Start(ctx)
	require.NoError(t, err)
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	base := repo.Branch("main")
	baseSHA, err := base.CommitSHA(ctx)
	require.NoError(t, err)
	tip := base.AsWorkspace().WithNewFile("next", "next").WithCommit("next", workspaceCommitDate).Git().Head()
	tipSHA, err := tip.CommitSHA(ctx)
	require.NoError(t, err)
	opts := dagger.GitRefPushOpts{To: repo, Branch: "sdk", ExpectedRemoteSHA: ""}
	disposition, err := base.Push(opts).Disposition(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.GitPushDispositionCreated, disposition)
	disposition, err = tip.Push(opts).Disposition(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.GitPushDispositionFastForward, disposition)
	_, err = base.Push(opts).Disposition(ctx)
	require.ErrorContains(t, err, "rejected")
	opts.ExpectedRemoteSHA = tipSHA
	disposition, err = base.Push(opts).Disposition(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.GitPushDispositionForced, disposition)
	opts.ExpectedRemoteSHA = baseSHA
	disposition, err = tip.Push(opts).Disposition(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.GitPushDispositionFastForward, disposition)
}

func (GitSuite) TestPushHTTPAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitPushHTTPService(ctx, t, c)
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service, HTTPAuthUsername: "writer", HTTPAuthToken: c.SetSecret("push-token", "push-test-password")})
	base := repo.Branch("main")
	ws := base.AsWorkspace().WithNewFile("new", "new").WithCommit("HTTP push", workspaceCommitDate)
	bad := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service, HTTPAuthUsername: "writer", HTTPAuthToken: c.SetSecret("push-wrong", "wrong-push-password")})
	_, err := pushGitRef(ctx, c, ws.Git().Head(), bad, "new", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "wrong-push-password")
	result, err := pushGitRef(ctx, c, ws.Git().Head(), repo, "new", nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", result.Disposition)
	// Defaulting to the original remote retains explicit auth on a remote ref.
	result, err = pushGitRef(ctx, c, base, nil, "http-default", nil)
	require.NoError(t, err)
	require.Equal(t, "refs/heads/http-default", result.Ref)
	// Anonymous reads must not imply anonymous writes, nor inherit the source's token.
	anonymous := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	_, err = pushGitRef(ctx, c, base, anonymous, "anonymous", nil)
	require.Error(t, err)
	header := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service,
		HTTPAuthHeader: c.SetSecret("push-header", "Basic "+base64.StdEncoding.EncodeToString([]byte("writer:push-test-password")))})
	_, err = pushGitRef(ctx, c, base, header, "header", nil)
	require.NoError(t, err)
}

func gitPushHTTPService(ctx context.Context, t *testctx.T, c *dagger.Client) (*dagger.Service, string) {
	t.Helper()
	// Public upload-pack, authenticated receive-pack.
	dir := makeGitDir(c, c.Directory().WithNewFile("base", "base"), "main")
	dir = c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).WithDirectory("/repos", dir).
		WithExec([]string{"git", "--git-dir=/repos/repo.git", "config", "http.receivepack", "true"}).Directory("/repos")
	service, url := gitSmartHTTPServiceDirAuth(ctx, t, c, "", dir, "writer", c.SetSecret("push-password", "push-test-password"), true)
	url += "/repo.git"
	_, err := service.Start(ctx)
	require.NoError(t, err)
	return service, url
}

func (GitSuite) TestPushHTTPCallerCredentials(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	config := filepath.Join(workdir, "gitconfig")
	require.NoError(t, os.WriteFile(config, []byte("[credential]\nhelper = \"!f() { echo username=writer; echo password=push-test-password; }; f\"\n"), 0o600))
	// The source worktree's .git file points outside the test container. Run
	// the caller's credential helper from a valid, independent working directory.
	c := connect(ctx, t, dagger.WithWorkdir(workdir), dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", config))
	service, url := gitPushHTTPService(ctx, t, c)
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	result, err := pushGitRef(ctx, c, repo.Branch("main"), nil, "implicit", nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", result.Disposition)
}
