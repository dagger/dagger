package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/alpine"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := &alpine.BuildInput{}
		if err := input.Decode(typedInput); err != nil {
			return nil, err
		}

		typedOutput := alpine.Build(ctx, typedInput)

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
