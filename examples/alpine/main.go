package main

import (
	"encoding/json"

	"github.com/dagger/cloak/dagger"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		typedInput := &dagger.AlpineBuildInput{}
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
