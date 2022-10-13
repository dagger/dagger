package testutil

import (
	"context"
	"os"

	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/buildkitd"
	"go.dagger.io/dagger/sdk/go/dagger"
)

type QueryOptions struct {
	Variables map[string]any
	Operation string
}

func Query(query string, res any, opts *QueryOptions) error {
	return QueryWithEngineConfig(query, res, opts, nil)
}

func QueryWithEngineConfig(query string, res any, opts *QueryOptions, cfg *engine.Config) error {
	ctx := context.Background()

	if opts == nil {
		opts = &QueryOptions{}
	}
	if opts.Variables == nil {
		opts.Variables = make(map[string]any)
	}

	clientOpts := []dagger.ClientOpt{}
	if cfg != nil {
		clientOpts = append(clientOpts,
			dagger.WithWorkdir(cfg.Workdir),
			dagger.WithConfigPath(cfg.ConfigPath),
		)
		for id, path := range cfg.LocalDirs {
			clientOpts = append(clientOpts, dagger.WithLocalDir(id, path))
		}
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

func SetupBuildkitd() error {
	host, err := buildkitd.StartGoModBuildkitd(context.Background())
	if err != nil {
		return err
	}
	os.Setenv("BUILDKIT_HOST", host)
	return nil
}
