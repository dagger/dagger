package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
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

func gitMirrorTestRun(t testing.TB, dir string, args ...string) string {
	t.Helper()
	git := gitutil.NewGitCLI(gitutil.WithDir(dir))
	out, err := git.Run(context.Background(), args...)
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// These use file:// (not Git's local-copy optimization), with packet and raw
// pack tracing, so negotiation regressions are visible in actual wire bytes.
func gitMirrorTestSource(t *testing.T) (source, base, next string) {
	t.Helper()
	source = t.TempDir()
	gitMirrorTestRun(t, source, "init", "--quiet", "--initial-branch=main")
	random := rand.New(rand.NewPCG(1, 2))
	commitTime := int64(1_000_000_000)
	commit := func(message string) {
		commitTime++
		git := gitutil.NewGitCLI(gitutil.WithDir(source), gitutil.WithConfig(map[string]string{
			"user.name": "Dagger", "user.email": "dagger@localhost",
		}), gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
			date := fmt.Sprintf("%d +0000", commitTime)
			cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
			return cmd.Run()
		}))
		_, err := git.Run(context.Background(), "commit", "-m", message)
		require.NoError(t, err)
	}
	writeRandom := func(name string, size int) {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(random.Uint32())
		}
		require.NoError(t, os.WriteFile(filepath.Join(source, name), data, 0o644))
	}
	writeRandom("history", 64*1024)
	gitMirrorTestRun(t, source, "add", ".")
	commit("history")
	gitMirrorTestRun(t, source, "rm", "history")
	writeRandom("large", 256*1024)
	gitMirrorTestRun(t, source, "add", ".")
	commit("base")
	base = gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "tag", "base")
	require.NoError(t, os.WriteFile(filepath.Join(source, "small"), []byte("new"), 0o644))
	gitMirrorTestRun(t, source, "add", ".")
	commit("next")
	next = gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	return source, base, next
}

func gitMirrorTestRepo(t *testing.T, source string) (*RemoteGitRepository, *gitutil.GitCLI, string) {
	t.Helper()
	mirror := t.TempDir()
	gitMirrorTestRun(t, mirror, "init", "--bare", "--quiet")
	gitMirrorTestRun(t, mirror, "remote", "add", "origin", "file://"+source)
	// Local transport is limited to this fixture, not accepted by the public API.
	url := &gitutil.GitURL{Scheme: "file", Path: source}
	return &RemoteGitRepository{URL: url}, gitutil.NewGitCLI(gitutil.WithGitDir(mirror)), mirror
}

func gitMirrorTestFetch(t *testing.T, repo *RemoteGitRepository, git *gitutil.GitCLI, sha string, depth int) {
	t.Helper()
	ctx := context.Background()
	refs, err := gitRefsToFetch(ctx, git, depth, []*RemoteGitRef{{Ref: &gitutil.Ref{SHA: sha}}})
	require.NoError(t, err)
	require.NoError(t, repo.fetchObjects(ctx, git, depth, false, refs))
}

func gitMirrorTestTrace(t *testing.T, git *gitutil.GitCLI, fn func(*gitutil.GitCLI)) (string, int) {
	t.Helper()
	traceDir := t.TempDir()
	git = git.New(gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		cmd.Env = append(cmd.Env, "GIT_TRACE_PACKET="+filepath.Join(traceDir, "packets"), "GIT_TRACE_PACKFILE="+filepath.Join(traceDir, "pack"))
		return cmd.Run()
	}))
	fn(git)
	readTrace := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(traceDir, name))
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		return data
	}
	return string(readTrace("packets")), len(readTrace("pack"))
}

