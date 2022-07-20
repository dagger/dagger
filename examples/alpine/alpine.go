//go:generate go run github.com/Khan/genqlient ./gen/core/genqlient.yaml

package main

import (
	"context"

	"github.com/dagger/cloak/examples/alpine/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func Build(ctx context.Context, input dagger.Map) interface{} {
	// start with Alpine base
	output, err := core.Image(ctx, dagger.Client(ctx), "alpine:3.15")
	if err != nil {
		panic(err)
	}

	fs := output.Core.Image.Fs

	// install each of the requested packages
	for _, pkg := range input.StringList("pkgs") {
		output, err := core.Exec(ctx, dagger.Client(ctx), fs, []string{"apk", "add", "-U", "--no-cache", pkg})
		if err != nil {
			panic(err)
		}
		fs = output.Core.Exec.Fs
	}

	return fs
}
