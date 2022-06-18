package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/netlify/sdk/netlify"
)

func main() {
	d := dagger.New()

	d.Action("deploy", func(ctx *dagger.Context, input dagger.FS) (dagger.FS, error) {
		typedInput := &netlify.DeployInput{}
		if err := dagger.Unmarshal(ctx, input, typedInput); err != nil {
			return dagger.FS{}, err
		}
		typedOutput := Deploy(ctx, typedInput)
		return dagger.Marshal(ctx, typedOutput)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
