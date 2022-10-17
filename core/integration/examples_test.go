package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger"
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
					Stdout struct {
						Contents string
					}
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
							exec(args: ["curl", "--version"]) {
								stdout {
									contents
								}
							}
						}
					}
				}`,
		},
		resp,
	)
	require.NoError(t, err)
	require.NotEmpty(t, data.Alpine.Build.Exec.Stdout.Contents)
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

func TestExtensionYarn(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(
		ctx,
		dagger.WithWorkdir("../../"),
		dagger.WithConfigPath("../../examples/yarn/dagger.json"),
	)
	require.NoError(t, err)
	defer c.Close()

	dirID, err := c.Core().Host().Workdir().Read().ID(ctx)
	require.NoError(t, err)

	data := struct {
		Yarn struct {
			Script struct {
				Contents []string
			}
		}
	}{}
	resp := &dagger.Response{Data: &data}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestYarn($source: DirectoryID!) {
				yarn {
					script(source: $source, runArgs: ["build"]) {
						contents(path: "sdk/nodejs/dagger/dist")
					}
				}
			}`,
			Variables: map[string]any{
				"source": dirID,
			},
		},
		resp,
	)
	require.NoError(t, err)
	require.NotEmpty(t, data.Yarn.Script.Contents)

	data2 := struct {
		Directory struct {
			Yarn struct {
				Contents []string
			}
		}
	}{}
	resp2 := &dagger.Response{Data: &data2}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestYarn($source: DirectoryID!) {
				directory(id: $source) {
					yarn(runArgs: ["build"]) {
						contents(path: "sdk/nodejs/dagger/dist")
					}
				}
			}`,
			Variables: map[string]any{
				"source": dirID,
			},
		},
		resp2,
	)
	require.NoError(t, err)
	require.NotEmpty(t, data2.Directory.Yarn.Contents)
}
