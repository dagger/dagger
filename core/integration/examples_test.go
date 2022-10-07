package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/engine"
)

func TestExtensionAlpine(t *testing.T) {
	startOpts := &engine.Config{
		Workdir:    "../../",
		ConfigPath: "examples/alpine/cloak.yaml",
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		data := struct {
			Alpine struct {
				Build struct {
					Exec struct {
						Stdout string
					}
				}
			}
		}{}
		resp := &graphql.Response{Data: &data}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `
				query {
					alpine {
						build(pkgs: ["curl"]) {
							exec(input:{args: ["curl", "--version"]}) {
								stdout
							}
						}
					}
				}`,
			},
			resp,
		)
		require.NoError(t, err)
		require.NotEmpty(t, data.Alpine.Build.Exec.Stdout)

		return nil
	})
	require.NoError(t, err)
}

func TestExtensionNetlifyGo(t *testing.T) {
	startOpts := &engine.Config{
		Workdir:    "../../",
		ConfigPath: "examples/netlify/go/cloak.yaml",
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		// TODO: until we setup some shared netlify auth tokens, this test just asserts on the schema showing up

		res := struct {
			Core struct {
				Project struct {
					Schema string
				}
			}
		}{}
		resp := &graphql.Response{Data: &res}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `{
					core {
						project(name: "netlify") {
							schema
						}
					}
				}`,
			},
			resp,
		)
		require.NoError(t, err)
		require.NotEmpty(t, res.Core.Project.Schema)

		return nil
	})
	require.NoError(t, err)
}
