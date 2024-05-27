package testutil

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/testctx"
)

type QueryOptions struct {
	Operation string
	Variables map[string]any
	Secrets   map[string]string
}

func Query(t *testctx.T, query string, res any, opts *QueryOptions, clientOpts ...dagger.ClientOpt) error {
	ctx := t.Context()

	if opts == nil {
		opts = &QueryOptions{}
	}
	if opts.Variables == nil {
		opts.Variables = make(map[string]any)
	}
	if opts.Secrets == nil {
		opts.Secrets = make(map[string]string)
	}

	clientOpts = append([]dagger.ClientOpt{
		dagger.WithLogOutput(NewTWriter(t)),
	}, clientOpts...)

	c, err := dagger.Connect(ctx, clientOpts...)
	if err != nil {
		return err
	}
	defer c.Close()

	for n, v := range opts.Secrets {
		s, err := newSecret(ctx, c, n, v)
		if err != nil {
			return err
		}
		opts.Variables[n] = s
	}

	return c.Do(ctx,
		&dagger.Request{
			Query:     query,
			Variables: opts.Variables,
			OpName:    opts.Operation,
		},
		&dagger.Response{Data: &res},
	)
}

func newSecret(ctx context.Context, c *dagger.Client, name, value string) (*core.SecretID, error) {
	query := `query Secret($name: String!, $value: String!) {
        setSecret(name: $name, plaintext: $value) {
            id
        }
    }`
	var res struct {
		SetSecret struct {
			ID core.SecretID
		}
	}
	err := c.Do(ctx,
		&dagger.Request{
			Query: query,
			Variables: map[string]string{
				"name":  name,
				"value": value,
			},
		},
		&dagger.Response{
			Data: &res,
		},
	)
	if err != nil {
		return nil, err
	}
	return &res.SetSecret.ID, nil
}
