package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceGitHostCheckoutReused checks that reading a host checkout's
// HEAD and dirty state reuses the engine's reconstruction of its repository
// across clients until the checkout's refs move, and then extends it with only
// what the checkout gained, while every read still sees the checkout as it is.
func (WorkspaceSuite) TestWorkspaceGitHostCheckoutReused(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	home := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+home,
			"GIT_AUTHOR_NAME=Dagger Tests", "GIT_AUTHOR_EMAIL=dagger@example.com",
			"GIT_COMMITTER_NAME=Dagger Tests", "GIT_COMMITTER_EMAIL=dagger@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	commit := func(name string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644))
		git("add", name)
		git("commit", "-qm", name)
		return git("rev-parse", "HEAD")
	}

	type read struct {
		head  string
		clean bool
		// packs holds, for each pack of the checkout, whether it was
		// relative to an earlier reconstruction.
		packs []bool
	}
	// Each read is a new client, as each dagger invocation is.
	readCheckout := func() read {
		t.Helper()
		sink := newAgentTraceSink(t)
		c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(repo), dagger.WithLogOutput(io.Discard))...)
		got, err := testutil.QueryWithClient[struct {
			CurrentWorkspace struct {
				Git struct {
					Head struct {
						CommitSha string `json:"commitSha"`
					} `json:"head"`
					Uncommitted struct {
						IsEmpty bool `json:"isEmpty"`
					} `json:"uncommitted"`
				} `json:"git"`
			} `json:"currentWorkspace"`
		}](c, t, `{ currentWorkspace { git { head { commitSha } uncommitted { isEmpty } } } }`, nil)
		require.NoError(t, err)
		require.NoError(t, c.Close())

		r := read{head: got.CurrentWorkspace.Git.Head.CommitSha, clean: got.CurrentWorkspace.Git.Uncommitted.IsEmpty}
		traces, _ := sink.capture()
		for _, request := range traces {
			for _, resource := range request.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						if span.Name != "pack host git checkout" || span.EndTimeUnixNano <= span.StartTimeUnixNano {
							continue
						}
						relative := false
						for _, attr := range span.Attributes {
							if attr.Key == "dagger.git.checkout.pack.relative" {
								relative = attr.Value.GetBoolValue()
							}
						}
						r.packs = append(r.packs, relative)
					}
				}
			}
		}
		return r
	}

	git("init", "-q", "-b", "main")
	first := commit("a.txt")

	r := readCheckout()
	require.Equal(t, read{head: first, clean: true, packs: []bool{false}}, r, "the first read packs the whole checkout")

	r = readCheckout()
	require.Equal(t, read{head: first, clean: true}, r, "an unchanged checkout is not packed again")

	second := commit("b.txt")
	r = readCheckout()
	require.Equal(t, read{head: second, clean: true, packs: []bool{true}}, r, "a new commit is packed relative to the last reconstruction")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.txt"), []byte("edited\n"), 0o644))
	r = readCheckout()
	require.Equal(t, read{head: second, clean: false}, r, "an uncommitted change is seen without packing")

	git("checkout", "-q", "--", "b.txt")
	git("checkout", "-q", "-b", "topic", first)
	r = readCheckout()
	require.Equal(t, read{head: first, clean: true, packs: []bool{true}}, r, "a moved HEAD is seen, with nothing new to pack")
}
