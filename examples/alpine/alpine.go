//go:generate go run ../../stub -m ./sdk/alpine/model.gen.go -f ./main.gen.go
package main

import (
	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/examples/alpine/sdk/alpine"
	"github.com/dagger/cloak/examples/core/sdk/core"
)

func Build(ctx *dagger.Context, input *alpine.BuildInput) *alpine.BuildOutput {
	output := &alpine.BuildOutput{}

	// start with Alpine base
	output.FS = core.Image(ctx, &core.ImageInput{Ref: "alpine:3.15.0"}).FS

	// install each of the requested packages
	for _, pkg := range input.Packages {
		output.FS = core.Exec(ctx, &core.ExecInput{
			FS:   output.FS,
			Dir:  "/",
			Args: []string{"apk", "add", "-U", "--no-cache", pkg},
		}).FS
	}

	return output
}
