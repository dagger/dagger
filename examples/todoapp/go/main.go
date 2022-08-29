package main

import (
	"context"

	"github.com/dagger/cloak/examples/todoapp/go/gen/netlify"
	"github.com/dagger/cloak/examples/todoapp/go/gen/yarn"
	"github.com/dagger/cloak/sdk/go/dagger"
	"golang.org/x/sync/errgroup"
)

func (r *todoapp) build(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, []string{"build"})
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoapp) test(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, []string{"test"})
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoapp) deploy(ctx context.Context, src dagger.FSID, token dagger.SecretID) (*DeployURLs, error) {
	// run build and test in parallel
	var eg errgroup.Group
	var buildOutput *dagger.Filesystem
	eg.Go(func() error {
		var err error
		buildOutput, err = r.build(ctx, src)
		return err
	})
	eg.Go(func() error {
		_, err := r.test(ctx, src)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// if build+test succeeded, deploy
	deployOutput, err := netlify.Deploy(ctx, buildOutput.ID, "build", "test-cloak-netlify-deploy", token)
	if err != nil {
		return nil, err
	}
	return &DeployURLs{
		URL:       deployOutput.Netlify.Deploy.Url,
		DeployURL: deployOutput.Netlify.Deploy.DeployURL,
	}, nil
}
