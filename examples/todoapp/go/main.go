package main

import (
	"context"
	"encoding/json"

	"github.com/dagger/cloak/examples/todoapp/go/gen/netlify"
	"github.com/dagger/cloak/examples/todoapp/go/gen/todoapp"
	"github.com/dagger/cloak/examples/todoapp/go/gen/yarn"
	"github.com/dagger/cloak/sdk/go/dagger"
	"golang.org/x/sync/errgroup"
)

func (r *todoappResolver) Build(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "build")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoappResolver) Test(ctx context.Context, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "test")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoappResolver) Deploy(ctx context.Context, src dagger.FSID, token dagger.SecretID) (*DeployURLs, error) {
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
	return &DeployURLs{
		URL:       deployOutput.Netlify.Deploy.Url,
		DeployURL: deployOutput.Netlify.Deploy.DeployUrl,
	}, nil
}

type todoappResolver struct{}

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){

		"Build": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			var err error

			var src dagger.FSID

			bytes, err = json.Marshal(fc.Args["src"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &src); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Build(ctx,

				src,
			)
		},

		"Test": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			var err error

			var src dagger.FSID

			bytes, err = json.Marshal(fc.Args["src"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &src); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Test(ctx,

				src,
			)
		},

		"Deploy": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			var err error

			var src dagger.FSID

			bytes, err = json.Marshal(fc.Args["src"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &src); err != nil {
				return nil, err
			}

			var token dagger.SecretID

			bytes, err = json.Marshal(fc.Args["token"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &token); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Deploy(ctx,

				src,

				token,
			)
		},
	})
}
