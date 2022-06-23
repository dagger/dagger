package main

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/examples/alpine/sdk/alpine"
)

func main() {
	err := dagger.Client(func(ctx *dagger.Context) error {
		output := alpine.Build(ctx, &alpine.BuildInput{
			Packages: dagger.ToStrings("bash", "curl", "jq"),
		})

		root := output.Root()
		root.Evaluate(ctx)

		bytes, err := json.Marshal(output)
		if err != nil {
			panic(err)
		}

		fmt.Printf("%s\n", string(bytes))

		if err := dagger.Shell(ctx, output.Root()); err != nil {
			panic(err)
		}

		return nil
	})
	if err != nil {
		panic(err)
	}
}