func TestGitMirrorFetchNegotiation(t *testing.T) {
	source, base, next := gitMirrorTestSource(t)
	for _, legacy := range []bool{true, false} {
		for _, initialDepth := range []int{0, 1} {
			t.Run(fmt.Sprintf("legacy=%v/depth=%d", legacy, initialDepth), func(t *testing.T) {
				repo, git, mirror := gitMirrorTestRepo(t, source)
				fetch := func(sha string, depth int) (string, int) {
					return gitMirrorTestTrace(t, git, func(git *gitutil.GitCLI) {
						if !legacy {
							gitMirrorTestFetch(t, repo, git, sha, depth)
							return
						}
						// Reproduce the old fetch: no destination ref, only FETCH_HEAD.
						args := []string{"fetch", "--no-tags", "--force"}
						if depth > 0 {
							args = append(args, fmt.Sprintf("--depth=%d", depth))
						} else if _, err := os.Stat(filepath.Join(mirror, "shallow")); err == nil {
							args = append(args, "--unshallow")
						}
						_, err := git.Run(context.Background(), append(args, "origin", sha)...)
						require.NoError(t, err)
					})
				}
				_, initialBytes := fetch(base, initialDepth)
				if initialDepth == 1 {
					require.Equal(t, "1", gitMirrorTestRun(t, mirror, "rev-list", "--count", base))
					_, fullBytes := fetch(base, 0)
					t.Logf("initial shallow=%d bytes; unshallow=%d bytes", initialBytes, fullBytes)
					// Full history really is missing initially; retaining tips cannot
					// eliminate that transfer, but should not resend the large tree.
					require.Greater(t, fullBytes, 64*1024)
					if !legacy {
						require.Less(t, fullBytes, 128*1024)
					}
				}
				require.Equal(t, "2", gitMirrorTestRun(t, mirror, "rev-list", "--count", base))
				packets, nextBytes := fetch(next, 1)
				t.Logf("new depth1 tip=%d bytes; have base=%v", nextBytes, strings.Contains(packets, "fetch> have "+base))
				if legacy {
					require.NotContains(t, packets, "fetch> have "+base)
					require.Greater(t, nextBytes, 256*1024)
				} else {
					require.Contains(t, packets, "fetch> have "+base)
					require.Less(t, nextBytes, 4096, "existing large blob must not transfer again")
				}
				_, restoredBytes := fetch(next, 0)
				t.Logf("restore full history=%d bytes", restoredBytes)
				require.Equal(t, "3", gitMirrorTestRun(t, mirror, "rev-list", "--count", next))
				require.Equal(t, "65536", gitMirrorTestRun(t, mirror, "cat-file", "-s", base+"^:history"))
				if !legacy {
					// Git can resend history when removing a shallow boundary even
					// with negotiation tips. This change does not widen depth=1
					// requests to avoid those boundaries.
					require.Less(t, restoredBytes, 128*1024)
					packets, cachedBytes := fetch(next, 0)
					require.Empty(t, packets, "complete cached refs need no remote contact")
					require.Zero(t, cachedBytes)
				}
			})
		}
	}
}

func TestGitMirrorFetchRetainsExistingCommits(t *testing.T) {
	source, base, _ := gitMirrorTestSource(t)
	for _, depth := range []int{0, 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			repo, git, mirror := gitMirrorTestRepo(t, source)
			// An existing mirror created before negotiation tips were retained.
			args := []string{"fetch", "--no-tags"}
			if depth > 0 {
				args = append(args, "--depth=1")
			}
			_, err := git.Run(context.Background(), append(args, "origin", base)...)
			require.NoError(t, err)
			require.Empty(t, gitMirrorTestRun(t, mirror, "for-each-ref", "--format=%(refname)"))
			packets, packBytes := gitMirrorTestTrace(t, git, func(git *gitutil.GitCLI) {
				gitMirrorTestFetch(t, repo, git, base, 0)
			})
			require.Equal(t, base, gitMirrorTestRun(t, mirror, "rev-parse", fetchedGitRef(base)))
			if depth == 0 {
				require.Empty(t, packets)
				require.Zero(t, packBytes)
			} else {
				require.Contains(t, packets, "fetch> have "+base)
				require.Less(t, packBytes, 128*1024)
			}
		})
	}
}

