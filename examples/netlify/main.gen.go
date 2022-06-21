package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/netlify/sdk/netlify"
)

func main() {
	d := dagger.New()

	d.Action("deploy", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &netlify.DeployInput{}
		if err := dagger.UnmarshalBytes(ctx, input, typedInput); err != nil {
			return nil, err
		}
		typedOutput := Deploy(ctx, typedInput)
		return dagger.MarshalBytes(ctx, typedOutput)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
