package main

import (
	"encoding/json"

	"github.com/dagger/cloak/dagger"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input []byte) ([]byte, error) {
		inputMap := make(map[string]interface{})
		if err := json.Unmarshal(input, &inputMap); err != nil {
			return nil, err
		}
		outputMap := Build(ctx, inputMap)
		return json.Marshal(outputMap)
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
