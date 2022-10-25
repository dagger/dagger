package core

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
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

func TestExtensionNetlify(t *testing.T) {
	t.Skip("Skipping test until shared netlify tokens are supported here, feel free to run locally though")

	runner := func(lang string) func(*testing.T) {
		return func(t *testing.T) {
			ctx := context.Background()
			c, err := dagger.Connect(
				ctx,
				dagger.WithWorkdir("../../"),
				dagger.WithConfigPath(fmt.Sprintf("../../examples/netlify/%s/dagger.json", lang)),
			)
			require.NoError(t, err)
			defer c.Close()

			dirID, err := c.Host().Workdir().Read().ID(ctx)
			require.NoError(t, err)

			secretID, err := c.Host().EnvVariable("NETLIFY_AUTH_TOKEN").Secret().ID(ctx)
			require.NoError(t, err)

			data := struct {
				Netlify struct {
					Deploy struct {
						URL string
					}
				}
			}{}
			resp := &dagger.Response{Data: &data}
			err = c.Do(ctx,
				&dagger.Request{
					Query: `query TestNetlify(
						$source: DirectoryID!,
						$token: SecretID!,
					) {
						netlify {
							deploy(
								contents: $source,
								siteName: "test-cloak-netlify-deploy",
								token: $token,
							) {
								url
							}
						}
					}`,
					Variables: map[string]any{
						"source": dirID,
						"token":  secretID,
					},
				},
				resp,
			)
			require.NoError(t, err)
			require.NotEmpty(t, data.Netlify.Deploy.URL)
		}
	}

	for _, lang := range []string{"go", "ts"} {
		t.Run(lang, runner(lang))
	}
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

	dirID, err := c.Host().Workdir().Read().ID(ctx)
	require.NoError(t, err)

	data := struct {
		Yarn struct {
			Script struct {
				Entries []string
			}
		}
	}{}
	resp := &dagger.Response{Data: &data}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestYarn($source: DirectoryID!) {
				yarn {
					script(source: $source, runArgs: ["build"]) {
						entries(path: "sdk/nodejs/dagger/dist")
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
	require.NotEmpty(t, data.Yarn.Script.Entries)

	data2 := struct {
		Directory struct {
			Yarn struct {
				Entries []string
			}
		}
	}{}
	resp2 := &dagger.Response{Data: &data2}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestYarn($source: DirectoryID!) {
				directory(id: $source) {
					yarn(runArgs: ["build"]) {
						entries(path: "sdk/nodejs/dagger/dist")
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
	require.NotEmpty(t, data2.Directory.Yarn.Entries)
}
