package testutil

import (
	"context"
	"os"

	"github.com/Khan/genqlient/graphql"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/buildkitd"
	"go.dagger.io/dagger/sdk/go/dagger"
)

type QueryOptions struct {
	Variables map[string]any
	Secrets   map[string]string
	Operation string
}

func Query(query string, res any, opts *QueryOptions) error {
	if opts == nil {
		opts = &QueryOptions{}
	}
	if opts.Variables == nil {
		opts.Variables = make(map[string]any)
	}
	if opts.Secrets == nil {
		opts.Secrets = make(map[string]string)
	}
	return engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		if err := addSecrets(ctx, ctx.Client, opts); err != nil {
			return err
		}

		return ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query:     query,
				Variables: opts.Variables,
				OpName:    opts.Operation,
			},
			&graphql.Response{Data: &res},
		)
	})
}

func addSecrets(ctx context.Context, cl graphql.Client, opts *QueryOptions) error {
	for name, plaintext := range opts.Secrets {
		addSecret := struct {
			Core struct {
				AddSecret dagger.SecretID
			}
		}{}
		err := cl.MakeRequest(ctx,
			&graphql.Request{
				Query: `query AddSecret($plaintext: String!) {
					core {
						addSecret(plaintext: $plaintext)
					}
				}`,
				Variables: map[string]string{
					"plaintext": plaintext,
				},
			},
			&graphql.Response{Data: &addSecret},
		)
		if err != nil {
			return err
		}
		opts.Variables[name] = addSecret.Core.AddSecret
	}
	return nil
}

func ReadFile(ctx context.Context, cl graphql.Client, fsid dagger.FSID, path string) (string, error) {
	data := struct {
		Core struct {
			Filesystem struct {
				File string
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query ReadFile($fs: FSID!, $path: String!) {
				core {
					filesystem(id: $fs) {
						file(path: $path)
					}
				}
			}`,
			Variables: map[string]any{
				"fs":   fsid,
				"path": path,
			},
		},
		resp,
	)
	if err != nil {
		return "", err
	}
	return data.Core.Filesystem.File, nil
}

func SetupBuildkitd() error {
	host, err := buildkitd.StartGoModBuildkitd(context.Background())
	if err != nil {
		return err
	}
	os.Setenv("BUILDKIT_HOST", host)
	return nil
}
