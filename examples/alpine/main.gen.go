package main

import (
	"encoding/json"

	"github.com/dagger/cloak/dagger"

	// TODO: need more generic mechanism for generating this import
	"github.com/dagger/cloak/examples/alpine/sdk/alpine"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &alpine.BuildInput{}
		if err := json.Unmarshal(input, typedInput); err != nil {
			return nil, err
		}
		typedOutput := Build(ctx, typedInput)
		return json.Marshal(typedOutput)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
