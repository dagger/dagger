package main

// THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

import (
	"context"
	"fmt"

	"github.com/dagger/cloak/examples/alpine/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"
)

type Resolver struct{}

func (r *queryResolver) Build(ctx context.Context, pkgs []string) (*dagger.Filesystem, error) {
	// start with Alpine base
	output, err := core.Image(ctx, "alpine:3.15")
	if err != nil {
		return nil, err
	}

	fs := &output.Core.Image

	// install each of the requested packages
	for _, pkg := range pkgs {
		output, err := core.Exec(ctx, fs.ID, core.ExecInput{
			Args:    []string{"apk", "add", "-U", "--no-cache", pkg},
			Workdir: "/mnt",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to install %s: %s", pkg, err)
		}
		fs = output.Core.Filesystem.Exec.Fs
	}

	return fs, nil
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
			return qr.Query().Build(rctx, fc.Args["pkgs"].([]string))
		},
	})
}
