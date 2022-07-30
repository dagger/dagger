package main

// THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

import (
	"context"

	"github.com/dagger/cloak/sdk/go/dagger"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/cloak/examples/todoapp/go/gen/netlify"
	"github.com/dagger/cloak/examples/todoapp/go/gen/todoapp"
	"github.com/dagger/cloak/examples/todoapp/go/gen/yarn"
)

type Resolver struct{}

func (r *queryResolver) Build(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "build")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *queryResolver) Test(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "test")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *queryResolver) Deploy(ctx context.Context, src dagger.FSID, token dagger.SecretID) (*Deploy, error) {
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
	deployOutput, err := netlify.Deploy(ctx, buildOutput.Todoapp.Build.ID, "build", "test-cloak-netlify-deploy", token)
	if err != nil {
		return nil, err
	}
	return &Deploy{
		URL:       deployOutput.Netlify.Deploy.Url,
		DeployURL: deployOutput.Netlify.Deploy.DeployUrl,
	}, nil
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() *queryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){
		"Build": func(rctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var err error
			fc.Args, err = (&executionContext{}).field_Query_build_args(rctx, fc.Args)
			if err != nil {
				return nil, err
			}
			obj, ok := fc.ParentResult.(struct{})
			_ = ok
			_ = obj
			qr := &queryResolver{}
			return qr.Query().Build(rctx, fc.Args["src"].(dagger.FSID))
		},
		"Test": func(rctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var err error
			fc.Args, err = (&executionContext{}).field_Query_test_args(rctx, fc.Args)
			if err != nil {
				return nil, err
			}
			obj, ok := fc.ParentResult.(struct{})
			_ = ok
			_ = obj
			qr := &queryResolver{}
			return qr.Query().Test(rctx, fc.Args["src"].(dagger.FSID))
		},
		"Deploy": func(rctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var err error
			fc.Args, err = (&executionContext{}).field_Query_deploy_args(rctx, fc.Args)
			if err != nil {
				return nil, err
			}
			obj, ok := fc.ParentResult.(struct{})
			_ = ok
			_ = obj
			qr := &queryResolver{}
			return qr.Query().Deploy(rctx, fc.Args["src"].(dagger.FSID), fc.Args["token"].(dagger.SecretID))
		},
	})
}
