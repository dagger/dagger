package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/alpine/sdk/alpine"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input dagger.FS) (dagger.FS, error) {
		typedInput := &alpine.BuildInput{}
		if err := dagger.Unmarshal(ctx, input, typedInput); err != nil {
			return dagger.FS{}, err
		}
		typedOutput := Build(ctx, typedInput)
		return dagger.Marshal(ctx, typedOutput)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
