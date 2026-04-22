package core

// Workspace alignment: mostly aligned; coverage targets post-workspace current-module introspection semantics, but setup still relies on historical module helpers.
// Scope: `dag.CurrentModule()` access to generated context, dependencies, module identity, source, and workdir helpers.
// Intent: Keep current-module introspection and workdir behavior separate from self-calls and the remaining umbrella runtime coverage.

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestCurrentModuleAPI(ctx context.Context, t *testctx.T) {
	t.Run("generatedContextDirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", `package main

			import "context"
			import "dagger/test/internal/dagger"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().GeneratedContextDirectory()
			}
			`,
			).
			With(daggerCall("fn", "export", "--path=./out")).
			Directory("out").
			Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger.gen.go")
	})

	t.Run("dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "depA")).
			With(daggerExec("init", "--sdk=go", "depB")).
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "./depA")).
			With(daggerExec("install", "./depB")).
			WithNewFile("/work/main.go", `package main

			import "context"
			import "sort"
			import "strings"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) {
				deps, err := dag.CurrentModule().Dependencies(ctx)
				if err != nil {
					return "", err
				}

				var depNames []string
				for _, dep := range deps {
					depName, err := dep.Name(ctx)
					if err != nil {
						return "", err
					}

					depNames = append(depNames, depName)
				}

				sort.Strings(depNames)

				return strings.Join(depNames, ","), nil
			}
			`,
			).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out, "depA,depB")
	})

	t.Run("name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=WaCkY", "--sdk=go")).
			WithNewFile("/work/main.go", `package main

			import "context"

			type WaCkY struct {}

			func (m *WaCkY) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/subdir/coolfile.txt", "nice").
			WithNewFile("/work/main.go", `package main

			import (
				"context"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *dagger.File {
				return dag.CurrentModule().Source().File("subdir/coolfile.txt")
			}
			`,
			).
			With(daggerCall("fn", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("workdir", func(ctx context.Context, t *testctx.T) {
		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.Directory, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().Workdir("subdir/moresubdir"), nil
			}
			`,
				).
				With(daggerCall("fn", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.File, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().WorkdirFile("subdir/moresubdir/coolfile.txt"), nil
			}
			`,
				).
				With(daggerCall("fn", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			func New() (*Test, error) {
				if err := os.WriteFile("/rootfile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}
				if err := os.MkdirAll("/foo", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("/foo/foofile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}

				return &Test{}, nil
			}

			type Test struct {}

			func (m *Test) EscapeFile(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("../rootfile.txt")
			}

			func (m *Test) EscapeFileAbs(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("/rootfile.txt")
			}

			func (m *Test) EscapeDir(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("../foo")
			}

			func (m *Test) EscapeDirAbs(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("/foo")
			}
			`,
				)

			_, err := ctr.
				With(daggerCall("escape-file", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-file-abs", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir-abs", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}
