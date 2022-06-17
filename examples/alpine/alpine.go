//go:generate go run ../../stub -m ./model.gen.go -f ./frontend/main.gen.go
package alpine

import (
	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/dagger/core"
)

func doBuild(ctx *dagger.Context, input *BuildInput) *BuildOutput {
	output := &BuildOutput{}
	output.fs = core.Image(&core.ImageInput{Ref: "alpine:3.15.0"}).FS()
	for _, pkg := range input.Packages {
		output.fs = core.Exec(&core.ExecInput{
			Base: output.fs,
			Dir:  "/",
			Args: []string{"apk", "add", "-U", "--no-cache", pkg},
		}).FS()
	}
	return output
}
