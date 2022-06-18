package alpine

import (
	"github.com/dagger/cloak/dagger"
)

type BuildInput struct {
	Packages []string `json:"packages,omitempty"`
}

type BuildOutput struct {
	FS dagger.FS `json:"fs,omitempty"`
}

func Build(ctx *dagger.Context, input *BuildInput) *BuildOutput {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

	fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:alpine", "build", fsInput)
	if err != nil {
		panic(err)
	}
	output := &BuildOutput{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
	return output
}
