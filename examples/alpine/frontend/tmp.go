package main

import (
	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/dagger/core"
)

// TODO: temporarily in this tmp.go file until there's a safe way of not overwriting custom code like this when regenerating stubs
func DoBuild(ctx *dagger.Context, input BuildInput) (output BuildOutput, rerr error) {
	// TODO: hate that you have to wrap in (), try builder pattern alternative?
	output.FS = (&core.Image{Ref: "alpine:3.15.0"}).FS()
	for _, pkg := range input.Packages {
		output.FS = (&core.Exec{
			Base: output.FS,
			Dir:  "/",
			Args: []string{"apk", "add", "-U", "--no-cache", pkg},
		}).FS()
	}
	return output, nil
}
