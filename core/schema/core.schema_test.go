package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/stretchr/testify/require"
)

func TestCoreImage(t *testing.T) {
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		require.NoError(t, err)

		res := struct {
			Core struct {
				Image struct {
					File string
				}
			}
		}{}
		err = cl.MakeRequest(ctx,
			&graphql.Request{
				Query: `
					{
						core {
							image(ref: "alpine:3.16.2") {
								file(path: "/etc/alpine-release")
							}
						}
					}
				`,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)
		require.NotEmpty(t, res.Core.Image.File)
		require.Equal(t, res.Core.Image.File, "3.16.2\n")

		return nil
	}))
}

func TestCoreGit(t *testing.T) {
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		require.NoError(t, err)

		res := struct {
			Core struct {
				Git struct {
					File string
				}
			}
		}{}
		err = cl.MakeRequest(ctx,
			&graphql.Request{
				Query: `
					{
						core {
							git(remote: "github.com/dagger/dagger") {
								file(path: "README.md")
							}
						}
					}
				`,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)
		require.NotEmpty(t, res.Core.Git.File)
		require.Contains(t, res.Core.Git.File, "dagger")

		return nil
	}))
}
