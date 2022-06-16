package main

import (
	"github.com/dagger/cloak/dagger"

	// TODO: this needs to be generated based on which schemas are re-used in this schema
	"github.com/dagger/cloak/dagger/core"
)

func main() {
	d := dagger.New()

	d.Action("build", func(ctx *dagger.Context, input *dagger.Input) (*dagger.Output, error) {
		typedInput := BuildInput{}
		if err := input.Decode(&typedInput); err != nil {
			return nil, err
		}

		typedOutput, err := DoBuild(ctx, typedInput)
		if err != nil {
			return nil, err
		}

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

type BuildInput struct {
	Packages []string `json:"packages,omitempty"`
}

type BuildOutput struct {
	FS core.FSOutput
}

/* TODO: need to have safe way of generating these skeletons such that we don't overwrite any existing user code in an irrecoverable way. Remember that this includes import statements too.
func DoBuild(ctx *dagger.Context, input alpine.Build) (output alpine.buildOutput, rerr error) {
  panic("implement me")
}
*/
