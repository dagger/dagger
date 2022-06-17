package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/netlify/sdk/netlify"
)

func main() {
	d := dagger.New()

	d.Action("deploy", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := &netlify.DeployInput{}
		if err := input.Decode(typedInput); err != nil {
			return nil, err
		}

		typedOutput := Deploy(ctx, typedInput)

		output := &dagger.Output{}
		if err := output.Encode(typedOutput); err != nil {
			return nil, err
		}

		return output, nil
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
