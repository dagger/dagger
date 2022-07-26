package main

// THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

import (
	"context"

	"github.com/dagger/cloak/examples/todoapp/go/gen/netlify"
	"github.com/dagger/cloak/examples/todoapp/go/gen/todoapp"
	"github.com/dagger/cloak/examples/todoapp/go/gen/todoapp/generated"
	"github.com/dagger/cloak/examples/todoapp/go/gen/todoapp/model"
	"github.com/dagger/cloak/examples/todoapp/go/gen/yarn"
	"github.com/dagger/cloak/sdk/go/dagger"
	"golang.org/x/sync/errgroup"
)

type Resolver struct{}

func (r *queryResolver) Build(ctx context.Context, src dagger.FS) (dagger.FS, error) {
	output, err := yarn.Script(ctx, src, "build")
	if err != nil {
		return "", err
	}
	return output.Yarn.Script, nil
}

func (r *queryResolver) Test(ctx context.Context, src dagger.FS) (dagger.FS, error) {
	output, err := yarn.Script(ctx, src, "test")
	if err != nil {
		return "", err
	}
	return output.Yarn.Script, nil
}

func (r *queryResolver) Deploy(ctx context.Context, src dagger.FS, token *string) (*model.Deploy, error) {
	// run build and test in parallel
	var eg errgroup.Group
	var buildOutput *todoapp.BuildResponse
	eg.Go(func() error {
		var err error
		buildOutput, err = todoapp.Build(ctx, src)
		return err
	})
	eg.Go(func() error {
		_, err := todoapp.Test(ctx, src)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// if build+test succeeded, deploy
	deployOutput, err := netlify.Deploy(ctx, buildOutput.Todoapp.Build, "build", "test-cloak-netlify-deploy", *token)
	if err != nil {
		return nil, err
	}
	return &model.Deploy{
		URL:       deployOutput.Netlify.Deploy.Url,
		DeployURL: deployOutput.Netlify.Deploy.DeployUrl,
	}, nil
}

// Query returns generated.QueryResolver implementation.
func (r *Resolver) Query() generated.QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
