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

func (r *todoappResolver) Build(ctx context.Context, obj *Todoapp, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "build")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoappResolver) Test(ctx context.Context, obj *Todoapp, src dagger.FSID) (*dagger.Filesystem, error) {
	output, err := yarn.Script(ctx, src, "test")
	if err != nil {
		return nil, err
	}
	return &output.Yarn.Script, nil
}

func (r *todoappResolver) Deploy(ctx context.Context, obj *Todoapp, src dagger.FSID, token dagger.SecretID) (*DeployURLs, error) {
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
		DeployURL: deployOutput.Netlify.Deploy.DeployURL,
	}, nil
}

func (r *deployURLsResolver) URL(ctx context.Context, obj *DeployURLs) (string, error) {

	return obj.URL, nil

}

func (r *deployURLsResolver) DeployURL(ctx context.Context, obj *DeployURLs) (string, error) {

	return obj.DeployURL, nil

}

func (r *deployURLsResolver) LogsURL(ctx context.Context, obj *DeployURLs) (*string, error) {

	return obj.LogsURL, nil

}

func (r *queryResolver) Todoapp(ctx context.Context) (*Todoapp, error) {

	return new(Todoapp), nil

}

type deployURLsResolver struct{}
type queryResolver struct{}
type todoappResolver struct{}

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){
		"DeployURLs.url": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(DeployURLs)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployURLsResolver{}).URL(ctx,

				obj,
			)
		},
		"DeployURLs.deployURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(DeployURLs)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployURLsResolver{}).DeployURL(ctx,

				obj,
			)
		},
		"DeployURLs.logsURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(DeployURLs)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployURLsResolver{}).LogsURL(ctx,

				obj,
			)
		},
		"Query.todoapp": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			return (&queryResolver{}).Todoapp(ctx)
		},
		"Todoapp.build": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			var src dagger.FSID

			bytes, err = json.Marshal(fc.Args["src"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &src); err != nil {
				return nil, err
			}

			obj := new(Todoapp)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Build(ctx,

				obj,

				src,
			)
		},
		"Todoapp.test": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			var src dagger.FSID

			bytes, err = json.Marshal(fc.Args["src"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &src); err != nil {
				return nil, err
			}

			obj := new(Todoapp)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Test(ctx,

				obj,

				src,
			)
		},
		"Todoapp.deploy": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

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

			obj := new(Todoapp)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&todoappResolver{}).Deploy(ctx,

				obj,

				src,

				token,
			)
		},
	})
}
