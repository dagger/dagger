package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/testutil"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func TestExtensionMount(t *testing.T) {
	startOpts := &engine.Config{
		Workdir:    "../../",
		ConfigPath: "core/integration/testdata/extension/cloak.yaml",
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		res := struct {
			Core struct {
				Filesystem struct {
					WriteFile struct {
						ID string `json:"id"`
					}
				}
			}
		}{}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `{
					core {
						filesystem(id: "scratch") {
							writeFile(path: "/foo", contents: "bar") {
								id
							}
						}
					}
				}`,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)

		res2 := struct {
			Test struct {
				TestMount string
			}
		}{}
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestMount($in: FSID!) {
					test {
						testMount(in: $in)
					}
				}`,
				Variables: map[string]any{
					"in": res.Core.Filesystem.WriteFile.ID,
				},
			},
			&graphql.Response{Data: &res2},
		)
		require.NoError(t, err)
		require.Equal(t, res2.Test.TestMount, "bar")

		return nil
	})
	require.NoError(t, err)
}

func TestGoGenerate(t *testing.T) {
	tmpdir := t.TempDir()

	daggerYamlPath := filepath.Join(tmpdir, "cloak.yaml")
	err := os.WriteFile(daggerYamlPath, []byte(`
name: testgogenerate
scripts:
  - path: .
    sdk: go
`), 0644) // #nosec G306
	require.NoError(t, err)

	goModPath := filepath.Join(tmpdir, "go.mod")
	err = os.WriteFile(goModPath, []byte(`
module testgogenerate
go 1.19
`), 0644) // #nosec G306
	require.NoError(t, err)

	startOpts := &engine.Config{
		LocalDirs: map[string]string{
			"testgogenerate": tmpdir,
		},
	}

	err = engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		data := struct {
			Core struct {
				Filesystem struct {
					LoadProject struct {
						GeneratedCode dagger.Filesystem
					}
				}
			}
		}{}
		resp := &graphql.Response{Data: &data}

		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `
			query GeneratedCode($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadProject(configPath: $configPath) {
							generatedCode {
								id
							}
						}
					}
				}
			}`,
				Variables: map[string]any{
					"fs":         ctx.LocalDirs["testgogenerate"],
					"configPath": ctx.ConfigPath,
				},
			},
			resp,
		)
		require.NoError(t, err)

		generatedFSID := data.Core.Filesystem.LoadProject.GeneratedCode.ID

		_, err = testutil.ReadFile(ctx, ctx.Client, generatedFSID, "main.go")
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)
}
