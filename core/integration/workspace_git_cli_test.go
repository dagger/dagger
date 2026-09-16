package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestGitCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Keep main behind feature so remote selections must use the requested ref.
	// A broken module also proves that metadata commands skip module loading.
	repo := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithEnvVariable("GIT_AUTHOR_NAME", "Workspace Author").
		WithEnvVariable("GIT_AUTHOR_EMAIL", "author@example.com").
		WithEnvVariable("GIT_AUTHOR_DATE", "2024-01-02T03:04:05Z").
		WithEnvVariable("GIT_COMMITTER_NAME", "Workspace Committer").
		WithEnvVariable("GIT_COMMITTER_EMAIL", "committer@example.com").
		WithEnvVariable("GIT_COMMITTER_DATE", "2024-02-03T04:05:06Z").
		WithWorkdir("/repo").
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile(".gitignore", "*.ignored\n").
		WithNewFile("tracked.txt", "initial\n").
		WithNewFile("items/dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("items/nested.txt", "nested\n").
		WithNewFile("items/deep/nested.txt", "deep\n").
		WithExec([]string{"git", "init", "-b", "main"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithExec([]string{"git", "checkout", "-b", "feature"}).
		WithExec([]string{"sh", "-c", `
set -eu
for i in 1 2 3 4 5 6 7 8 9 10 11; do
  printf '%s\n' "$i" > tracked.txt
  git add tracked.txt
  git commit -m "change $i" -m "Body for change $i."
done
`}).
		WithExec([]string{"git", "tag", "v1.2.3"}).
		WithExec([]string{"git", "branch", "v1.2.3", "main"})

	commitsOut, err := repo.WithExec([]string{"git", "rev-list", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	commits := strings.Fields(commitsOut)
	require.Len(t, commits, 12)

	remote := workspaceSelectionRemoteRef(ctx, t, c, repo.
		WithExec([]string{"git", "checkout", "main"}).Directory("/repo"))
	remoteURL := strings.TrimSuffix(remote, "@main")
	base := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/caller/local-only.txt", "caller\n").
		WithWorkdir("/caller")

	for _, kind := range []string{"local", "remote"} {
		t.Run(kind, func(ctx context.Context, t *testctx.T) {
			ctr := base
			workspace := remoteURL + "#feature:items"
			if kind == "local" {
				ctr = ctr.WithDirectory("/selected", repo.Directory("/repo"))
				workspace = "/selected/items"
			}

			for _, tc := range []struct {
				name string
				args []string
				want string
			}{
				{"ref", []string{"ref"}, "refs/heads/feature\n"},
				{"sha", []string{"sha"}, commits[0] + "\n"},
				{"clean", []string{"dirty"}, "false\n"},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"-W", workspace, "workspace", "git"}, tc.args...)
					out, err := ctr.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, out)
				})
			}

			for _, tc := range []struct {
				name  string
				args  []string
				count int
			}{
				{"default log limit", nil, 10},
				{"explicit log limit", []string{"--limit", "2"}, 2},
				{"log limit exceeds history", []string{"--limit", "20"}, 12},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"-W", workspace, "ws", "git", "log"}, tc.args...)
					out, err := ctr.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					var want strings.Builder
					for i, sha := range commits[:tc.count] {
						headline := "initial"
						if i < 11 {
							headline = fmt.Sprintf("change %d", 11-i)
						}
						fmt.Fprintf(&want, "%s %s\n", sha[:7], headline)
					}
					require.Equal(t, want.String(), out)
				})
			}

			t.Run("JSON log metadata", func(ctx context.Context, t *testctx.T) {
				out, err := ctr.With(workspaceSelectionDaggerExec(
					"-W", workspace, "ws", "git", "log", "--limit", "2", "--json",
				)).Stdout(ctx)
				require.NoError(t, err)
				var want []map[string]any
				for i, sha := range commits[:2] {
					headline := fmt.Sprintf("change %d", 11-i)
					body := fmt.Sprintf("Body for change %d.", 11-i)
					want = append(want, map[string]any{
						"sha":             sha,
						"shortSha":        sha[:7],
						"authorName":      "Workspace Author",
						"authorEmail":     "author@example.com",
						"authoredDate":    "2024-01-02T03:04:05Z",
						"committerName":   "Workspace Committer",
						"committerEmail":  "committer@example.com",
						"committedDate":   "2024-02-03T04:05:06Z",
						"message":         headline + "\n\n" + body,
						"messageHeadline": headline,
						"messageBody":     body,
						"parentShas":      []string{commits[i+1]},
					})
				}
				wantJSON, err := json.Marshal(want)
				require.NoError(t, err)
				require.JSONEq(t, string(wantJSON), out)
			})

			t.Run("URL preserves selected directory", func(ctx context.Context, t *testctx.T) {
				withOrigin := ctr
				if kind == "local" {
					withOrigin = ctr.WithExec([]string{"git", "-C", "/selected", "remote", "add", "origin", remoteURL})
				}
				out, err := withOrigin.With(workspaceSelectionDaggerExec(
					"-W", workspace, "ws", "git", "url",
				)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, remoteURL+"#refs/heads/feature:items\n", out)
				address := strings.TrimSpace(out)

				out, err = base.With(workspaceSelectionDaggerExec(
					"-W", address, "ws", "cat", "nested.txt",
				)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "nested\n", out)

				out, err = c.Address(address).Directory().File("nested.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "nested\n", out)
			})

			t.Run("URL below nested workspace configuration", func(ctx context.Context, t *testctx.T) {
				withOrigin := ctr
				selected := remoteURL + "#feature:items/deep"
				if kind == "local" {
					withOrigin = ctr.WithExec([]string{"git", "-C", "/selected", "remote", "add", "origin", remoteURL})
					selected = "/selected/items/deep"
				}
				out, err := withOrigin.With(workspaceSelectionDaggerExec(
					"-W", selected, "ws", "git", "url",
				)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, remoteURL+"#refs/heads/feature:items/deep\n", out)
				out, err = c.Address(strings.TrimSpace(out)).Directory().File("nested.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "deep\n", out)
			})

			t.Run("unnamed commit", func(ctx context.Context, t *testctx.T) {
				detached := ctr
				detachedWorkspace := remoteURL + "#" + commits[1] + ":items"
				if kind == "local" {
					detached = ctr.WithExec([]string{"git", "-C", "/selected", "checkout", "--detach", commits[1]})
					detachedWorkspace = workspace
					head, err := detached.File("/selected/.git/HEAD").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, commits[1]+"\n", head)
				}
				for _, command := range []string{"ref", "sha", "url"} {
					t.Run(command, func(ctx context.Context, t *testctx.T) {
						selected := detached
						want := commits[1] + "\n"
						if command == "url" {
							want = remoteURL + "#" + commits[1] + ":items\n"
							if kind == "local" {
								selected = selected.WithExec([]string{"git", "-C", "/selected", "remote", "add", "origin", remoteURL})
							}
						}
						out, err := selected.With(workspaceSelectionDaggerExec(
							"-W", detachedWorkspace, "ws", "git", command,
						)).Stdout(ctx)
						require.NoError(t, err)
						require.Equal(t, want, out)
					})
				}
			})

			if kind == "remote" {
				t.Run("tag ref", func(ctx context.Context, t *testctx.T) {
					out, err := ctr.With(workspaceSelectionDaggerExec(
						"-W", remoteURL+"#refs/tags/v1.2.3:items", "ws", "git", "ref",
					)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "refs/tags/v1.2.3\n", out)
				})
				for _, tc := range []struct {
					name    string
					address string
				}{
					{"tag URL", remoteURL + "#refs/tags/v1.2.3:items"},
					{"version query URL", remoteURL + "/items@v1"},
				} {
					t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
						out, err := ctr.With(workspaceSelectionDaggerExec(
							"-W", tc.address, "ws", "git", "url",
						)).Stdout(ctx)
						require.NoError(t, err)
						require.Equal(t, remoteURL+"#refs/tags/v1.2.3:items\n", out)
						// A branch with the same name points at another commit.
						out, err = ctr.With(workspaceSelectionDaggerExec(
							"-W", strings.TrimSpace(out), "ws", "git", "sha",
						)).Stdout(ctx)
						require.NoError(t, err)
						require.Equal(t, commits[0]+"\n", out)
					})
				}
				return
			}

			t.Run("URL without origin", func(ctx context.Context, t *testctx.T) {
				result := ctr.WithExec([]string{"dagger", "-W", workspace, "ws", "git", "url"}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				})
				out, err := result.Stdout(ctx)
				require.NoError(t, err)
				require.Empty(t, out)
				stderr, err := result.Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, "origin")
			})

			for _, tc := range []struct {
				name   string
				change func(*dagger.Container) *dagger.Container
				want   string
			}{
				{"tracked change outside cwd", func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithNewFile("/selected/tracked.txt", "changed\n")
				}, "true\n"},
				{"staged change", func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithNewFile("/selected/tracked.txt", "staged\n").
						WithExec([]string{"git", "-C", "/selected", "add", "tracked.txt"})
				}, "true\n"},
				{"untracked file", func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithNewFile("/selected/untracked.txt", "untracked\n")
				}, "true\n"},
				{"ignored untracked file", func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithNewFile("/selected/generated.ignored", "ignored\n")
				}, "false\n"},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					out, err := tc.change(ctr).With(workspaceSelectionDaggerExec(
						"-W", workspace, "ws", "git", "dirty",
					)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, out)
				})
			}
		})
	}

	t.Run("non-Git workspace", func(ctx context.Context, t *testctx.T) {
		ctr := base.WithNewFile("/plain/dagger.toml", "[modules]\n")
		for _, command := range []string{"ref", "sha", "dirty", "log", "url"} {
			t.Run(command, func(ctx context.Context, t *testctx.T) {
				result := ctr.WithExec([]string{"dagger", "-W", "/plain", "ws", "git", command}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				})
				out, err := result.Stdout(ctx)
				require.NoError(t, err)
				require.Empty(t, out)
				stderr, err := result.Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, "workspace is not in a git repository")
			})
		}
	})
}
