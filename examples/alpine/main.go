package main

import (
	"context"
	"encoding/json"

	"github.com/dagger/cloak/sdk/go/dagger"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx context.Context, input []byte) ([]byte, error) {
		inputMap := make(map[string]interface{})
		if err := json.Unmarshal(input, &inputMap); err != nil {
			return nil, err
		}
		return json.Marshal(Build(ctx, dagger.Map{inputMap}))
	})

	if err := d.Serve(); err != nil {
		panic(err)
	}
}
