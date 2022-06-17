package alpine

import (
	"encoding/json"

	"github.com/dagger/cloak/dagger"

	// TODO: this needs to be generated based on which schemas are re-used in this schema
	"github.com/dagger/cloak/dagger/core"
)

type BuildInput struct {
	Packages []string `json:"packages,omitempty"`
}

type BuildOutput struct {
	FS core.FSOutput
}

func Build(ctx *dagger.Context, input *BuildInput) *BuildOutput {
	rawInput, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}

	rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:alpine", "build", string(rawInput))
	if err != nil {
		panic(err)
	}
	output := &BuildOutput{}
	if err := rawOutput.Decode(output); err != nil {
		panic(err)
	}
	return output
}
