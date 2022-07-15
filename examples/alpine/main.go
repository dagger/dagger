package main

import (
	"context"
	"encoding/json"

	dagger "github.com/dagger/cloak/sdk/go"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx context.Context, input []byte) ([]byte, error) {
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
