package testutil

import (
	"context"

	"dagger.io/dagger"
)

type QueryOptions struct {
	Variables map[string]any
	Operation string
}

func Query(query string, res any, opts *QueryOptions, clientOpts ...dagger.ClientOpt) error {
	ctx := context.Background()

	if opts == nil {
		opts = &QueryOptions{}
	}
	if opts.Variables == nil {
		opts.Variables = make(map[string]any)
	}

	c, err := dagger.Connect(ctx, clientOpts...)
	if err != nil {
		return err
	}
	defer c.Close()

	return c.Do(ctx,
		&dagger.Request{
			Query:     query,
			Variables: opts.Variables,
			OpName:    opts.Operation,
		},
		&dagger.Response{Data: &res},
	)
}
