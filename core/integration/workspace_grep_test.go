package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestGrepCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("root.txt", "needle at root\n").
		WithNewFile("items/a.txt", "before\n  needle one\nneedle two\nafter\n").
		WithNewFile("items/sub/b.txt", "needle nested\n").
		WithNewFile("items/sub/code.go", "needle go\n").
		WithNewFile("items/.hidden", "needle hidden\n").
		WithNewFile("items/file with spaces.txt", "needle spaces\n").
		WithNewFile("items/literal.txt", "a.b\naxb\n").
		WithNewFile("items/case.txt", "CaseNeedle\n").
		WithNewFile("items/.ignore", "ignored.txt\n").
		WithNewFile("items/ignored.txt", "needle ignored\n")
	base := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "ripgrep"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/caller/local-only.txt", "needle from caller\n").
		WithWorkdir("/caller")

	for _, kind := range []string{"local", "remote"} {
		t.Run(kind, func(ctx context.Context, t *testctx.T) {
			ctr := base
			var workspace string
			if kind == "local" {
				ctr = ctr.WithDirectory("/selected", source.WithNewDirectory(".git"))
				workspace = "/selected/items"
			} else {
				ref := workspaceSelectionRemoteRef(ctx, t, c, source)
				workspace = strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
			}

			defaultMatches := []string{
				".hidden:1:needle hidden",
				"a.txt:2:  needle one",
				"a.txt:3:needle two",
				"file with spaces.txt:1:needle spaces",
				"sub/b.txt:1:needle nested",
				"sub/code.go:1:needle go",
			}
			for _, tc := range []struct {
				name string
				args []string
				want []string
			}{
				{
					name: "default directory includes hidden files and skips ignored files",
					args: []string{"needle"},
					want: defaultMatches,
				},
				{
					name: "multiple paths",
					args: []string{"needle", "a.txt", "sub"},
					want: []string{"a.txt:2:  needle one", "a.txt:3:needle two", "sub/b.txt:1:needle nested", "sub/code.go:1:needle go"},
				},
				{
					name: "root relative directory",
					args: []string{"needle", "/items/sub"},
					want: []string{"sub/b.txt:1:needle nested", "sub/code.go:1:needle go"},
				},
				{
					name: "file with spaces",
					args: []string{"needle", "file with spaces.txt"},
					want: []string{"file with spaces.txt:1:needle spaces"},
				},
				{
					name: "parent relative file",
					args: []string{"needle", "../root.txt"},
					want: []string{"../root.txt:1:needle at root"},
				},
				{
					name: "root relative file",
					args: []string{"needle", "/root.txt"},
					want: []string{"../root.txt:1:needle at root"},
				},
				{
					name: "regular expression",
					args: []string{"a.b", "literal.txt"},
					want: []string{"literal.txt:1:a.b", "literal.txt:2:axb"},
				},
				{
					name: "fixed strings",
					args: []string{"-F", "a.b", "literal.txt"},
					want: []string{"literal.txt:1:a.b"},
				},
				{
					name: "ignore case",
					args: []string{"-i", "caseneedle", "case.txt"},
					want: []string{"case.txt:1:CaseNeedle"},
				},
				{
					name: "repeatable globs",
					args: []string{"needle", "-g", "*.txt", "-g", "*.go", "-g", "!a.txt", "-g", "!ignored.txt"},
					want: []string{"file with spaces.txt:1:needle spaces", "sub/b.txt:1:needle nested", "sub/code.go:1:needle go"},
				},
				{
					name: "files with matches",
					args: []string{"-l", "needle", "a.txt", "sub/b.txt"},
					want: []string{"a.txt", "sub/b.txt"},
				},
				{
					name: "include ignored files",
					args: []string{"--all", "needle"},
					want: append(append([]string{}, defaultMatches...), "ignored.txt:1:needle ignored"),
				},
				{
					name: "multiline",
					args: []string{"--multiline", `needle one\nneedle two`, "a.txt"},
					want: []string{"a.txt:2:  needle one", "a.txt:3:needle two"},
				},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"-W", workspace, "workspace", "grep"}, tc.args...)
					out, err := ctr.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					require.True(t, strings.HasSuffix(out, "\n"), "output must end with a newline: %q", out)
					require.ElementsMatch(t, tc.want, strings.Split(strings.TrimSuffix(out, "\n"), "\n"))
				})
			}

			t.Run("JSON includes match positions and full lines", func(ctx context.Context, t *testctx.T) {
				out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "ws", "grep", "--json", "needle one", "a.txt")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `[{
					"filePath": "a.txt",
					"lineNumber": 2,
					"matchedLines": "  needle one\n",
					"absoluteOffset": 7,
					"submatches": [{"text": "needle one", "start": 2, "end": 12}]
				}]`, out)
			})

			for _, tc := range []struct {
				name   string
				args   []string
				stdout string
				status int
				stderr string
			}{
				{name: "no matches", args: []string{"absent-pattern"}, status: 1},
				{name: "JSON no matches", args: []string{"--json", "absent-pattern"}, stdout: "[]\n", status: 1},
				{name: "missing path", args: []string{"needle", "missing"}, status: 2, stderr: "no such file or directory"},
				{name: "invalid expression", args: []string{"["}, status: 2, stderr: "unclosed character class"},
				{name: "missing pattern", status: 2},
				{name: "incompatible formats", args: []string{"--json", "-l", "needle"}, status: 2},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"dagger", "-W", workspace, "ws", "grep"}, tc.args...)
					result := ctr.WithExec(args, dagger.ContainerWithExecOpts{
						ExperimentalPrivilegedNesting: true,
						Expect:                        dagger.ReturnTypeFailure,
					})
					status, err := result.ExitCode(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.status, status)
					out, err := result.Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.stdout, out)
					if tc.stderr != "" {
						stderr, err := result.Stderr(ctx)
						require.NoError(t, err)
						require.Contains(t, strings.ToLower(stderr), tc.stderr)
					}
				})
			}
		})
	}
}
