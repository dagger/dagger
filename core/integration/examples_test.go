package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func TestExtensionAlpine(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(
		ctx,
		dagger.WithWorkdir("../../"),
		dagger.WithConfigPath("../../examples/alpine/dagger.json"),
	)
	require.NoError(t, err)
	defer c.Close()

	data := struct {
		Alpine struct {
			Build struct {
				Exec struct {
					Stdout string
				}
			}
		}
	}{}
	resp := &dagger.Response{Data: &data}
	err = c.Do(ctx,
		&dagger.Request{
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
}

func TestExtensionNetlifyGo(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(
		ctx,
		dagger.WithWorkdir("../../"),
		dagger.WithConfigPath("../../examples/netlify/go/dagger.json"),
	)
	require.NoError(t, err)
	defer c.Close()

	// TODO: until we setup some shared netlify auth tokens, this test just asserts on the schema showing up

	res := struct {
		Project struct {
			Schema string
		}
	}{}
	resp := &dagger.Response{Data: &res}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `{
					project(name: "netlify") {
						schema
					}
				}`,
		},
		resp,
	)
	require.NoError(t, err)
	require.NotEmpty(t, res.Project.Schema)
}
