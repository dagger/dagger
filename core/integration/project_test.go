package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/engine"
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
