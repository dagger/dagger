package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/cloak/examples/alpine/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func (r *alpineResolver) Build(ctx context.Context, pkgs []string) (*dagger.Filesystem, error) {
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

type alpineResolver struct{}

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){

		"Build": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			var err error

			var pkgs []string

			bytes, err = json.Marshal(fc.Args["pkgs"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &pkgs); err != nil {
				return nil, err
			}

			return (&alpineResolver{}).Build(ctx,

				pkgs,
			)
		},
	})
}
