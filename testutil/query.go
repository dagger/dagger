package testutil

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
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
	return engine.Start(context.Background(), nil, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		if err := addSecrets(ctx, cl, opts); err != nil {
			return err
		}

		return cl.MakeRequest(ctx,
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