func TestGitMirrorFetchPrivateRefs(t *testing.T) {
	source, base, next := gitMirrorTestSource(t)
	repo, git, mirror := gitMirrorTestRepo(t, source)
	gitMirrorTestFetch(t, repo, git, base, 1)
	gitMirrorTestFetch(t, repo, git, next, 1)
	require.Equal(t, "1", gitMirrorTestRun(t, mirror, "rev-list", "--count", next), "successive shallow fetches must stay shallow")
	require.Empty(t, gitMirrorTestRun(t, mirror, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags", "refs/remotes"))
	require.Equal(t, base, gitMirrorTestRun(t, mirror, "rev-parse", fetchedGitRef(base)))
	require.Equal(t, next, gitMirrorTestRun(t, mirror, "rev-parse", fetchedGitRef(next)))
	checkout := t.TempDir()
	checkoutGit := gitutil.NewGitCLI(gitutil.WithWorkTree(checkout), gitutil.WithGitDir(filepath.Join(checkout, ".git")))
	require.NoError(t, doGitCheckout(context.Background(), checkoutGit, nil, "file://"+mirror, &gitutil.Ref{SHA: next}, 1, false))
	require.Empty(t, gitMirrorTestRun(t, checkout, "for-each-ref", "--format=%(refname)"), "private mirror refs and unrequested tags must not leak into checkout")
	require.Equal(t, "new", gitMirrorTestRun(t, checkout, "show", "HEAD:small"))
}

func TestGitMirrorFetchNamedFallback(t *testing.T) {
	source, base, _ := gitMirrorTestSource(t)
	for _, depth := range []int{0, 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			repo, git, mirror := gitMirrorTestRepo(t, source)
			rejections := 0
			git = git.New(gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
				for _, arg := range cmd.Args {
					if arg == base+":"+fetchedGitRef(base) {
						rejections++
						return gitutil.ErrSHAFetchUnsupported
					}
				}
				return cmd.Run()
			}))
			// The branch moved to next after base was resolved. Only a full
			// fallback fetch can materialize the pinned base in this fixture.
			err := repo.fetchObjects(context.Background(), git, depth, false, []*RemoteGitRef{{Ref: &gitutil.Ref{Name: "refs/heads/main", SHA: base}}})
			require.Equal(t, 1, rejections)
			if depth == 0 {
				require.NoError(t, err)
				require.Equal(t, base, gitMirrorTestRun(t, mirror, "rev-parse", fetchedGitRef(base)))
				require.Equal(t, fetchedGitRef(base), gitMirrorTestRun(t, mirror, "for-each-ref", "--format=%(refname)", "refs/dagger.fetched"))
				require.Empty(t, gitMirrorTestRun(t, mirror, "for-each-ref", "--format=%(refname)", "refs/dagger.fetch", "refs/heads", "refs/tags"))
			} else {
				require.ErrorContains(t, err, "did not materialize expected sha")
				require.Empty(t, gitMirrorTestRun(t, mirror, "for-each-ref", "--format=%(refname)", "refs/dagger.fetched", "refs/dagger.fetch"), "failed fallback must not retain an incorrect tip")
			}
		})
	}
}

func TestGitCheckoutContentOnlyDepthWithSubmodule(t *testing.T) {
	ctx := context.Background()
	submodule := t.TempDir()
	gitBundleTestRun(t, submodule, "init", "--quiet", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(submodule, "value"), []byte("pinned"), 0o644))
	gitBundleTestRun(t, submodule, "add", ".")
	gitBundleTestRun(t, submodule, "commit", "-m", "pinned submodule")
	pinned := gitBundleTestRun(t, submodule, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(submodule, "value"), []byte("later"), 0o644))
	gitBundleTestRun(t, submodule, "commit", "-am", "later submodule")
	source := t.TempDir()
	gitBundleTestRun(t, source, "init", "--quiet", "--initial-branch=main")
	gitBundleTestRun(t, source, "-c", "protocol.file.allow=always", "submodule", "add", "file://"+submodule, "module")
	gitBundleTestRun(t, filepath.Join(source, "module"), "checkout", pinned)
	gitBundleTestRun(t, source, "add", ".")
	gitBundleTestRun(t, source, "commit", "-m", "submodule at older commit")
	gitBundleTestRun(t, source, "commit", "--allow-empty", "-m", "later parent")
	head := gitBundleTestRun(t, source, "rev-parse", "HEAD")
	for _, depth := range []int{0, 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			root := t.TempDir()
			git := gitutil.NewGitCLI(gitutil.WithDir(root), gitutil.WithWorkTree(root),
				gitutil.WithGitDir(filepath.Join(root, ".git")),
				// Only this private test fixture allows local submodule URLs.
				gitutil.WithArgs("-c", "protocol.file.allow=always"))
			require.NoError(t, doGitCheckout(ctx, git, nil, "file://"+source, &gitutil.Ref{SHA: head}, depth, true))
			data, err := os.ReadFile(filepath.Join(root, "module", "value"))
			require.NoError(t, err)
			require.Equal(t, "pinned", string(data), "parent depth does not change the gitlink checkout")
			_, err = os.Stat(filepath.Join(root, ".git"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

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
