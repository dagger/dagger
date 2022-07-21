package main

import (
	"context"

	"github.com/dagger/cloak/examples/alpine/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"
)

type Alpine struct{}

func (r *Alpine) Build(ctx context.Context, pkgs []string) (dagger.FS, error) {
	// start with Alpine base
	output, err := core.Image(ctx, dagger.Client(ctx), "alpine:3.15")
	if err != nil {
		return dagger.FS(""), err
	}

	fs := output.Core.Image.Fs

	// install each of the requested packages
	for _, pkg := range pkgs {
		output, err := core.Exec(ctx, dagger.Client(ctx), fs, []string{"apk", "add", "-U", "--no-cache", pkg})
		if err != nil {
			return dagger.FS(""), err
		}
		fs = output.Core.Exec.Fs
	}

	return fs, nil
}
