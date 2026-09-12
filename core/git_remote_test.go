package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestRemoteFromCacheResultAcceptsStringPayload(t *testing.T) {
	payloadRemote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "refs/heads/main", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		Symrefs: map[string]string{
			"HEAD": "refs/heads/main",
		},
	}
	payload, err := json.Marshal(payloadRemote)
	require.NoError(t, err)

	remote, err := remoteFromCacheResult(string(payload))
	require.NoError(t, err)
	require.Len(t, remote.Refs, 1)
	require.Equal(t, payloadRemote.Refs[0].Name, remote.Refs[0].Name)
	require.Equal(t, payloadRemote.Refs[0].SHA, remote.Refs[0].SHA)
	require.Equal(t, payloadRemote.Symrefs["HEAD"], remote.Symrefs["HEAD"])
}

func TestRemoteFromCacheResultRejectsInvalidPayload(t *testing.T) {
	_, err := remoteFromCacheResult("{not-json")
	require.ErrorContains(t, err, "decode cached remote")
}

func TestRemoteMetadataCacheKeyIsolation(t *testing.T) {
	ctx := context.Background()

	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)

	remotePayload := `{"refs":[]}`
	_, err = cacheIface.GetOrInitArbitrary(ctx, "git-remote-test-session", "git-remote-test-dedicated-key", dagql.ArbitraryValueFunc(remotePayload))
	require.NoError(t, err)

	gitInitCalls := 0
	gitPayload := `{"refs":[{"name":"refs/heads/main","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`
	res, err := cacheIface.GetOrInitArbitrary(ctx, "git-remote-test-session", "git-current-call-key", func(context.Context) (any, error) {
		gitInitCalls++
		return gitPayload, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, gitInitCalls, "unrelated call key should not be aliased to remote metadata payload")
	require.Equal(t, gitPayload, res.Value())
}

func TestNamedFetchRefSpecs(t *testing.T) {
	commitSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	refs := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  commitSHA,
			},
		},
		{
			Ref: &gitutil.Ref{
				Name: "refs/tags/v1.0.0",
				SHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
		{
			Ref: &gitutil.Ref{
				SHA: "cccccccccccccccccccccccccccccccccccccccc",
			},
		},
		{
			Ref: &gitutil.Ref{
				Name: commitSHA,
				SHA:  commitSHA,
			},
		},
		nil,
	}

	specs := namedFetchRefSpecs(refs)
	require.Len(t, specs, 2, "only named, non-commit refs should generate fallback specs")
	require.Equal(t, namedFetchRefSpecs(refs), specs, "fallback specs should be deterministic")

	for _, spec := range specs {
		src, dst, ok := strings.Cut(spec, ":")
		require.True(t, ok)
		require.NotEmpty(t, src)
		require.True(t, strings.HasPrefix(dst, "refs/dagger.fetch/"))
		require.NotContains(t, dst, ":", "fallback destination ref should be a valid git ref component")
	}
}

func TestNamedFetchRefSpecsChangesWithPinnedSHA(t *testing.T) {
	base := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
	updated := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}

	baseSpecs := namedFetchRefSpecs(base)
	updatedSpecs := namedFetchRefSpecs(updated)
	require.Len(t, baseSpecs, 1)
	require.Len(t, updatedSpecs, 1)
	require.NotEqual(t, baseSpecs[0], updatedSpecs[0], "fallback destination should track pinned ref+sha pair")
}

// Remote Git commands must keep maintenance inside the caller's mirror lock.
func TestRemoteGitMaintenanceFinishesBeforeFetchReturns(t *testing.T) {
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	mirror := filepath.Join(root, "mirror")
	setupGit := gitutil.NewGitCLI(gitutil.WithConfig(map[string]string{
		"user.name": "Test", "user.email": "test@example.invalid",
		"maintenance.auto": "false",
	}))
	gitRun := func(cli *gitutil.GitCLI, args ...string) string {
		t.Helper()
		out, err := cli.Run(ctx, args...)
		require.NoError(t, err, "%v", args)
		return strings.TrimSpace(string(out))
	}
	gitRun(setupGit, "init", "--quiet", "--initial-branch=main", origin)
	originGit := setupGit.New(gitutil.WithDir(origin))
	var commits []string
	for _, value := range []string{"first", "second", "third"} {
		require.NoError(t, os.WriteFile(filepath.Join(origin, "value"), []byte(value), 0600))
		gitRun(originGit, "add", "value")
		gitRun(originGit, "commit", "--quiet", "-m", value)
		commits = append(commits, gitRun(originGit, "rev-parse", "HEAD"))
	}
	gitRun(setupGit, "init", "--bare", "--quiet", mirror)
	mirrorGit := setupGit.New(gitutil.WithGitDir(mirror))
	gitRun(mirrorGit, "remote", "add", "origin", "file://"+origin)
	// Small real packs make automatic maintenance eligible without a large repo.
	gitRun(mirrorGit, "config", "fetch.unpackLimit", "1")
	gitRun(mirrorGit, "config", "gc.autoPackLimit", "1")
	// New Git versions use geometric repacking instead of the gc task.
	gitRun(mirrorGit, "config", "maintenance.geometric-repack.auto", "-1")
	gitRun(mirrorGit, "fetch", "--depth=1", "origin", commits[0])
	gitRun(mirrorGit, "update-ref", "refs/heads/keep", commits[0])

	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0700))
	ready := filepath.Join(root, "repack-ready")
	release := filepath.Join(root, "repack-release")
	// Only pause the real maintenance child; every command still executes Git.
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = repack ]; then
 touch %q
 attempts=0
 while [ ! -e %q ]; do
  # An early test failure can remove the fixture before release is observed.
  if [ ! -d %q ] || [ "$attempts" -ge 500 ]; then
   echo "repack release was not observed" >&2
   exit 1
  fi
  attempts=$((attempts + 1))
  sleep .01
 done
fi
exec %q "$@"
`, ready, release, root, realGit)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700))
	t.Cleanup(func() { require.NoError(t, os.WriteFile(release, nil, 0600)) })
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: t.Name()})
	ctx = ContextWithQuery(ctx, &Query{Server: &remoteGitMaintenanceServer{}})
	remoteGit, cleanup, err := new(RemoteGitRepository).setup(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	remoteGit = remoteGit.New(gitutil.WithGitDir(mirror), gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
		// Local file transport needs no mount namespace or network override.
		cmd.Env = append(cmd.Env, "GIT_EXEC_PATH="+bin)
		return runProcessGroup(ctx, cmd)
	}))
	fetch := func(sha string) error {
		_, err := remoteGit.Run(ctx, "fetch", "--progress", "--no-tags", "--update-head-ok", "--force", "--depth=1", "origin", sha)
		return err
	}
	done := make(chan error, 1)
	go func() { done <- fetch(commits[1]) }()
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "automatic repack did not start")
	select {
	case err := <-done:
		t.Fatalf("fetch returned before its maintenance finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, os.WriteFile(release, nil, 0600))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("fetch did not finish after releasing maintenance")
	}
	require.NoError(t, fetch(commits[2]))
	require.Equal(t, "third", gitRun(remoteGit, "show", commits[2]+":value"))
}

type remoteGitMaintenanceServer struct{ *mockServer }

func (*remoteGitMaintenanceServer) DNS() *oci.DNSConfig { return &oci.DNSConfig{} }
