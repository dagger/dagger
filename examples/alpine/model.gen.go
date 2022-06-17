package alpine

import (
	"github.com/dagger/cloak/dagger"

	// TODO: this needs to be generated based on which schemas are re-used in this schema
	"github.com/dagger/cloak/dagger/core"
)

type BuildInput struct {
	Packages []string `json:"packages,omitempty"`
}

type BuildOutput struct {
	fs core.FSOutput
}

func (a *BuildOutput) FS() core.FSOutput {
	return a.fs
}

func Build(ctx *dagger.Context, input *BuildInput) *BuildOutput {
	output := &BuildOutput{}
	if err := dagger.Do(ctx, "localhost:5555/dagger:alpine", "build", input, output, doBuild); err != nil {
		panic(err)
	}
	return output
}

var _ func(ctx *dagger.Context, input *BuildInput) *BuildOutput = doBuild
